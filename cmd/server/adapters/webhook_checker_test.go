package adapters

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/radarr"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func newCheckerWithSonarr(t *testing.T, instanceName string, handler http.Handler) *WebhookChecker {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client := sonarr.NewWithOptions(domain.InstanceName(instanceName), srv.URL, "test-key", 5*time.Second, nil, nil)

	reg := catalogrest.InstanceRegistry{
		Load: func() map[string]scan.Instance {
			return map[string]scan.Instance{
				instanceName: {
					Config: config.SonarrInstance{Name: instanceName, URL: srv.URL},
					Client: client,
				},
			}
		},
	}
	return NewWebhookChecker(reg)
}

func TestWebhookChecker_UnknownInstance(t *testing.T) {
	t.Parallel()
	reg := catalogrest.InstanceRegistry{
		Load: func() map[string]scan.Instance {
			return map[string]scan.Instance{}
		},
	}
	c := NewWebhookChecker(reg)

	ok, err := c.IsInstalled(context.Background(), "alpha")
	assert.False(t, ok)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownInstance)
}

func TestWebhookChecker_NilLoadIsUnknown(t *testing.T) {
	t.Parallel()
	c := NewWebhookChecker(catalogrest.InstanceRegistry{})

	ok, err := c.IsInstalled(context.Background(), "alpha")
	assert.False(t, ok)
	require.Error(t, err)
}

func TestWebhookChecker_MatchExact(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{
            "id": 7,
            "name": "seasonfill",
            "implementation": "Webhook",
            "onGrab": true,
            "onDownload": true,
            "fields": [
                {"name":"url","value":"https://app.example/api/v1/webhook/sonarr/alpha"},
                {"name":"headers","value":"X-Api-Key=secret"}
            ]
        }]`))
	})
	c := newCheckerWithSonarr(t, "alpha", mux)

	ok, err := c.IsInstalled(context.Background(), "alpha")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestWebhookChecker_MatchPrefixIgnoresPublicURLDrift(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{
            "id": 9,
            "name": "seasonfill (legacy port)",
            "implementation": "Webhook",
            "onGrab": true,
            "fields": [
                {"name":"url","value":"https://old.example:8080/api/v1/webhook/sonarr/alpha"}
            ]
        }]`))
	})
	c := newCheckerWithSonarr(t, "alpha", mux)

	ok, err := c.IsInstalled(context.Background(), "alpha")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestWebhookChecker_NoMatch(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{
            "id": 1,
            "name": "Discord",
            "implementation": "Discord",
            "onGrab": true,
            "fields": [{"name":"url","value":"https://discord.com/api/webhooks/x"}]
        }, {
            "id": 2,
            "name": "Other Webhook",
            "implementation": "Webhook",
            "onGrab": true,
            "fields": [{"name":"url","value":"https://other.example/api/v1/webhook/foo/alpha"}]
        }]`))
	})
	c := newCheckerWithSonarr(t, "alpha", mux)

	ok, err := c.IsInstalled(context.Background(), "alpha")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestWebhookChecker_OnGrabFalseRejectsMatch(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{
            "id": 1,
            "name": "seasonfill (import-only)",
            "implementation": "Webhook",
            "onGrab": false,
            "fields": [{"name":"url","value":"https://app.example/api/v1/webhook/sonarr/alpha"}]
        }]`))
	})
	c := newCheckerWithSonarr(t, "alpha", mux)

	ok, err := c.IsInstalled(context.Background(), "alpha")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestWebhookChecker_SonarrErrorPropagates(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	c := newCheckerWithSonarr(t, "alpha", mux)

	ok, err := c.IsInstalled(context.Background(), "alpha")
	require.Error(t, err)
	assert.False(t, ok)
}

// fakeRadarrLookup satisfies catalogrest.RadarrConfigLookup. Mirror of
// internal/shared/http/handlers/qbit_discover_test.go's fakeRadarrLookup.
type fakeRadarrLookup struct {
	m map[string]scan.RadarrInstance
}

func (f fakeRadarrLookup) Load() map[string]scan.RadarrInstance { return f.m }

func newRadarrClient(t *testing.T, instanceName string, handler http.Handler) *radarr.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return radarr.New(domain.InstanceName(instanceName), srv.URL, "test-key", 5*time.Second,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
}

