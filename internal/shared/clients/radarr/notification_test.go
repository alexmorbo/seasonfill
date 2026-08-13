package radarr

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

func newNotifTestClient(t *testing.T, mux *http.ServeMux) *Client {
	t.Helper()
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.SetKeepAlivesEnabled(false)
	srv.Start()
	t.Cleanup(srv.Close)
	return New("test", srv.URL, "secret", 5*time.Second,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
}

func TestClient_ListNotifications_Empty(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	c := newNotifTestClient(t, mux)
	out, err := c.ListNotifications(context.Background())
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestClient_ListNotifications_WithWebhook(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id": 9, "name": "seasonfill", "implementation": "Webhook",
			 "onGrab": true, "onDownload": true, "onMovieAdded": true,
			 "fields": [
				{"name":"url","value":"https://seasonfill.example/api/v1/webhook/radarr/alpha"},
				{"name":"method","value":1},
				{"name":"headers","value":"X-Api-Key=abc"}
			 ]}
		]`))
	})
	c := newNotifTestClient(t, mux)
	out, err := c.ListNotifications(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "Webhook", out[0].Implementation)
	assert.True(t, out[0].OnGrab)
	assert.True(t, out[0].OnMovieAdded)
	require.Len(t, out[0].Fields, 3)
	assert.Equal(t, "url", out[0].Fields[0].Name)
}

func TestClient_CreateNotification_Success(t *testing.T) {
	t.Parallel()
	var gotBody string
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		buf, _ := io.ReadAll(r.Body)
		gotBody = string(buf)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":42,"name":"seasonfill","implementation":"Webhook",
			"onGrab":true,"onDownload":true,"onMovieAdded":true,
			"fields":[{"name":"url","value":"https://x/y"}]}`))
	})
	c := newNotifTestClient(t, mux)
	n, err := c.CreateNotification(context.Background(), NotificationPayload{
		Name: "seasonfill", URL: "https://x/y", APIKeyHeader: "k",
	})
	require.NoError(t, err)
	assert.Equal(t, "/api/v3/notification", gotPath)
	assert.Equal(t, 42, n.ID)
	assert.Equal(t, "Webhook", n.Implementation)
	assert.Contains(t, gotBody, `"implementation":"Webhook"`)
	assert.Contains(t, gotBody, `"configContract":"WebhookSettings"`)
	// Movie trigger flags all requested.
	assert.Contains(t, gotBody, `"onGrab":true`)
	assert.Contains(t, gotBody, `"onDownload":true`)
	assert.Contains(t, gotBody, `"onMovieAdded":true`)
	assert.Contains(t, gotBody, `"onMovieDelete":true`)
	assert.Contains(t, gotBody, `"onMovieFileDelete":true`)
	assert.Contains(t, gotBody, `"onManualInteractionRequired":true`)
	assert.Contains(t, gotBody, `"onHealthIssue":true`)
	assert.Contains(t, gotBody, `"key":"X-Api-Key"`)
	assert.Contains(t, gotBody, `"value":"k"`)
	assert.Contains(t, gotBody, `"value":"https://x/y"`)
	// sonarr-only series/episode flags must NOT be requested.
	assert.NotContains(t, gotBody, `"onSeriesAdd"`)
	assert.NotContains(t, gotBody, `"onSeriesDelete"`)
	assert.NotContains(t, gotBody, `"onEpisodeFileDelete"`)
	// radarr flags outside the requested set must NOT be requested.
	assert.NotContains(t, gotBody, `"onRename"`)
	assert.NotContains(t, gotBody, `"onUpgrade"`)
	assert.NotContains(t, gotBody, `"onApplicationUpdate"`)
}

