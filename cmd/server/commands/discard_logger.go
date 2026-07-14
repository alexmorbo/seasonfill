package commands

import (
	"io"
	"log/slog"

	"github.com/alexmorbo/seasonfill/internal/logger"
)

func newDiscardLogger() *slog.Logger {
	return logger.New(logger.Config{
		Level:  "error",
		Format: "json",
		Output: io.Discard,
	})
}
