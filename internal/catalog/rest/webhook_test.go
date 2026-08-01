package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/config"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// passthroughTxr is a unit-test Transactor: it runs fn with the same ctx,
// no real transaction. The durable atomicity is exercised by the E2
// repository integration tests (testcontainers Postgres), not here.
type passthroughTxr struct{}

func (passthroughTxr) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

type webhookFixture struct {
	inbox     *ports.WebhookInboxRepositoryMock
	mu        sync.Mutex
	inserted  []ports.WebhookInboxRow
	pokeCount atomic.Int32
	insertErr error // when non-nil, InsertFunc returns it (and records nothing)
	router    *gin.Engine
}

func newWebhookFixture(t *testing.T, known map[string]struct{}) *webhookFixture {
	t.Helper()

	f := &webhookFixture{}
	f.inbox = &ports.WebhookInboxRepositoryMock{
		InsertFunc: func(_ context.Context, row ports.WebhookInboxRow) error {
			if f.insertErr != nil {
				return f.insertErr
			}
			f.mu.Lock()
			f.inserted = append(f.inserted, row)
			f.mu.Unlock()
			return nil
		},
	}
	poke := func() { f.pokeCount.Add(1) }
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))

	reg := InstanceRegistry{}
	if known != nil {
		state := map[string]scan.Instance{}
		for n := range known {
			state[n] = scan.Instance{Config: config.SonarrInstance{Name: n}}
		}
		reg.Load = func() map[string]scan.Instance { return state }
	}
	h := NewWebhookHandler(f.inbox, passthroughTxr{}, poke, reg, lg)

	r := gin.New()
	api := r.Group("/api/v1")
	wh := api.Group("/webhook/sonarr/:instance_name")
	wh.POST("", h.Handle)
	f.router = r
	return f
}

func (f *webhookFixture) post(t *testing.T, instance string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/webhook/sonarr/"+instance, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func (f *webhookFixture) rows() []ports.WebhookInboxRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ports.WebhookInboxRow, len(f.inserted))
	copy(out, f.inserted)
	return out
}

func importedPayload() []byte {
	return []byte(`{"eventType":"Download","instanceName":"ignored","downloadId":"ABC123","series":{"id":122,"title":"Hijack"},"episodes":[{"id":1,"seasonNumber":2,"episodeNumber":4}],"episodeFile":{"id":9876,"quality":"WEBDL-2160p"}}`)
}

func importFailedPayload() []byte {
	return []byte(`{"eventType":"ManualInteractionRequired","instanceName":"ignored","downloadId":"ABC123","series":{"id":122},"episodes":[{"id":1,"seasonNumber":2}],"downloadStatusMessages":[{"title":"Audio","messages":["bad"]}]}`)
}

func unsupportedPayload() []byte {
	return []byte(`{"eventType":"Rename","instanceName":"ignored","series":{"id":122}}`)
}

func grabPayloadWithHash() []byte {
	return []byte(`{"eventType":"Grab","instanceName":"ignored","downloadId":"0123456789abcdef0123456789abcdef01234567","release":{"releaseTitle":"Hijack.S02.PACK","indexer":"RT"},"series":{"id":122,"title":"Hijack"},"episodes":[{"id":1,"seasonNumber":2,"episodeNumber":4}]}`)
}

func grabPayloadShortHash() []byte {
	return []byte(`{"eventType":"Grab","instanceName":"ignored","downloadId":"ABC123","series":{"id":122},"episodes":[{"id":1,"seasonNumber":2}]}`)
}

func seriesAddPayload() []byte {
	return []byte(`{
		"eventType":"SeriesAdd",
		"series":{"id":42,"title":"Black-ish","titleSlug":"black-ish","tvdbId":269578,"imdbId":"tt3487356"}
	}`)
}

func seriesDeletePayload() []byte {
	return []byte(`{
		"eventType":"SeriesDelete",
		"series":{"id":42,"title":"Black-ish"},
		"deletedFiles":false
	}`)
}

