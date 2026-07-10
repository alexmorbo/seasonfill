package ports_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/ports"
)

func newJSONLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewJSONHandler(buf, nil)), buf
}

func decodeOne(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var entry map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry))
	return entry
}

func TestDomainLogger_AddsDomainAttr(t *testing.T) {
	t.Parallel()
	base, buf := newJSONLogger()
	ports.DomainLogger(base, "http").Info("hi")
	entry := decodeOne(t, buf)
	assert.Equal(t, "http", entry["domain"])
	assert.Equal(t, "hi", entry["msg"])
}

func TestDomainLogger_PreservesBaseAttrs(t *testing.T) {
	t.Parallel()
	base, buf := newJSONLogger()
	withK := base.With(slog.String("k", "v"))
	ports.DomainLogger(withK, "scan").Info("msg")
	entry := decodeOne(t, buf)
	assert.Equal(t, "v", entry["k"])
	assert.Equal(t, "scan", entry["domain"])
}

func TestDomainLogger_PanicsOnUnknownDomain(t *testing.T) {
	t.Parallel()
	base, _ := newJSONLogger()
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic on unknown domain")
		msg, _ := r.(string)
		assert.Contains(t, msg, "unknown domain")
		assert.Contains(t, msg, "frobnicate")
	}()
	ports.DomainLogger(base, "frobnicate")
}

func TestDomainLogger_PanicsOnNilBase(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic on nil base logger")
		msg, _ := r.(string)
		assert.Contains(t, msg, "must not be nil")
	}()
	ports.DomainLogger(nil, "http")
}

func TestDomainLogger_AllAllowedDomains(t *testing.T) {
	t.Parallel()
	for domain := range ports.AllowedDomains {
		t.Run(domain, func(t *testing.T) {
			t.Parallel()
			base, buf := newJSONLogger()
			assert.NotPanics(t, func() {
				ports.DomainLogger(base, domain).Info("hi")
			})
			entry := decodeOne(t, buf)
			assert.Equal(t, domain, entry["domain"])
		})
	}
}

func TestAllowedDomains_MatchesPRD(t *testing.T) {
	t.Parallel()
	// Hard-coded PRD §6.5 closed list. If the PRD adds a domain, BOTH this
	// list AND ports.AllowedDomains must change in lockstep — this test is
	// the lockstep guard.
	want := []string{
		"http", "webhook", "scan", "tmdb", "omdb",
		"sonarr", "radarr", "qbit", "queue", "composer",
		"enrichment", "watchdog", "discovery", "auth", "admin",
		"boot", "gc", "shutdown", "catalog_counts",
		"library_poster_coverage", "enrichment_coverage",
	}
	assert.Len(t, ports.AllowedDomains, len(want), "AllowedDomains count drifted from PRD §6.5")

	got := make(map[string]struct{}, len(ports.AllowedDomains))
	for k := range ports.AllowedDomains {
		got[k] = struct{}{}
	}
	for _, d := range want {
		_, ok := got[d]
		assert.True(t, ok, "PRD domain %q missing from AllowedDomains (have: %s)",
			d, strings.Join(keysOf(got), ","))
	}
}

func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
