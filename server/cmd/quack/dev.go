package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/taha2samy/quackquack/server/internal/config"
	"github.com/taha2samy/quackquack/server/internal/devtools"
)

func devCmd(ctx context.Context, cfg *config.Config, log *slog.Logger, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: quack dev seed|hook|demo|simulate [flags]")
	}
	if args[0] == "simulate" { // talks only to the gateway, no database needed
		switch args[0] {
		case "simulate":
			fs := newFlagSet("simulate")
			file := fs.String("file", "demo-devices.json", "demo device keys written by `dev demo`")
			wsBase := fs.String("ws-base", "ws://127.0.0.1:8080", "gateway WebSocket base URL")
			if err := fs.Parse(args[1:]); err != nil {
				return err
			}
			return devtools.Simulate(ctx, *file, *wsBase, log)
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
		return devtools.Seed(ctx, pool, *out, *wsBase, *origin, cfg.SessionTTL)
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
