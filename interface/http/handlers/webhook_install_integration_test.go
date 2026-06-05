package handlers

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// TestWebhookInstall_NoopThenCreate_EndToEnd exercises the full
// install flow against a stateful httptest Sonarr stub. Two POSTs:
// first the list is empty → 201 create + Sonarr stores the new
// webhook. Second POST sees the stored webhook → 200 no-op.
func TestWebhookInstall_NoopThenCreate_EndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var stored atomic.Value // []byte JSON of the notification list
	stored.Store([]byte(`[]`))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/notification" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write(stored.Load().([]byte))
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			// echo back with an id stamped in
			stamped := []byte(`[{"id":11,"implementation":"Webhook","fields":` +
				extractFields(string(body)) + `}]`)
			stored.Store(stamped)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":11,"implementation":"Webhook"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	client := sonarr.New("alpha", srv.URL, "k", 2*time.Second,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	reg := InstanceRegistry{Load: func() map[string]scan.Instance {
		return map[string]scan.Instance{
			"alpha": {Config: config.SonarrInstance{Name: "alpha"}, Client: client},
		}
	}}
	h := NewWebhookInstallHandler(reg, "api-key-XYZ",
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r := gin.New()
	r.POST("/api/v1/instances/:name/webhook/install", h.Install)

	// First call: list empty → POST → 201.
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/instances/alpha/webhook/install", nil)
	req1.Host = "seasonfill.example"
	req1.Header.Set("X-Forwarded-Proto", "https")
	r.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusCreated, w1.Code)
	assert.Contains(t, w1.Body.String(), `"created":true`)

	// Second call: list now contains the stored webhook → 200 no-op.
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/instances/alpha/webhook/install", nil)
	req2.Host = "seasonfill.example"
	req2.Header.Set("X-Forwarded-Proto", "https")
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	assert.Contains(t, w2.Body.String(), `"created":false`)
}

// extractFields pulls the "fields" array literal out of a POST body
// so the stateful stub can echo it back on subsequent GETs. Naive
// substring extraction is good enough for the test harness.
func extractFields(body string) string {
	const marker = `"fields":`
	idx := -1
	for i := 0; i+len(marker) <= len(body); i++ {
		if body[i:i+len(marker)] == marker {
			idx = i + len(marker)
			break
		}
	}
	if idx < 0 {
		return `[]`
	}
	// scan from idx to end, capture balanced [..]
	depth := 0
	start := -1
	for i := idx; i < len(body); i++ {
		ch := body[i]
		//nolint:staticcheck
		if ch == '[' {
			if depth == 0 {
				start = i
			}
			depth++
		} else if ch == ']' {
			depth--
			if depth == 0 {
				return body[start : i+1]
			}
		}
	}
	return `[]`
}
