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
