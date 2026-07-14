package commands

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/alexmorbo/seasonfill/internal/logger"
)

func newDiscardLogger() *slog.Logger {
	return logger.New(logger.Config{
		Level:  "error",
		Format: "json",
		Output: io.Discard,
	})
}

// AuthMode is a deprecated no-op retained so the `auth-mode` subcommand
// keeps parsing without error. The auth-mode concept was removed: forms
// auth is always enabled and OIDC is additive (gated by OIDC readiness).
func AuthMode(_ []string) error {
	_, _ = fmt.Fprintln(os.Stderr,
		"auth-mode: deprecated no-op — the auth mode concept was removed; "+
			"forms auth is always enabled and OIDC is additive")
	return nil
}
