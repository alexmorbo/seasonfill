//go:build integration

package main

import (
	"context"

	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// runForTest is the integration-test entry point. It runs the full server
// lifecycle against ctx, blocking until ctx is cancelled. onReady is
// called with the live bus after all subscribers are registered.
func runForTest(ctx context.Context, onReady func(*runtime.Bus)) error {
	_, err := runWithContext(ctx, onReady)
	return err
}
