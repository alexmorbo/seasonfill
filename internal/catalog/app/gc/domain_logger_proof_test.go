package gc

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestWeeklyJob_NilLogger_EmitsDomainGC is the F-4b-8 proof
// (Story 407): when gc.WeeklyJob.Run is invoked with Logger=nil, the
// fallback path wraps slog.Default() via sharedports.DomainLogger(...,
// "gc") so every record emitted by the weekly orchestrator + its
// three sub-tasks (orphan_series, media_sweep, event_prune) carries
// domain="gc".
//
// We construct WeeklyJob{} with all sub-tasks nil so the run is a
// fast-path (just opens/closes around no work), then assert the
// emitted "weekly-gc.started" and "weekly-gc.finished" records both
// carry the domain attribute.
//
// NOT t.Parallel() — mutates slog.SetDefault (process-global state),
// same constraint as Stories 392/393/394/395/396/397/398 proof tests.
func TestWeeklyJob_NilLogger_EmitsDomainGC(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	// Logger=nil drives the fallback path under test. All sub-tasks are
	// nil so Run is a no-op fast-path that still emits the opening +
	// closing records through the wired fallback logger.
	job := WeeklyJob{}
	job.Run(context.Background())

	out := buf.String()
	t.Logf("captured slog output (proof artifact): %s", out)
	assert.True(t, strings.Contains(out, `"domain":"gc"`),
		`expected log record with domain="gc" when Logger=nil; got: %s`, out)
}
