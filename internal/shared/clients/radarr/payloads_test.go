package radarr

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/arrcore"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestQueueAll_MapsRecordsAndParams(t *testing.T) {
	var (
		mu    sync.Mutex
		gotQ  map[string]string
		gotPa string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPa = r.URL.Path
		gotQ = map[string]string{
			"includeMovie":             r.URL.Query().Get("includeMovie"),
			"includeUnknownMovieItems": r.URL.Query().Get("includeUnknownMovieItems"),
			"page":                     r.URL.Query().Get("page"),
			"pageSize":                 r.URL.Query().Get("pageSize"),
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"page":1,"pageSize":1000,"totalRecords":2,
			"records":[
				{"id":11,"movieId":42,"title":"Dune.2021.2160p","status":"downloading",
				 "trackedDownloadStatus":"ok","trackedDownloadState":"downloading",
				 "downloadId":"AABBCCDDEEFF00112233445566778899AABBCCDD",
				 "downloadClient":"qBittorrent","protocol":"torrent",
				 "size":3164549982,"sizeleft":1024},
				{"id":12,"movieId":43,"title":"Arrival","downloadId":"","protocol":"usenet"}
			]}`))
	}))
	t.Cleanup(srv.Close)

	c := newClient(t, srv)
	p, err := c.QueueAll(context.Background())
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "/api/v3/queue", gotPa)
	assert.Equal(t, "false", gotQ["includeMovie"])
	assert.Equal(t, "false", gotQ["includeUnknownMovieItems"])
	assert.Equal(t, "1", gotQ["page"])
	assert.Equal(t, "1000", gotQ["pageSize"])

	// Both records are returned; the reconciler (not the client) drops the
	// empty-downloadId one.
	require.Len(t, p.Records, 2)
	assert.Equal(t, 2, p.TotalRecords, "TotalRecords is the count WE returned")
	assert.Equal(t, shareddomain.RadarrMovieID(42), p.Records[0].MovieID)
	assert.Equal(t, "aabbccddeeff00112233445566778899aabbccdd", p.Records[0].DownloadID,
		"downloadId is lower-cased at the client boundary")
	assert.Equal(t, int64(3164549982), p.Records[0].Size)
	assert.Equal(t, int64(1024), p.Records[0].SizeLeft)
	assert.Equal(t, "downloading", p.Records[0].Status)
	assert.Equal(t, "ok", p.Records[0].TrackedDownloadStatus)
	assert.Equal(t, "torrent", p.Records[0].Protocol)
	assert.Empty(t, p.Records[1].DownloadID)
}

// Radarr's QueueResource.Size is a C# decimal — a fractional value must NOT
// break the page. Regression guard for the float64 DTO field.
func TestQueueAll_FractionalSizeDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"page":1,"pageSize":1000,"totalRecords":1,
			"records":[{"id":1,"movieId":5,"downloadId":"ff","size":1536.75,"sizeleft":0.5}]}`))
	}))
	t.Cleanup(srv.Close)

	p, err := newClient(t, srv).QueueAll(context.Background())
	require.NoError(t, err)
	require.Len(t, p.Records, 1)
	assert.Equal(t, int64(1536), p.Records[0].Size)
	assert.Equal(t, int64(0), p.Records[0].SizeLeft)
}

// Server clamps pageSize to 2 -> the walker must keep paging until a short
// page arrives, not stop after page 1.
func TestQueueAll_PaginatesUntilShortPage(t *testing.T) {
	var mu sync.Mutex
	pages := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("page")
		mu.Lock()
		pages = append(pages, p)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch p {
		case "1":
			_, _ = w.Write([]byte(`{"page":1,"pageSize":2,"totalRecords":3,"records":[
				{"id":1,"movieId":1,"downloadId":"aa"},{"id":2,"movieId":2,"downloadId":"bb"}]}`))
		default:
			_, _ = w.Write([]byte(`{"page":2,"pageSize":2,"totalRecords":3,"records":[
				{"id":3,"movieId":3,"downloadId":"cc"}]}`))
		}
	}))
	t.Cleanup(srv.Close)

	p, err := newClient(t, srv).QueueAll(context.Background())
	require.NoError(t, err)
	require.Len(t, p.Records, 3)
	assert.Equal(t, 3, p.TotalRecords)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"1", "2"}, pages)
}

