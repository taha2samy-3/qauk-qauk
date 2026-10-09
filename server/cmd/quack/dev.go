package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/taha2samy/quackquack/server/internal/config"
	"github.com/taha2samy/quackquack/server/internal/devtools"
)

func devCmd(ctx context.Context, cfg *config.Config, log *slog.Logger, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: quack dev seed|hook|demo|simulate|token [flags]")
	}
	if args[0] == "simulate" || args[0] == "token" { // no database needed
		switch args[0] {
		case "token":
			fs := newFlagSet("token")
			file := fs.String("file", "demo-devices.json", "demo device keys written by `dev demo`")
			device := fs.String("device", "", "demo device name or id")
			ttl := fs.Duration("ttl", time.Hour, "token lifetime (at most QUACK_DEVICE_JWT_MAX_LIFETIME)")
			if err := fs.Parse(args[1:]); err != nil {
				return err
			}
			tok, err := devtools.Token(*file, *device, *ttl)
			if err != nil {
				return err
			}
			fmt.Println(tok)
			return nil
		case "simulate":
			fs := newFlagSet("simulate")
			file := fs.String("file", "demo-devices.json", "demo device keys written by `dev demo`")
			wsBase := fs.String("ws-base", "ws://127.0.0.1:8080", "gateway base URL (ws:// or http://)")
			transport := fs.String("transport", "websocket", "websocket, rest, grpc, or mixed (one device per transport)")
			if err := fs.Parse(args[1:]); err != nil {
				return err
			}
			return devtools.Simulate(ctx, *file, *wsBase, *transport, log)
		}
	}
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	switch args[0] {
	case "seed":
		fs := newFlagSet("seed")
		out := fs.String("out", "fixture.json", "fixture output path")
		wsBase := fs.String("ws-base", "ws://127.0.0.1:8080", "WebSocket base URL written to the fixture")
		origin := fs.String("origin", "http://127.0.0.1:8080", "allowed Origin written to the fixture")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		hist, err := openHistory(ctx, cfg)
		if err != nil {
			return err
		}
		defer func() { _ = hist.Close() }()
		return devtools.Seed(ctx, pool, hist, *out, *wsBase, *origin, cfg.SessionTTL)
	case "hook":
		return devtools.Hook(ctx, pool, args[1:])
	case "demo":
		fs := newFlagSet("demo")
		out := fs.String("out", "demo-devices.json", "where to write demo device keys")
		user := fs.String("admin", "admin", "admin username to create")
		pass := fs.String("password", "admin12345", "admin password (dev only)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return devtools.Demo(ctx, pool, *out, *user, *pass)

	}
	return errors.New("unknown dev command")
}
