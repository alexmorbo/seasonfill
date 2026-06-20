package adapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func newCheckerWithSonarr(t *testing.T, instanceName string, handler http.Handler) *WebhookChecker {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client := sonarr.NewWithOptions(domain.InstanceName(instanceName), srv.URL, "test-key", 5*time.Second, nil, nil)

	reg := handlers.InstanceRegistry{
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
	reg := handlers.InstanceRegistry{
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
	c := NewWebhookChecker(handlers.InstanceRegistry{})

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

// ports import alive — keeps the compiler honest if a future refactor
// inlines the SonarrClient interface lookup.
var _ ports.SonarrClient
