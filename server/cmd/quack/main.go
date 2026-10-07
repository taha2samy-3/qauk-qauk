// Command quack is the Quack Quack backend: API, realtime gateway, ingester,
// migrations and admin tooling in one binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/config"
	"github.com/taha2samy/quackquack/server/internal/db"
	"github.com/taha2samy/quackquack/server/internal/gateway"
	"github.com/taha2samy/quackquack/server/internal/httpserver"
	"github.com/taha2samy/quackquack/server/internal/importdjango"
	"github.com/taha2samy/quackquack/server/internal/history"
	_ "github.com/taha2samy/quackquack/server/internal/history/clickhouse" // history drivers
	_ "github.com/taha2samy/quackquack/server/internal/history/timescale"
	"github.com/taha2samy/quackquack/server/internal/ingest"
	"github.com/taha2samy/quackquack/server/internal/metrics"
	"github.com/taha2samy/quackquack/server/internal/outbox"
	"github.com/taha2samy/quackquack/server/internal/store"
)

const usage = `quack - Quack Quack backend

Usage:
  quack serve            Run the HTTP server (roles from QUACK_ROLES: api,gateway)
  quack ingest           Run the history ingester (Redpanda -> history store)
  quack migrate          Apply database migrations and create Redpanda topics
  quack admin <cmd>      Admin tasks: create-user, set-password, import-key
  quack import-django    Import data from the legacy Django database
  quack dev <cmd>        Development helpers: seed, hook (contract tests)

Configuration is read from QUACK_* environment variables (see internal/config).
`

func main() {
	// All timestamps the server emits (REST and WebSocket) are UTC (RFC 3339).
	time.Local = time.UTC
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "serve":
		err = withConfig(ctx, serve)
	case "ingest":
		err = withConfig(ctx, runIngest)
	case "migrate":
		err = withConfig(ctx, migrate)
	case "admin":
		err = withConfig(ctx, func(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
			return adminCmd(ctx, cfg, log, args)
		})
	case "import-django":
		err = withConfig(ctx, func(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
			return importDjango(ctx, cfg, log, args)
		})
	case "dev":
		err = withConfig(ctx, func(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
			return devCmd(ctx, cfg, log, args)
		})
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func withConfig(ctx context.Context, fn func(context.Context, *config.Config, *slog.Logger) error) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	return fn(ctx, cfg, newLogger(cfg.LogLevel))
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
	slog.SetDefault(log)
	return log
}

func openPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	return retry(ctx, "database", func() (*pgxpool.Pool, error) { return db.Open(ctx, cfg.DatabaseURL) })
}

// openHistory opens the time-series store (QUACK_HISTORY_DRIVER / _URL).
func openHistory(ctx context.Context, cfg *config.Config) (history.Store, error) {
	targets := history.ParseTargets(cfg.HistoryDriver, cfg.HistoryURL, cfg.DatabaseURL)
	settings := history.Settings{Retention: cfg.HistoryRetention, CompressAfter: cfg.HistoryCompressAfter}
	return retry(ctx, "history store", func() (history.Store, error) { return history.Open(ctx, targets, settings) })
}

// retry waits for a dependency (useful under docker compose start ordering).
func retry[T any](ctx context.Context, what string, fn func() (T, error)) (T, error) {
	deadline := time.Now().Add(60 * time.Second)
	for {
		v, err := fn()
		if err == nil || time.Now().After(deadline) || ctx.Err() != nil {
			return v, err
		}
		slog.Warn("waiting for dependency", "dependency", what, "err", err)
		select {
		case <-ctx.Done():
			return v, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func migrate(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	if _, err := retry(ctx, "database", func() (struct{}, error) { return struct{}{}, db.Migrate(ctx, cfg.DatabaseURL) }); err != nil {
		return err
	}
	log.Info("migrations applied")
	hist, err := openHistory(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = hist.Close() }()
	if _, err := retry(ctx, "history store", func() (struct{}, error) { return struct{}{}, hist.Migrate(ctx) }); err != nil {
		return fmt.Errorf("history: %w", err)
	}
	log.Info("history store ready", "driver", cfg.HistoryDriver)
	if _, err := retry(ctx, "redpanda", func() (struct{}, error) { return struct{}{}, bus.EnsureTopics(ctx, cfg.KafkaBrokers) }); err != nil {
		return fmt.Errorf("topics: %w", err)
	}
	log.Info("topics ready")
	return nil
}

func serve(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	hist, err := openHistory(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = hist.Close() }()
	producer, err := bus.NewProducer(cfg.KafkaBrokers, cfg.KafkaAcksAll, log)
	if err != nil {
		return err
	}
	defer producer.Close()
	producer.OnError = func(topic string, _ error) { metrics.BusProduceErrors.WithLabelValues(topic).Inc() }

	errc := make(chan error, 4)
	var gw *gateway.Gateway
	if cfg.HasRole("gateway") {
		gw = gateway.New(ctx, cfg, pool, producer, hist, log)
		go func() { errc <- gw.Run(ctx) }()
	}
	if cfg.HasRole("api") {
		relay := &outbox.Relay{Pool: pool, Producer: producer, Log: log, Published: func(n int) { metrics.OutboxPublished.Add(float64(n)) }}
		go func() { errc <- relay.Run(ctx) }()
		go purgeSessions(ctx, pool, log)
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpserver.New(cfg, pool, hist, gw, log),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Info("listening", "addr", cfg.HTTPAddr, "roles", strings.Join(cfg.Roles, ","), "gateway_id", cfg.GatewayID)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err = <-errc:
		if err != nil {
			log.Error("component failed", "err", err)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	return err
}

func purgeSessions(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := store.PurgeExpiredSessions(ctx, pool); err == nil && n > 0 {
			log.Info("purged expired sessions", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func runIngest(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	hist, err := openHistory(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = hist.Close() }()
	producer, err := bus.NewProducer(cfg.KafkaBrokers, true, log)
	if err != nil {
		return err
	}
	defer producer.Close()
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
		mux.Handle("/metrics", httpserverMetrics())
		_ = (&http.Server{Addr: cfg.HTTPAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}).ListenAndServe()
	}()
	log.Info("ingester started", "group", cfg.IngestGroup, "history", cfg.HistoryDriver)
	return (&ingest.Ingester{Group: cfg.IngestGroup, Store: hist, Brokers: cfg.KafkaBrokers, Publisher: producer, Log: log}).Run(ctx)
}

func importDjango(ctx context.Context, cfg *config.Config, log *slog.Logger, args []string) error {
	fs := newFlagSet("import-django")
	from := fs.String("from", os.Getenv("DJANGO_DATABASE_URL"), "Django Postgres URL (or DJANGO_DATABASE_URL)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return errors.New("--from is required")
	}
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	rep, err := importdjango.Run(ctx, *from, pool, log)
	if err != nil {
		return err
	}
	fmt.Printf("imported: users=%d groups=%d memberships=%d keys=%d devices=%d elements=%d styles=%d permissions=%d\n",
		rep.Users, rep.Groups, rep.Memberships, rep.Keys, rep.Devices, rep.Elements, rep.Styles, rep.Permissions)
	for _, w := range rep.Warnings {
		fmt.Println("warning:", w)
	}
	return nil
}