// --- Happy paths: assert the durable INSERT, not Process ------------------

func TestWebhookHandler_Imported_Enqueued_200(t *testing.T) {
	f := newWebhookFixture(t, nil)
	body := importedPayload()
	w := f.post(t, "sonarr-main", body)
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"ok": true}`, w.Body.String())

	rows := f.rows()
	require.Len(t, rows, 1)
	assert.Equal(t, ports.WebhookInboxStatusPending, rows[0].Status)
	assert.Equal(t, "sonarr-main", rows[0].InstanceName,
		"InstanceName must come from URL path, not the payload's instanceName:ignored")
	assert.Equal(t, "Download", rows[0].EventType, "raw Sonarr eventType stored verbatim (D6)")
	assert.Equal(t, body, rows[0].Payload, "raw body stored verbatim for the drainer to re-map")
	assert.Contains(t, string(rows[0].Payload), `"downloadId":"ABC123"`,
		"downloadId is preserved in the stored body — parsing is deferred to the drainer")
	assert.GreaterOrEqual(t, f.pokeCount.Load(), int32(1), "drainer poked after a successful enqueue")
}

func TestWebhookHandler_ImportFailed_Enqueued_200(t *testing.T) {
	f := newWebhookFixture(t, nil)
	w := f.post(t, "sonarr-main", importFailedPayload())
	require.Equal(t, http.StatusOK, w.Code)
	rows := f.rows()
	require.Len(t, rows, 1)
	assert.Equal(t, "ManualInteractionRequired", rows[0].EventType)
}

func TestWebhookHandler_Grabbed_FortyCharHex_Enqueued_200(t *testing.T) {
	f := newWebhookFixture(t, nil)
	body := grabPayloadWithHash()
	w := f.post(t, "sonarr-main", body)
	require.Equal(t, http.StatusOK, w.Code)
	rows := f.rows()
	require.Len(t, rows, 1)
	assert.Equal(t, "Grab", rows[0].EventType)
	assert.Equal(t, body, rows[0].Payload)
	assert.Contains(t, string(rows[0].Payload),
		"0123456789abcdef0123456789abcdef01234567",
		"40-char hex downloadId reaches the inbox verbatim — parsing happens in the drainer")
}

func TestWebhookHandler_Grabbed_ShortDownloadId_Enqueued_200(t *testing.T) {
	f := newWebhookFixture(t, nil)
	w := f.post(t, "sonarr-main", grabPayloadShortHash())
	require.Equal(t, http.StatusOK, w.Code)
	rows := f.rows()
	require.Len(t, rows, 1)
	assert.Contains(t, string(rows[0].Payload), `"downloadId":"ABC123"`,
		"the handler does NOT pre-filter malformed hashes — the drainer's ParseTorrentHash decides")
}

func TestWebhookHandler_SeriesAdd_Enqueued_200(t *testing.T) {
	f := newWebhookFixture(t, nil)
	body := seriesAddPayload()
	w := f.post(t, "sonarr-main", body)
	require.Equal(t, http.StatusOK, w.Code)
	rows := f.rows()
	require.Len(t, rows, 1)
	assert.Equal(t, "SeriesAdd", rows[0].EventType)
	assert.Equal(t, "sonarr-main", rows[0].InstanceName)
	assert.Equal(t, body, rows[0].Payload,
		"series fields (tvdbId/imdbId/slug) travel in the stored body — the drainer re-maps them")
}

func TestWebhookHandler_SeriesDelete_Enqueued_200(t *testing.T) {
	f := newWebhookFixture(t, nil)
	w := f.post(t, "sonarr-main", seriesDeletePayload())
	require.Equal(t, http.StatusOK, w.Code)
	rows := f.rows()
	require.Len(t, rows, 1)
	assert.Equal(t, "SeriesDelete", rows[0].EventType)
	assert.Equal(t, "sonarr-main", rows[0].InstanceName)
}

// --- Unsupported: classify-at-ingest -> 200 with NO insert (D1 step 5) -----

func TestWebhookHandler_UnsupportedEvent_200_NoInsert(t *testing.T) {
	f := newWebhookFixture(t, nil)
	w := f.post(t, "sonarr-main", unsupportedPayload())
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"ok": true}`, w.Body.String())
	assert.Empty(t, f.rows(), "unsupported events are dropped at ingest, never enqueued")
	assert.Equal(t, int32(0), f.pokeCount.Load(), "no enqueue -> no poke")
}

