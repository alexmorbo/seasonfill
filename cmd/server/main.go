package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexmorbo/seasonfill/cmd/server/commands"
	"github.com/alexmorbo/seasonfill/cmd/server/loops"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "reset-password" {
		if err := commands.ResetPassword(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "reset-password: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "auth-mode" {
		if err := commands.AuthMode(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "auth-mode: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "backfill-assets" {
		if err := commands.BackfillAssets(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "backfill-assets: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "backfill-base-lang" {
		if err := commands.BackfillBaseLang(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "backfill-base-lang: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "grabs" {
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: seasonfill grabs <reparse> [flags]")
			os.Exit(2)
		}
		switch os.Args[2] {
		case "reparse":
			if err := commands.Reparse(context.Background(), os.Args[3:]); err != nil {
				fmt.Fprintf(os.Stderr, "reparse: %v\n", err)
				os.Exit(1)
			}
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown grabs subcommand: %s\n", os.Args[2])
			os.Exit(2)
		}
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	_, err := runWithContext(ctx, nil)
	return err
}

// runWithContext is a thin shim preserved for the integration-test entry
// (main_test_entry.go → runForTest). Production run() and tests both go
// through this; the bus is exposed for E2E assertions.
func runWithContext(ctx context.Context, onReady func(*runtime.Bus)) (*runtime.Bus, error) {
	srv, err := New(ctx, Options{OnReady: onReady})
	if err != nil {
		return nil, err
	}
	if err := srv.Run(ctx); err != nil {
		return srv.bus, err
	}
	return srv.bus, nil
}

// runCooldownSweep is preserved for callers (and tests) that drive the
// sweep with a fixed cadence. New call sites should construct a
// sweepLoop directly so the cadence can be updated by the reload bus.
func runCooldownSweep(ctx context.Context, repo ports.CooldownRepository, every time.Duration, log *slog.Logger) {
	loops.NewSweepLoop(repo, every, log).Run(ctx)
}
