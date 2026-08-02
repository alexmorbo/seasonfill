package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureHandler records whether Handle was invoked, so a "dropped" record can
// be asserted as "inner never called".
type captureHandler struct {
	records []slog.Record
	attrs   []slog.Attr
}

func (c *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (c *captureHandler) Handle(_ context.Context, r slog.Record) error {
	c.records = append(c.records, r)
	return nil
}

func (c *captureHandler) WithAttrs(a []slog.Attr) slog.Handler {
	c.attrs = append(c.attrs, a...)
	return c
}

func (c *captureHandler) WithGroup(string) slog.Handler { return c }

// TestDomainLevelHandler_Handle_Table exercises the gate directly, asserting
// the inner handler is NOT called on a drop and IS called on a pass — for both
// the captured-domain path and the record-attr fallback path.
func TestDomainLevelHandler_Handle_Table(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		levels         map[string]slog.Level
		defaultLevel   slog.Level
		capturedDomain string
		recDomainAttr  string // "" = no domain attr on the record
		recLevel       slog.Level
		wantWritten    bool
	}{
		{"captured below threshold dropped", map[string]slog.Level{"webhook": slog.LevelWarn}, slog.LevelInfo, "webhook", "", slog.LevelInfo, false},
		{"captured at threshold passes", map[string]slog.Level{"webhook": slog.LevelWarn}, slog.LevelInfo, "webhook", "", slog.LevelWarn, true},
		{"captured above threshold passes", map[string]slog.Level{"webhook": slog.LevelWarn}, slog.LevelInfo, "webhook", "", slog.LevelError, true},
		{"unknown domain uses default drop", map[string]slog.Level{"webhook": slog.LevelDebug}, slog.LevelInfo, "tmdb", "", slog.LevelDebug, false},
		{"unknown domain uses default pass", map[string]slog.Level{"webhook": slog.LevelDebug}, slog.LevelInfo, "tmdb", "", slog.LevelInfo, true},
		{"no domain uses default drop", map[string]slog.Level{}, slog.LevelInfo, "", "", slog.LevelDebug, false},
		{"no domain uses default pass", map[string]slog.Level{}, slog.LevelInfo, "", "", slog.LevelInfo, true},
		{"record-attr domain elevated passes", map[string]slog.Level{"webhook": slog.LevelDebug}, slog.LevelInfo, "", "webhook", slog.LevelDebug, true},
		{"record-attr domain suppressed drops", map[string]slog.Level{"webhook": slog.LevelWarn}, slog.LevelInfo, "", "webhook", slog.LevelInfo, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cap := &captureHandler{}
			h := &DomainLevelHandler{
				inner:        cap,
				levels:       tc.levels,
				defaultLevel: tc.defaultLevel,
				domain:       tc.capturedDomain,
			}
			rec := slog.NewRecord(time.Now(), tc.recLevel, "msg", 0)
			if tc.recDomainAttr != "" {
				rec.AddAttrs(slog.String("domain", tc.recDomainAttr))
			}
			require.NoError(t, h.Handle(context.Background(), rec))
			if tc.wantWritten {
				assert.Len(t, cap.records, 1, "inner must be called on pass")
			} else {
				assert.Empty(t, cap.records, "inner must NOT be called on drop")
			}
		})
	}
}

func TestDomainLevelHandler_WithAttrs_CapturesDomain(t *testing.T) {
	t.Parallel()

	base := &DomainLevelHandler{
		inner:        &captureHandler{},
		levels:       map[string]slog.Level{"enrichment": slog.LevelDebug},
		defaultLevel: slog.LevelInfo,
	}

	withDomain := base.WithAttrs([]slog.Attr{slog.String("domain", "enrichment")}).(*DomainLevelHandler)
	assert.Equal(t, "enrichment", withDomain.domain)

	// Non-domain attrs keep the previously captured domain.
	further := withDomain.WithAttrs([]slog.Attr{slog.String("k", "v")}).(*DomainLevelHandler)
	assert.Equal(t, "enrichment", further.domain)

	// WithGroup preserves domain + config.
	grouped := withDomain.WithGroup("grp").(*DomainLevelHandler)
	assert.Equal(t, "enrichment", grouped.domain)
	assert.Equal(t, slog.LevelDebug, grouped.levels["enrichment"])
	assert.Equal(t, slog.LevelInfo, grouped.defaultLevel)
}

// --- integration via New(): buffer + JSON, asserting real output ---

