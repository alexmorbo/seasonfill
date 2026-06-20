package loops

import (
	"bytes"
	"context"
	"log/slog"
	"time"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
)

// nullLogger returns a slog.Logger that discards all output.
// Duplicated from cmd/server/helpers_test.go so the loops package has
// no test-time dependency on package main.
func nullLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), &slog.HandlerOptions{Level: slog.LevelError}))
}

// fakeCooldownRepo is a minimal in-memory implementation of
// ports.CooldownRepository. Duplicated from cmd/server/helpers_test.go
// so cmd/server/loops/sweep_test.go can embed it without importing the
// package-main test surface.
type fakeCooldownRepo struct {
	sweepErr error
	swept    int64
	calls    int
}

func (f *fakeCooldownRepo) Set(_ context.Context, _ cooldown.Cooldown) error { return nil }
func (f *fakeCooldownRepo) Get(_ context.Context, _ cooldown.Scope, _ string) (cooldown.Cooldown, bool, error) {
	return cooldown.Cooldown{}, false, nil
}
func (f *fakeCooldownRepo) FilterActive(_ context.Context, _ cooldown.Scope, _ []string, _ time.Time) ([]cooldown.Cooldown, error) {
	return nil, nil
}
func (f *fakeCooldownRepo) Sweep(_ context.Context, _ time.Time) (int64, error) {
	f.calls++
	return f.swept, f.sweepErr
}

// compile-time check that fakeCooldownRepo satisfies the interface.
var _ ports.CooldownRepository = (*fakeCooldownRepo)(nil)
