package seriesrefresh

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNew_NilLogger_EmitsDomainComposer is the F-4b-6 proof for the
// "composer" domain via the seriesrefresh package (Story 397): when
// New is called with Deps{Logger: nil}, the fallback path wraps
// slog.Default() via sharedports.DomainLogger(..., "composer") so
// every record emitted by the use case carries domain="composer".
//
// We construct the UseCase with Logger=nil to drive the fallback
// path, then emit a deterministic record via the internal uc.log
// field (same-package test access). The captured buffer is asserted
// to contain `"domain":"composer"`.
//
// Stubs are reused from usecase_test.go (same package, already
// satisfying ports.SeriesCacheRepository + SeriesByIDReader +
// enrichment.Dispatcher) — see refreshFakeCache / refreshFakeSeries /
// refreshFakeDispatcher there.
//
// NOT t.Parallel() — mutates slog.SetDefault (process-global state).
func TestNew_NilLogger_EmitsDomainComposer(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	uc, err := New(Deps{
		SeriesCache: &refreshFakeCache{},
		Series:      &refreshFakeSeries{},
		Dispatcher:  &refreshFakeDispatcher{},
		Logger:      nil,
	})
	require.NoError(t, err)
	uc.log.WarnContext(context.Background(), "f4b6_seriesrefresh_proof_emit")

	out := buf.String()
	t.Logf("captured slog output (proof artifact): %s", out)
	assert.True(t, strings.Contains(out, `"domain":"composer"`),
		"expected log record with domain=\"composer\" when Logger=nil; got: %s", out)
}
