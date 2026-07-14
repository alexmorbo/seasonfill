package sonarr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// TestQueuePayloadDTO_ParsesSizeAndProgress — story 379. Verifies the
// queue projector picks up the new size + sizeleft fields and falls
// back to the nested episode block for season/episode number + title
// when the top-level fields are zero.
func TestQueuePayloadDTO_ParsesSizeAndProgress(t *testing.T) {
	t.Parallel()
	body := `{
        "totalRecords": 1,
        "records": [{
            "id": 17,
            "seriesId": 42,
            "episodeId": 555,
            "seasonNumber": 0,
            "title": "Rick.and.Morty.S05E03.1080p.mkv",
            "status": "downloading",
            "downloadId": "abc",
            "protocol": "torrent",
            "size": 2000000000,
            "sizeleft": 600000000,
            "episode": {"id":555,"episodeNumber":3,"seasonNumber":5,"title":"A Rickconvenient Mort","monitored":true,"hasFile":false,"airDateUtc":"2024-01-01T00:00:00Z","episodeFileId":0}
        }]
    }`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	q, err := c.Queue(context.Background(), 42)
	require.NoError(t, err)
	require.Len(t, q.Records, 1)
	r := q.Records[0]
	assert.Equal(t, 3, r.EpisodeNumber)
	assert.Equal(t, 5, r.SeasonNumber)
	assert.Equal(t, int64(2000000000), r.Size)
	assert.Equal(t, int64(600000000), r.SizeLeft)
	assert.Equal(t, "A Rickconvenient Mort", r.Title)
	assert.Equal(t, "downloading", r.Status)
}

// TestClient_Queue_FiltersBySeriesID — bug #1134. Sonarr's /api/v3/queue
// ignores the seriesId query param and returns the ENTIRE global queue;
// Client.Queue MUST drop foreign-series records client-side so the hero
// download chip is per-series, not a global leak.
func TestClient_Queue_FiltersBySeriesID(t *testing.T) {
	t.Parallel()
	body := `{
        "totalRecords": 3,
        "records": [
            {"id": 1, "seriesId": 42, "episodeId": 100, "seasonNumber": 1,
             "title": "Wanted.S01E01", "status": "downloading",
             "downloadId": "AAA", "protocol": "torrent", "size": 100, "sizeleft": 40},
            {"id": 2, "seriesId": 99, "episodeId": 200, "seasonNumber": 2,
             "title": "Foreign.S02E01", "status": "downloading",
             "downloadId": "BBB", "protocol": "torrent", "size": 100, "sizeleft": 10},
            {"id": 3, "seriesId": 42, "episodeId": 101, "seasonNumber": 1,
             "title": "Wanted.S01E02", "status": "queued",
             "downloadId": "CCC", "protocol": "torrent", "size": 100, "sizeleft": 100}
        ]
    }`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	q, err := c.Queue(context.Background(), 42)
	require.NoError(t, err)
	require.Len(t, q.Records, 2, "only seriesId==42 records survive; the global queue's foreign record is dropped client-side")
	for _, r := range q.Records {
		assert.Equal(t, shareddomain.SonarrSeriesID(42), r.SeriesID, "no foreign-series record may leak")
	}
	ids := []int{q.Records[0].ID, q.Records[1].ID}
	assert.ElementsMatch(t, []int{1, 3}, ids)
}

// queueTestRecord is the minimal queue record the pagination fakes emit.
type queueTestRecord struct {
	ID           int    `json:"id"`
	SeriesID     int    `json:"seriesId"`
	EpisodeID    int    `json:"episodeId"`
	SeasonNumber int    `json:"seasonNumber"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	DownloadID   string `json:"downloadId"`
	Protocol     string `json:"protocol"`
}

type queueTestResponse struct {
	Page         int               `json:"page"`
	PageSize     int               `json:"pageSize"`
	TotalRecords int               `json:"totalRecords"`
	Records      []queueTestRecord `json:"records"`
}

// paginatedQueueServer returns a fake Sonarr /api/v3/queue that honours the
// `page` query param over a GLOBAL queue of 1002 records: page 1 = 1000 foreign
// records (seriesId 99), page 2 = 2 records for the target series (seriesId 42).
// The target's records live ONLY on page 2, so a single-page (page-1-only) fetch
// drops them entirely — the regression this story fixes.
func paginatedQueueServer(t *testing.T) *httptest.Server {
	t.Helper()
	const pageSize = 1000
	const target = 42
	const foreign = 99
	const total = pageSize + 2

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		var recs []queueTestRecord
		switch page {
		case 1:
			recs = make([]queueTestRecord, 0, pageSize)
			for i := range pageSize {
				recs = append(recs, queueTestRecord{
					ID:           i + 1,
					SeriesID:     foreign,
					EpisodeID:    5000 + i,
					SeasonNumber: 1,
					Title:        "Foreign.Show",
					Status:       "downloading",
					DownloadID:   fmt.Sprintf("F%04d", i),
					Protocol:     "torrent",
				})
			}
		case 2:
			recs = []queueTestRecord{
				{ID: 2001, SeriesID: target, EpisodeID: 100, SeasonNumber: 3,
					Title: "Target.S03E01", Status: "downloading", DownloadID: "T1", Protocol: "torrent"},
				{ID: 2002, SeriesID: target, EpisodeID: 101, SeasonNumber: 3,
					Title: "Target.S03E02", Status: "queued", DownloadID: "T2", Protocol: "torrent"},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(queueTestResponse{
			Page:         page,
			PageSize:     pageSize,
			TotalRecords: total,
			Records:      recs,
		})
	}))
}

// TestClient_Queue_PaginatesGlobalQueue — regression for the page-1-only bug.
// Sonarr ignores seriesId and paginates the global queue; the target series'
// records live only on page 2. Client.Queue MUST walk pages and return all of
// them. FAILS on the pre-fix single-request code (returns 0 records).
func TestClient_Queue_PaginatesGlobalQueue(t *testing.T) {
	t.Parallel()
	srv := paginatedQueueServer(t)
	defer srv.Close()

	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	q, err := c.Queue(context.Background(), 42)
	require.NoError(t, err)
	require.Len(t, q.Records, 2, "both target records live on page 2; a single-page fetch silently drops them")
	assert.Equal(t, 2, q.TotalRecords, "F-04: TotalRecords reflects the filtered subset, not the global 1002")
	for _, r := range q.Records {
		assert.Equal(t, shareddomain.SonarrSeriesID(42), r.SeriesID, "no foreign-series record may leak")
	}
	ids := []int{q.Records[0].ID, q.Records[1].ID}
	assert.ElementsMatch(t, []int{2001, 2002}, ids)
}

// TestClient_Queue_SeriesIDZeroReturnsAllPages — the seriesID==0 (unfiltered)
// path must also paginate: every record on every page is returned. Confirms the
// pagination loop preserves the original unfiltered behaviour across page
// boundaries (1000 foreign on page 1 + 2 target on page 2 = 1002).
func TestClient_Queue_SeriesIDZeroReturnsAllPages(t *testing.T) {
	t.Parallel()
	srv := paginatedQueueServer(t)
	defer srv.Close()

	c := New("test", srv.URL, "secret", 5*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	q, err := c.Queue(context.Background(), 0)
	require.NoError(t, err)
	require.Len(t, q.Records, 1002, "seriesID==0 is unfiltered and must span both pages")
	assert.Equal(t, 1002, q.TotalRecords)
}