func TestQueueAll_EmptyQueue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"page":1,"pageSize":1000,"totalRecords":0,"records":[]}`))
	}))
	t.Cleanup(srv.Close)

	p, err := newClient(t, srv).QueueAll(context.Background())
	require.NoError(t, err)
	assert.Empty(t, p.Records)
	assert.Equal(t, 0, p.TotalRecords)
}

func TestQueueAll_StatusErrorSaysRadarr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	_, err := newClient(t, srv).QueueAll(context.Background())
	require.Error(t, err)
	var se *arrcore.StatusError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, "radarr", se.Arr)
}

func TestGrabHistoryPaged_SendsEventType1AndMaps(t *testing.T) {
	var (
		mu   sync.Mutex
		gotQ map[string]string
		path string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		path = r.URL.Path
		gotQ = map[string]string{
			"eventType":     r.URL.Query().Get("eventType"),
			"page":          r.URL.Query().Get("page"),
			"pageSize":      r.URL.Query().Get("pageSize"),
			"sortKey":       r.URL.Query().Get("sortKey"),
			"sortDirection": r.URL.Query().Get("sortDirection"),
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"page":2,"pageSize":50,"totalRecords":120,"records":[
			{"id":1,"movieId":42,"eventType":"grabbed","downloadId":"AABB","sourceTitle":"Dune"},
			{"id":2,"movieId":43,"eventType":"grabbed","sourceTitle":"usenet-no-hash"}
		]}`))
	}))
	t.Cleanup(srv.Close)

	hp, err := newClient(t, srv).GrabHistoryPaged(context.Background(), 2, 50)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "/api/v3/history", path)
	assert.Equal(t, "1", gotQ["eventType"], "Radarr grabbed == 1")
	assert.Equal(t, "2", gotQ["page"])
	assert.Equal(t, "50", gotQ["pageSize"])
	assert.Equal(t, "date", gotQ["sortKey"])
	assert.Equal(t, "descending", gotQ["sortDirection"])

	require.Len(t, hp.Records, 1, "the downloadId-less usenet record is dropped")
	assert.Equal(t, "aabb", hp.Records[0].DownloadID)
	assert.Equal(t, shareddomain.RadarrMovieID(42), hp.Records[0].MovieID)
	assert.Equal(t, "grabbed", hp.Records[0].EventType)
	assert.Equal(t, 2, hp.RawCount, "RawCount counts records BEFORE the filter")
	assert.Equal(t, 120, hp.TotalRecords)
	assert.Equal(t, 2, hp.Page)
}

func TestImportHistoryPaged_SendsEventType2(t *testing.T) {
	var (
		mu     sync.Mutex
		gotEvt string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotEvt = r.URL.Query().Get("eventType")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"page":1,"pageSize":50,"totalRecords":1,"records":[
			{"id":9,"movieId":77,"eventType":"downloadFolderImported","downloadId":"CCDD"}]}`))
	}))
	t.Cleanup(srv.Close)

	hp, err := newClient(t, srv).ImportHistoryPaged(context.Background(), 1, 50)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "2", gotEvt, "Radarr downloadFolderImported == 2 (Sonarr's is 3 — enums diverge)")
	require.Len(t, hp.Records, 1)
	assert.Equal(t, "ccdd", hp.Records[0].DownloadID)
	assert.Equal(t, shareddomain.RadarrMovieID(77), hp.Records[0].MovieID)
}

func TestHistoryPaged_ClampsNonPositivePageAndSize(t *testing.T) {
	var (
		mu             sync.Mutex
		gotPage, gotPS string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPage, gotPS = r.URL.Query().Get("page"), r.URL.Query().Get("pageSize")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"page":1,"pageSize":50,"totalRecords":0,"records":[]}`))
	}))
	t.Cleanup(srv.Close)

	hp, err := newClient(t, srv).GrabHistoryPaged(context.Background(), 0, 0)
	require.NoError(t, err)
	assert.Empty(t, hp.Records)
	assert.Equal(t, 0, hp.RawCount)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "1", gotPage)
	assert.Equal(t, fmt.Sprintf("%d", defaultHistoryPageSize), gotPS)
}

func TestHistoryPaged_ErrorWrapsPageAndEventType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	_, err := newClient(t, srv).GrabHistoryPaged(context.Background(), 3, 50)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "history page 3 (eventType=1)")
}
