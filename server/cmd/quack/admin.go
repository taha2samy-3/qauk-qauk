package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/taha2samy/quackquack/server/internal/config"
	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

func newFlagSet(name string) *flag.FlagSet { return flag.NewFlagSet(name, flag.ContinueOnError) }

func httpserverMetrics() http.Handler { return promhttp.Handler() }

// readPassword takes the flag, then QUACK_PASSWORD, then one line of stdin.
func readPassword(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if p := os.Getenv("QUACK_PASSWORD"); p != "" {
		return p, nil
	}
	fmt.Fprint(os.Stderr, "password: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no password provided")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func adminCmd(ctx context.Context, cfg *config.Config, _ *slog.Logger, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: quack admin create-user|set-password|import-key [flags]")
	}
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	svc := service.New(pool)
	actor := service.Actor{Name: "cli"}

	switch args[0] {
	case "create-user":
		fs := newFlagSet("create-user")
		username := fs.String("username", "", "username")
		email := fs.String("email", "", "email")
		password := fs.String("password", "", "password (or QUACK_PASSWORD, or stdin)")
		isAdmin := fs.Bool("admin", false, "grant admin privileges")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		pw, err := readPassword(*password)
		if err != nil {
			return err
		}
		u, err := svc.CreateUser(ctx, actor, service.UserInput{Username: *username, Email: *email, Password: pw, IsActive: true, IsAdmin: *isAdmin})
		if err != nil {
			return err
		}
		fmt.Printf("created user %d (%s) admin=%v\n", u.ID, u.Username, u.IsAdmin)
	case "set-password":
		fs := newFlagSet("set-password")
		username := fs.String("username", "", "username")
		password := fs.String("password", "", "new password (or QUACK_PASSWORD, or stdin)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		u, err := store.GetUserByUsername(ctx, pool, *username)
		if err != nil {
			return fmt.Errorf("user %q: %w", *username, err)
		}
		pw, err := readPassword(*password)
		if err != nil {
			return err
		}
		if _, err := svc.UpdateUser(ctx, actor, u.ID, service.UserPatch{Password: &pw}); err != nil {
			return err
		}
		fmt.Println("password updated; existing sessions were revoked")
	case "import-key":
		fs := newFlagSet("import-key")
		name := fs.String("name", "", "key name")
		file := fs.String("file", "", "PEM public key file")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		pemBytes, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		k, err := svc.CreateKey(ctx, actor, *name, string(pemBytes), nil)
		if err != nil {
			return err
		}
		fmt.Printf("created key %s (%s, %d bits)\n", k.ID, k.Algorithm, k.KeySize)
	default:
		return fmt.Errorf("unknown admin command %q", args[0])
	}
	return nil
}