func newBufLogger(t *testing.T, appLevel string, domains map[string]slog.Level, def *slog.Level) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	lg := New(Config{
		Level:              appLevel,
		Format:             "json",
		Output:             buf,
		DomainLevels:       domains,
		DefaultDomainLevel: def,
	})
	return lg, buf
}

// CRITICAL: domain arriving via .With("domain", …) (the WithAttrs path used by
// sharedports.DomainLogger) is filtered by THAT domain's level, not default.
func TestNew_WithAttrsDomain_FilteredByDomainLevel(t *testing.T) {
	t.Parallel()

	// app INFO, enrichment elevated to DEBUG.
	lg, buf := newBufLogger(t, "info", map[string]slog.Level{"enrichment": slog.LevelDebug}, nil)

	// Same wrap DomainLogger performs internally: base.With(domain=…).
	enrich := lg.With(slog.String("domain", "enrichment"))
	enrich.Debug("enrich-debug-kept")

	// A default (no-domain) logger's DEBUG must be dropped at app INFO.
	lg.Debug("root-debug-dropped")

	out := buf.String()
	assert.Contains(t, out, "enrich-debug-kept", "enrichment DEBUG must pass at its elevated level")
	assert.NotContains(t, out, "root-debug-dropped", "no-domain DEBUG must be gated by default (app INFO)")

	// The kept record still carries the domain attribute.
	var entry map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry))
	assert.Equal(t, "enrichment", entry["domain"])
}

func TestNew_DomainBelowThreshold_Dropped(t *testing.T) {
	t.Parallel()

	lg, buf := newBufLogger(t, "info", map[string]slog.Level{"webhook": slog.LevelWarn}, nil)
	wh := lg.With(slog.String("domain", "webhook"))

	wh.Info("wh-info-dropped")
	wh.Warn("wh-warn-kept")

	out := buf.String()
	assert.NotContains(t, out, "wh-info-dropped")
	assert.Contains(t, out, "wh-warn-kept")
}

func TestNew_UnknownDomain_UsesDefault(t *testing.T) {
	t.Parallel()

	lg, buf := newBufLogger(t, "info", map[string]slog.Level{"webhook": slog.LevelDebug}, nil)
	tmdb := lg.With(slog.String("domain", "tmdb")) // no override → default = app INFO

	tmdb.Debug("tmdb-debug-dropped")
	tmdb.Info("tmdb-info-kept")

	out := buf.String()
	assert.NotContains(t, out, "tmdb-debug-dropped")
	assert.Contains(t, out, "tmdb-info-kept")
}

func TestNew_ElevatedDomainAboveAppLevel(t *testing.T) {
	t.Parallel()

	// app INFO but webhook cranked to DEBUG — DEBUG must reach output.
	lg, buf := newBufLogger(t, "info", map[string]slog.Level{"webhook": slog.LevelDebug}, nil)
	lg.With(slog.String("domain", "webhook")).Debug("wh-debug-elevated")

	assert.Contains(t, buf.String(), "wh-debug-elevated")
}

func TestNew_DefaultEntryLowersDefault(t *testing.T) {
	t.Parallel()

	// No per-domain overrides, but default explicitly lowered to DEBUG.
	d := slog.LevelDebug
	def := &d
	lg, buf := newBufLogger(t, "info", nil, def)

	lg.Debug("root-debug-now-kept")
	assert.Contains(t, buf.String(), "root-debug-now-kept")
}

func TestNew_WithGroupPreservesDomainFilter(t *testing.T) {
	t.Parallel()

	lg, buf := newBufLogger(t, "info", map[string]slog.Level{"webhook": slog.LevelWarn}, nil)
	wh := lg.WithGroup("grp").With(slog.String("domain", "webhook"))

	wh.Info("grp-info-dropped")
	wh.Warn("grp-warn-kept")

	out := buf.String()
	assert.NotContains(t, out, "grp-info-dropped")
	assert.Contains(t, out, "grp-warn-kept")
}

// Unset env (nil map, nil default) must be a strict no-op: identical to the
// plain app-level behavior — no domain wrapping, DEBUG dropped at INFO.
func TestNew_UnsetDomainLevels_NoOp(t *testing.T) {
	t.Parallel()

	lg, buf := newBufLogger(t, "info", nil, nil)

	lg.Debug("noop-debug-dropped")
	lg.Info("noop-info-kept")
	lg.With(slog.String("domain", "webhook")).Debug("noop-wh-debug-dropped")

	out := buf.String()
	assert.NotContains(t, out, "noop-debug-dropped")
	assert.Contains(t, out, "noop-info-kept")
	assert.NotContains(t, out, "noop-wh-debug-dropped")
}