// TestWebhookChecker_RadarrFallbackMatch: sonarr registry misses "beta"
// entirely; the radarr lookup holds it with a matching OnGrab webhook.
// ADR-0023 F3.
func TestWebhookChecker_RadarrFallbackMatch(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{
            "id": 3,
            "name": "seasonfill",
            "implementation": "Webhook",
            "onGrab": true,
            "fields": [
                {"name":"url","value":"https://app.example/api/v1/webhook/radarr/beta"}
            ]
        }]`))
	})
	client := newRadarrClient(t, "beta", mux)

	reg := catalogrest.InstanceRegistry{
		Load: func() map[string]scan.Instance { return map[string]scan.Instance{} },
	}
	lookup := fakeRadarrLookup{m: map[string]scan.RadarrInstance{
		"beta": {Client: client},
	}}
	c := NewWebhookChecker(reg).WithRadarr(lookup)

	ok, err := c.IsInstalled(context.Background(), "beta")
	require.NoError(t, err)
	assert.True(t, ok)
}

// TestWebhookChecker_RadarrFallbackNoMatch: radarr instance found, but its
// notification list has no matching OnGrab Webhook entry.
func TestWebhookChecker_RadarrFallbackNoMatch(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{
            "id": 4,
            "name": "Other",
            "implementation": "Webhook",
            "onGrab": true,
            "fields": [{"name":"url","value":"https://other.example/api/v1/webhook/radarr/other-instance"}]
        }]`))
	})
	client := newRadarrClient(t, "beta", mux)

	reg := catalogrest.InstanceRegistry{
		Load: func() map[string]scan.Instance { return map[string]scan.Instance{} },
	}
	lookup := fakeRadarrLookup{m: map[string]scan.RadarrInstance{
		"beta": {Client: client},
	}}
	c := NewWebhookChecker(reg).WithRadarr(lookup)

	ok, err := c.IsInstalled(context.Background(), "beta")
	require.NoError(t, err)
	assert.False(t, ok)
}

// TestWebhookChecker_RadarrFallbackUnknownInstance: sonarr misses, radarr
// lookup is injected but does not contain the requested name either →
// ErrUnknownInstance, same sentinel as the sonarr-only miss case.
func TestWebhookChecker_RadarrFallbackUnknownInstance(t *testing.T) {
	t.Parallel()
	reg := catalogrest.InstanceRegistry{
		Load: func() map[string]scan.Instance { return map[string]scan.Instance{} },
	}
	lookup := fakeRadarrLookup{m: map[string]scan.RadarrInstance{}}
	c := NewWebhookChecker(reg).WithRadarr(lookup)

	ok, err := c.IsInstalled(context.Background(), "ghost")
	assert.False(t, ok)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownInstance)
}

// TestWebhookChecker_NilRadarrLookupIsUnknown: no WithRadarr call at all
// (nil field) behaves exactly like pre-F3 — ErrUnknownInstance, no panic.
func TestWebhookChecker_NilRadarrLookupIsUnknown(t *testing.T) {
	t.Parallel()
	reg := catalogrest.InstanceRegistry{
		Load: func() map[string]scan.Instance { return map[string]scan.Instance{} },
	}
	c := NewWebhookChecker(reg)

	ok, err := c.IsInstalled(context.Background(), "ghost")
	assert.False(t, ok)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownInstance)
}

// TestWebhookChecker_RadarrErrorPropagates: radarr instance found, but its
// /api/v3/notification call fails — transport error propagates, mirrors
// TestWebhookChecker_SonarrErrorPropagates.
func TestWebhookChecker_RadarrErrorPropagates(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	client := newRadarrClient(t, "beta", mux)

	reg := catalogrest.InstanceRegistry{
		Load: func() map[string]scan.Instance { return map[string]scan.Instance{} },
	}
	lookup := fakeRadarrLookup{m: map[string]scan.RadarrInstance{
		"beta": {Client: client},
	}}
	c := NewWebhookChecker(reg).WithRadarr(lookup)

	ok, err := c.IsInstalled(context.Background(), "beta")
	require.Error(t, err)
	assert.False(t, ok)
}

// TestWebhookChecker_SonarrHitSkipsRadarr: sonarr registry HAS the instance —
// the radarr lookup must never be consulted even if injected and non-empty
// (dispatch order: sonarr first, unconditionally).
func TestWebhookChecker_SonarrHitSkipsRadarr(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{
            "id": 7,
            "name": "seasonfill",
            "implementation": "Webhook",
            "onGrab": true,
            "fields": [{"name":"url","value":"https://app.example/api/v1/webhook/sonarr/alpha"}]
        }]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	sonarrClient := sonarr.NewWithOptions(domain.InstanceName("alpha"), srv.URL, "test-key", 5*time.Second, nil, nil)

	reg := catalogrest.InstanceRegistry{
		Load: func() map[string]scan.Instance {
			return map[string]scan.Instance{
				"alpha": {Config: config.SonarrInstance{Name: "alpha", URL: srv.URL}, Client: sonarrClient},
			}
		},
	}
	// A radarr lookup that ALSO has "alpha" but would fail loudly if ever
	// dialed (points at a closed port) — proves sonarr-hit short-circuits.
	lookup := fakeRadarrLookup{m: map[string]scan.RadarrInstance{
		"alpha": {Client: radarr.New(domain.InstanceName("alpha"), "http://127.0.0.1:1", "k", time.Second,
			slog.New(slog.NewJSONHandler(io.Discard, nil)))},
	}}
	c := NewWebhookChecker(reg).WithRadarr(lookup)

	ok, err := c.IsInstalled(context.Background(), "alpha")
	require.NoError(t, err)
	assert.True(t, ok)
}

// ports import alive — keeps the compiler honest if a future refactor
// inlines the SonarrClient / RadarrClient interface lookups.
var _ ports.SonarrClient
var _ ports.RadarrClient
