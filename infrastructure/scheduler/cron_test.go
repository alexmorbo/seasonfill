package scheduler

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestScheduler_NewStop(t *testing.T) {
	s := New("*/5 * * * *", 0, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	assert.NotNil(t, s)
	stopCtx := s.cron.Stop()
	select {
	case <-stopCtx.Done():
	case <-time.After(time.Second):
	}
}