func TestClient_CreateNotification_409Conflict(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"duplicate name"}`))
	})
	c := newNotifTestClient(t, mux)
	_, err := c.CreateNotification(context.Background(), NotificationPayload{
		Name: "seasonfill", URL: "https://x", APIKeyHeader: "k",
	})
	require.Error(t, err)
	var se *StatusError
	require.True(t, errors.As(err, &se))
	assert.Equal(t, http.StatusConflict, se.Status)
}

func TestClient_CreateNotification_TemplateMirroring(t *testing.T) {
	t.Parallel()
	var gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		gotBody = string(buf)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1,"implementation":"Webhook"}`))
	})
	c := newNotifTestClient(t, mux)
	_, err := c.CreateNotification(context.Background(), NotificationPayload{
		Name: "seasonfill", URL: "https://y", APIKeyHeader: "kk",
		TemplateFields: []NotificationField{
			{Name: "url", Value: "stale"},
			{Name: "method", Value: 1},
			{Name: "ignoreSsl", Value: false},
			{Name: "headers", Value: "stale=stale"},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, gotBody, `"value":"https://y"`)
	assert.Contains(t, gotBody, `"key":"X-Api-Key"`)
	assert.Contains(t, gotBody, `"value":"kk"`)
	assert.Contains(t, gotBody, `"name":"ignoreSsl"`)
}

func TestClient_Notification_Unauthorized(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	c := newNotifTestClient(t, mux)
	_, err := c.ListNotifications(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, sharedErrors.ErrInstanceUnauthorized))
}

func TestClient_Notification_NetworkError(t *testing.T) {
	t.Parallel()
	c := New("t", "http://127.0.0.1:1", "k", 200*time.Millisecond,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	_, err := c.ListNotifications(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, sharedErrors.ErrInstanceNetwork))
}

func TestClient_CreateNotification_FallbackOnUnknownMovieTrigger(t *testing.T) {
	t.Parallel()
	var bodies []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(buf))
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":[{"propertyName":"OnMovieAdded","errorMessage":"is not a recognized trigger"}]}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":7,"implementation":"Webhook"}`))
	})
	c := newNotifTestClient(t, mux)
	n, err := c.CreateNotification(context.Background(), NotificationPayload{
		Name: "seasonfill", URL: "https://x/y", APIKeyHeader: "k",
	})
	require.NoError(t, err, "first 400 with OnMovieAdded in body must be retried without the new flags")
	assert.Equal(t, 7, n.ID)
	require.Len(t, bodies, 2, "exactly two POSTs: original + fallback")
	assert.Contains(t, bodies[0], `"onMovieAdded":true`, "first attempt includes the movie flags")
	assert.Contains(t, bodies[0], `"onMovieFileDelete":true`)
	assert.Contains(t, bodies[0], `"onHealthIssue":true`)
	assert.NotContains(t, bodies[1], `"onMovieAdded":true`, "fallback omits onMovieAdded (omitempty)")
	assert.NotContains(t, bodies[1], `"onMovieDelete":true`, "fallback omits onMovieDelete (omitempty)")
	assert.NotContains(t, bodies[1], `"onMovieFileDelete":true`, "fallback omits onMovieFileDelete (omitempty)")
	assert.NotContains(t, bodies[1], `"onManualInteractionRequired":true`, "fallback omits onManualInteractionRequired (omitempty)")
	assert.NotContains(t, bodies[1], `"onHealthIssue":true`, "fallback omits onHealthIssue (omitempty)")
	assert.Contains(t, bodies[1], `"onGrab":true`, "fallback keeps the known-good core")
	assert.Contains(t, bodies[1], `"onDownload":true`)
}

func TestClient_CreateNotification_400WithoutMovieTrigger_NoRetry(t *testing.T) {
	t.Parallel()
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"duplicate URL"}`))
	})
	c := newNotifTestClient(t, mux)
	_, err := c.CreateNotification(context.Background(), NotificationPayload{
		Name: "seasonfill", URL: "https://x/y", APIKeyHeader: "k",
	})
	require.Error(t, err, "400 without a movie trigger substring must propagate")
	assert.Equal(t, 1, calls, "no retry — only the targeted trigger case falls back")
	var se *StatusError
	require.True(t, errors.As(err, &se))
	assert.Equal(t, http.StatusBadRequest, se.Status)
}

