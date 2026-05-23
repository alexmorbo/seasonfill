package http

import (
	"log/slog"
	"os"
	"testing"

	"github.com/alexmorbo/seasonfill/internal/config"
)

// newServerForTest builds a Server with auth enabled and nil deps —
// docs_test.go reads engine.Routes() only; handlers are never invoked.
func newServerForTest(t *testing.T, apiKey string) *Server {
	t.Helper()
	cfg := config.HTTPConfig{
		Bind: "127.0.0.1:0",
		Auth: config.AuthConfig{Enabled: true, APIKey: apiKey},
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	admin := &stubAdminRepo{}
	return NewServer(cfg, nil, nil, nil, nil, nil, nil,
		admin, nil, nil,
		nil, nil, nil,
		nil, nil, nil, nil, logger)
}
