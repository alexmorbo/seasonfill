package sonarr

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