func TestClient_UpdateNotification_PUTsExpectedPath(t *testing.T) {
	t.Parallel()
	var gotBody string
	var gotMethod string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification/42", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		buf, _ := io.ReadAll(r.Body)
		gotBody = string(buf)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":42,"name":"seasonfill","implementation":"Webhook",
			"onGrab":true,"onDownload":true,
			"fields":[{"name":"url","value":"https://new.example/api/v1/webhook/radarr/alpha"}]}`))
	})
	c := newNotifTestClient(t, mux)
	existing := Notification{
		ID: 42, Name: "seasonfill", Implementation: "Webhook",
		OnGrab: true, OnDownload: true,
		Fields: []NotificationField{{Name: "url", Value: "https://old.example/api/v1/webhook/radarr/alpha"}},
	}
	n, err := c.UpdateNotification(context.Background(), existing, NotificationPayload{
		Name: "seasonfill", URL: "https://new.example/api/v1/webhook/radarr/alpha", APIKeyHeader: "newkey",
	})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPut, gotMethod)
	assert.Equal(t, 42, n.ID)
	assert.Contains(t, gotBody, `"id":42`)
	assert.Contains(t, gotBody, `"https://new.example/api/v1/webhook/radarr/alpha`)
	assert.Contains(t, gotBody, `"newkey"`)
	// Update reconciles the FULL desired trigger set.
	assert.Contains(t, gotBody, `"onGrab":true`)
	assert.Contains(t, gotBody, `"onDownload":true`)
	assert.Contains(t, gotBody, `"onMovieAdded":true`)
	assert.Contains(t, gotBody, `"onMovieDelete":true`)
	assert.Contains(t, gotBody, `"onMovieFileDelete":true`)
	assert.Contains(t, gotBody, `"onManualInteractionRequired":true`)
	assert.Contains(t, gotBody, `"onHealthIssue":true`)
}

func TestClient_UpdateNotification_FallbackOnUnknownTrigger(t *testing.T) {
	t.Parallel()
	var bodies []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification/42", func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(buf))
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":[{"propertyName":"OnMovieFileDelete","errorMessage":"is not a recognized trigger"}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":42,"implementation":"Webhook","onGrab":true,"onDownload":true}`))
	})
	c := newNotifTestClient(t, mux)
	existing := Notification{
		ID: 42, Name: "seasonfill", Implementation: "Webhook",
		Fields: []NotificationField{{Name: "url", Value: "https://old.example/api/v1/webhook/radarr/alpha"}},
	}
	n, err := c.UpdateNotification(context.Background(), existing, NotificationPayload{
		Name: "seasonfill", URL: "https://new.example/api/v1/webhook/radarr/alpha", APIKeyHeader: "k",
	})
	require.NoError(t, err, "400 naming a newer trigger must be retried without it")
	assert.Equal(t, 42, n.ID)
	require.Len(t, bodies, 2, "exactly two PUTs: original + fallback")
	assert.Contains(t, bodies[0], `"onMovieFileDelete":true`, "first attempt includes the movie triggers")
	assert.NotContains(t, bodies[1], `"onMovieFileDelete":true`, "fallback drops onMovieFileDelete")
	assert.NotContains(t, bodies[1], `"onMovieAdded":true`, "fallback drops onMovieAdded")
	assert.NotContains(t, bodies[1], `"onMovieDelete":true`, "fallback drops onMovieDelete")
	assert.NotContains(t, bodies[1], `"onManualInteractionRequired":true`, "fallback drops onManualInteractionRequired")
	assert.NotContains(t, bodies[1], `"onHealthIssue":true`, "fallback drops onHealthIssue")
	assert.Contains(t, bodies[1], `"onGrab":true`, "fallback keeps the known-good core")
	assert.Contains(t, bodies[1], `"onDownload":true`)
}

func TestClient_UpdateNotification_RejectsZeroID(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	c := newNotifTestClient(t, mux)
	_, err := c.UpdateNotification(context.Background(), Notification{ID: 0}, NotificationPayload{
		Name: "test", URL: "https://x", APIKeyHeader: "k",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing id")
}

func TestClient_DeleteNotification_DELETEsExpectedPath(t *testing.T) {
	t.Parallel()
	var gotMethod string
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification/13", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	c := newNotifTestClient(t, mux)
	err := c.DeleteNotification(context.Background(), 13)
	require.NoError(t, err)
	assert.Equal(t, http.MethodDelete, gotMethod)
	assert.Equal(t, "/api/v3/notification/13", gotPath)
}

func TestClient_DeleteNotification_RejectsZeroID(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	c := newNotifTestClient(t, mux)
	err := c.DeleteNotification(context.Background(), 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing id")
}

func TestWebhookFieldURL_Present(t *testing.T) {
	t.Parallel()
	fields := []NotificationField{
		{Name: "url", Value: "https://example.com/api/v1/webhook/radarr/alpha"},
		{Name: "method", Value: 1},
	}
	got := WebhookFieldURL(fields)
	assert.Equal(t, "https://example.com/api/v1/webhook/radarr/alpha", got)
}

func TestWebhookFieldURL_NonString(t *testing.T) {
	t.Parallel()
	fields := []NotificationField{
		{Name: "url", Value: 42},
		{Name: "method", Value: 1},
	}
	got := WebhookFieldURL(fields)
	assert.Equal(t, "", got)
}

func TestWebhookFieldURL_Absent(t *testing.T) {
	t.Parallel()
	fields := []NotificationField{
		{Name: "method", Value: 1},
		{Name: "headers", Value: "X-Api-Key=test"},
	}
	got := WebhookFieldURL(fields)
	assert.Equal(t, "", got)
}

func TestDesiredTriggers_AllOn(t *testing.T) {
	t.Parallel()
	assert.Equal(t, TriggerSet{
		OnGrab:                      true,
		OnDownload:                  true,
		OnMovieAdded:                true,
		OnMovieDelete:               true,
		OnMovieFileDelete:           true,
		OnManualInteractionRequired: true,
		OnHealthIssue:               true,
	}, DesiredTriggers())
}

func TestNotificationFromDTO_SurfacesMovieTriggers(t *testing.T) {
	t.Parallel()
	n := notificationFromDTO(notificationDTO{OnMovieAdded: true})
	assert.True(t, n.OnMovieAdded)
	assert.False(t, n.OnMovieDelete)
	assert.False(t, n.OnHealthIssue)
}

func TestClient_TestNotification_Success(t *testing.T) {
	t.Parallel()
	var gotPath, gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification/test", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		buf, _ := io.ReadAll(r.Body)
		gotBody = string(buf)
		w.WriteHeader(http.StatusOK)
	})
	c := newNotifTestClient(t, mux)
	err := c.TestNotification(context.Background(), NotificationPayload{
		Name: "seasonfill", URL: "https://x/y", APIKeyHeader: "k",
	})
	require.NoError(t, err)
	assert.Equal(t, "/api/v3/notification/test", gotPath)
	assert.Contains(t, gotBody, `"implementation":"Webhook"`)
	assert.Contains(t, gotBody, `"configContract":"WebhookSettings"`)
	assert.Contains(t, gotBody, `"onGrab":true`)
	assert.Contains(t, gotBody, `"onDownload":true`)
	assert.Contains(t, gotBody, `"onMovieAdded":true`)
	assert.Contains(t, gotBody, `"value":"https://x/y"`)
	assert.Contains(t, gotBody, `"key":"X-Api-Key"`)
	assert.Contains(t, gotBody, `"value":"k"`)
}

func TestClient_TestNotification_Non2xxReturnsError(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification/test", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"Unable to connect to seasonfill"}`))
	})
	c := newNotifTestClient(t, mux)
	err := c.TestNotification(context.Background(), NotificationPayload{
		Name: "seasonfill", URL: "https://x/y", APIKeyHeader: "k",
	})
	require.Error(t, err, "a non-2xx test result must surface so Installed is NOT set")
	var se *StatusError
	require.True(t, errors.As(err, &se))
	assert.Equal(t, http.StatusBadRequest, se.Status)
}

