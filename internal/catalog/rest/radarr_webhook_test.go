package rest

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// radarrWebhookFixture mirrors webhookFixture but wires the RADARR handler and
// the radarr-aware registry edge.NewServer builds (ADR-0023 A1): a
// map[string]scan.Instance whose KEYS are the radarr instance names and whose
// values are deliberately zero — the handler guard only tests key presence.
type radarrWebhookFixture struct {
	mu       sync.Mutex
	inserted []ports.WebhookInboxRow
	router   *gin.Engine
}

func newRadarrWebhookFixture(t *testing.T, known []string) *radarrWebhookFixture {
	t.Helper()

	f := &radarrWebhookFixture{}
	inbox := &ports.WebhookInboxRepositoryMock{
		InsertFunc: func(_ context.Context, row ports.WebhookInboxRow) error {
			f.mu.Lock()
			f.inserted = append(f.inserted, row)
			f.mu.Unlock()
			return nil
		},
	}
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))

	reg := InstanceRegistry{}
	if known != nil {
		state := map[string]scan.Instance{}
		for _, n := range known {
			state[n] = scan.Instance{} // zero value — key presence is the contract
		}
		reg.Load = func() map[string]scan.Instance { return state }
	}
	h := NewRadarrWebhookHandler(inbox, passthroughTxr{}, nil, reg, lg)

	r := gin.New()
	api := r.Group("/api/v1")
	rwh := api.Group("/webhook/radarr/:instance_name")
	rwh.POST("", h.Handle)
	f.router = r
	return f
}

func (f *radarrWebhookFixture) post(t *testing.T, instance string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/webhook/radarr/"+instance, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func (f *radarrWebhookFixture) rows() []ports.WebhookInboxRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ports.WebhookInboxRow, len(f.inserted))
	copy(out, f.inserted)
	return out
}

func radarrDownloadPayload() []byte {
	return []byte(`{"eventType":"Download","instanceName":"ignored","downloadId":"ABC123","movie":{"id":12,"title":"Dune","tmdbId":438631,"year":2021},"movieFile":{"id":9,"quality":"WEBDL-2160p","size":1024}}`)
}

func radarrTestPayload() []byte {
	return []byte(`{"eventType":"Test","instanceName":"ignored","movie":{"id":1,"title":"Test Title","tmdbId":1}}`)
}

// A radarr instance present in the radarr-aware registry is accepted and
// enqueued — the case that 404'd before A1 because the handler was wired with
// the sonarr registry.
func TestRadarrWebhookHandler_KnownInstanceEnqueues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newRadarrWebhookFixture(t, []string{"movies"})

	w := f.post(t, "movies", radarrDownloadPayload())

	require.Equal(t, http.StatusOK, w.Code)
	rows := f.rows()
	require.Len(t, rows, 1)
	assert.Equal(t, "movies", rows[0].InstanceName)
	assert.Equal(t, "Download", rows[0].EventType)
	assert.Equal(t, ports.WebhookInboxStatusPending, rows[0].Status)
}

// Radarr's install-time Test POST is classified Unsupported → 200 without an
// inbox row. This is the exact live symptom A1 fixes (installed:false).
func TestRadarrWebhookHandler_KnownInstanceTestEventAccepted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newRadarrWebhookFixture(t, []string{"movies"})

	w := f.post(t, "movies", radarrTestPayload())

	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, f.rows())
}

// An instance absent from the radarr registry still 404s — the guard must stay
// a guard, not become accept-any once Load is wired.
func TestRadarrWebhookHandler_UnknownInstance404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newRadarrWebhookFixture(t, []string{"movies"})

	w := f.post(t, "nosuch", radarrDownloadPayload())

	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Empty(t, f.rows())
}

// Load == nil (radarrConfigLookup unwired in minimal/test wirings) keeps the
// documented accept-any mode.
func TestRadarrWebhookHandler_NilRegistryAcceptsAny(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newRadarrWebhookFixture(t, nil)

	w := f.post(t, "anything", radarrDownloadPayload())

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, f.rows(), 1)
}
