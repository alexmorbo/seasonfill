package sonarr

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

	"github.com/alexmorbo/seasonfill/domain"
)

func newNotifTestClient(t *testing.T, mux *http.ServeMux) *Client {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return New("test", srv.URL, "secret", 5*time.Second,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
}

func TestClient_ListDownloadClients_Empty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/downloadclient", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	c := newNotifTestClient(t, mux)
	out, err := c.ListDownloadClients(context.Background())
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestClient_ListDownloadClients_WithQbit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/downloadclient", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id": 1, "name": "qbit-main", "implementation": "QBittorrent",
			 "enable": true,
			 "fields": [
				{"name":"host","value":"10.0.0.5"},
				{"name":"port","value":8080},
				{"name":"username","value":"sonarr"},
				{"name":"tvCategory","value":"tv-sonarr"}
			 ]}
		]`))
	})
	c := newNotifTestClient(t, mux)
	out, err := c.ListDownloadClients(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "qbit-main", out[0].Name)
	assert.Equal(t, "QBittorrent", out[0].Implementation)
	assert.True(t, out[0].Enable)
	assert.Equal(t, "10.0.0.5", out[0].Host)
	assert.Equal(t, 8080, out[0].Port)
	assert.Equal(t, "sonarr", out[0].Username)
	assert.Equal(t, "tv-sonarr", out[0].Category)
}

func TestClient_ListDownloadClients_WithNoQbit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/downloadclient", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id": 5, "name": "tr", "implementation": "Transmission",
			 "enable": true, "fields": []}
		]`))
	})
	c := newNotifTestClient(t, mux)
	out, err := c.ListDownloadClients(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "Transmission", out[0].Implementation)
}

func TestClient_ListNotifications_Empty(t *testing.T) {
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
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id": 9, "name": "seasonfill", "implementation": "Webhook",
			 "onGrab": true, "onDownload": true, "onDownloadFailure": true,
			 "fields": [
				{"name":"url","value":"https://seasonfill.example/api/v1/webhook/sonarr/alpha"},
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
	require.Len(t, out[0].Fields, 3)
	assert.Equal(t, "url", out[0].Fields[0].Name)
}

func TestClient_CreateNotification_Success(t *testing.T) {
	var gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		gotBody = string(buf)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":42,"name":"seasonfill","implementation":"Webhook",
			"onGrab":true,"onDownload":true,"onDownloadFailure":true,
			"fields":[{"name":"url","value":"https://x/y"}]}`))
	})
	c := newNotifTestClient(t, mux)
	n, err := c.CreateNotification(context.Background(), NotificationPayload{
		Name: "seasonfill", URL: "https://x/y", APIKeyHeader: "k",
	})
	require.NoError(t, err)
	assert.Equal(t, 42, n.ID)
	assert.Equal(t, "Webhook", n.Implementation)
	assert.Contains(t, gotBody, `"implementation":"Webhook"`)
	assert.Contains(t, gotBody, `"configContract":"WebhookSettings"`)
	assert.Contains(t, gotBody, `"onGrab":true`)
	assert.Contains(t, gotBody, `X-Api-Key=k`)
}

func TestClient_CreateNotification_409Conflict(t *testing.T) {
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
	assert.False(t, IsAuth(err))
}

func TestClient_CreateNotification_TemplateMirroring(t *testing.T) {
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
	assert.Contains(t, gotBody, `"value":"X-Api-Key=kk"`)
	assert.Contains(t, gotBody, `"name":"ignoreSsl"`)
}

func TestClient_Notification_Unauthorized(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/notification", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	c := newNotifTestClient(t, mux)
	_, err := c.ListNotifications(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInstanceUnauthorized))
}

func TestClient_Notification_NetworkError(t *testing.T) {
	c := New("t", "http://127.0.0.1:1", "k", 200*time.Millisecond,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	_, err := c.ListDownloadClients(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInstanceNetwork))
}