// --- 400 paths: reject pre-insert, zero rows ------------------------------

func TestWebhookHandler_MalformedJSON_400(t *testing.T) {
	f := newWebhookFixture(t, nil)
	w := f.post(t, "sonarr-main", []byte(`{"eventType":`))
	require.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "malformed payload", body["error"])
	assert.Empty(t, f.rows())
}

func TestWebhookHandler_EmptyBody_400(t *testing.T) {
	f := newWebhookFixture(t, nil)
	w := f.post(t, "sonarr-main", nil)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, f.rows())
}

func TestWebhookHandler_MissingEventType_400(t *testing.T) {
	f := newWebhookFixture(t, nil)
	w := f.post(t, "sonarr-main", []byte(`{"instanceName":"x"}`))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, f.rows())
}

func TestWebhookHandler_OversizeBody_400(t *testing.T) {
	f := newWebhookFixture(t, nil)
	oversized := bytes.Repeat([]byte("x"), 2<<20)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/webhook/sonarr/sonarr-main", bytes.NewReader(oversized))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "payload too large", body["error"])
	assert.Empty(t, f.rows())
}

// --- Instance gating: 404 stays pre-insert --------------------------------

func TestWebhook_UnknownInstance_404(t *testing.T) {
	t.Parallel()
	f := newWebhookFixture(t, map[string]struct{}{"main": {}})
	w := f.post(t, "ghost", []byte(`{}`))
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Empty(t, f.rows())
}

func TestWebhook_KnownInstance_Enqueued_200(t *testing.T) {
	t.Parallel()
	f := newWebhookFixture(t, map[string]struct{}{"sonarr-main": {}, "sonarr-tv": {}})
	w := f.post(t, "sonarr-tv", importedPayload())
	require.Equal(t, http.StatusOK, w.Code)
	rows := f.rows()
	require.Len(t, rows, 1)
	assert.Equal(t, "sonarr-tv", rows[0].InstanceName)
}

func TestWebhook_NilKnownInstances_AcceptsAny_Enqueued(t *testing.T) {
	t.Parallel()
	f := newWebhookFixture(t, nil)
	w := f.post(t, "sonarr-anything", importedPayload())
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, f.rows(), 1)
}

// --- Enqueue failure -> 500 (F-11: the ONLY 500 path) ---------------------

func TestWebhookHandler_InsertFails_500(t *testing.T) {
	f := newWebhookFixture(t, nil)
	f.insertErr = errors.New("db down")
	w := f.post(t, "sonarr-main", importedPayload())
	require.Equal(t, http.StatusInternalServerError, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "enqueue failed", body["error"])
	assert.Empty(t, f.rows(), "a failed insert records nothing")
	assert.Equal(t, int32(0), f.pokeCount.Load(), "no poke on a failed enqueue")
}

// --- Race smoke: N concurrent posts -> N rows -----------------------------

func TestWebhookHandler_Concurrent_Race(t *testing.T) {
	f := newWebhookFixture(t, nil)
	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			body := []byte(strings.Replace(string(importedPayload()),
				`"ABC123"`, fmt.Sprintf(`"ABC%03d"`, i), 1))
			w := f.post(t, "sonarr-main", body)
			require.Equal(t, http.StatusOK, w.Code)
		}(i)
	}
	wg.Wait()
	assert.Len(t, f.rows(), n)
	assert.Equal(t, int32(n), f.pokeCount.Load())
}