func TestClient_TestNotification_Unauthorized(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification/test", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	c := newNotifTestClient(t, mux)
	err := c.TestNotification(context.Background(), NotificationPayload{
		Name: "seasonfill", URL: "https://x/y", APIKeyHeader: "k",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, sharedErrors.ErrInstanceUnauthorized))
}

func TestClient_TestNotification_FallbackOnUnknownTrigger(t *testing.T) {
	t.Parallel()
	var bodies []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification/test", func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(buf))
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":[{"propertyName":"OnMovieAdded","errorMessage":"is not a recognized trigger"}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	c := newNotifTestClient(t, mux)
	err := c.TestNotification(context.Background(), NotificationPayload{
		Name: "seasonfill", URL: "https://x/y", APIKeyHeader: "k",
	})
	require.NoError(t, err, "an old Radarr rejecting a newer trigger must retry without it, mirroring Create/Update")
	require.Len(t, bodies, 2, "exactly two POSTs: original + fallback")
	assert.Contains(t, bodies[0], `"onMovieAdded":true`, "first attempt includes the newer trigger")
	assert.NotContains(t, bodies[1], `"onMovieAdded":true`, "fallback drops onMovieAdded (omitempty)")
	assert.Contains(t, bodies[1], `"onGrab":true`, "fallback keeps the known-good core")
	assert.Contains(t, bodies[1], `"onDownload":true`)
}
