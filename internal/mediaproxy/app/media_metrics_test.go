package media

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/stretchr/testify/require"

	media "github.com/alexmorbo/seasonfill/internal/mediaproxy/domain"
	"github.com/alexmorbo/seasonfill/internal/observability"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// TestMediaMetrics_SuccessPath drives one job to status=stored and
// asserts the success outcome counter + duration histogram are emitted,
// the workers/capacity gauges reflect config, and inflight settles to 0.
func TestMediaMetrics_SuccessPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("IMG-BYTES"))
	}))
	defer srv.Close()

	eq := NewEnqueuer(discardLogger())
	repo := newFakeRepo()
	store := newFakeStore()
	d, err := NewDownloader(eq, DownloaderDeps{
		Store:      store,
		Repo:       repo,
		HTTPClient: srv.Client(),
		Logger:     discardLogger(),
		Workers:    1,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx)

	url := srv.URL + "/p.jpg"
	eq.Enqueue(ctx, []EnqueueRequest{{UpstreamURL: url, Kind: "poster_w342", Extension: "jpg"}})

	require.Eventually(t, func() bool {
		a, gErr := repo.Get(ctx, HashFromURL(url))
		return gErr == nil && a.Status == media.StatusStored
	}, 3*time.Second, 20*time.Millisecond)

	cancel()
	eq.Close()
	d.Close()

	// Labeled families via WritePrometheus string-contains (exact name+label).
	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	s := buf.String()
	require.Contains(t, s, `seasonfill_media_download_total{outcome="success"}`)
	require.Contains(t, s, `seasonfill_media_download_duration_seconds_sum{outcome="success"}`)

	// Gauge values (float-format-independent).
	require.Equal(t, float64(1),
		metrics.GetOrCreateGauge(observability.MetricMediaDownloaderWorkers, nil).Get())
	require.Equal(t, float64(channelCap),
		metrics.GetOrCreateGauge(observability.MetricMediaPrewarmQueueCapacity, nil).Get())
	require.Equal(t, float64(0),
		metrics.GetOrCreateGauge(observability.MetricMediaDownloaderInflight, nil).Get())
}

// TestMediaMetrics_Drop fills the channel past capacity with no consumer
// running so the (cap+1)th enqueue drops; asserts the drop counter ticks
// and the depth gauge reflects a full queue.
func TestMediaMetrics_Drop(t *testing.T) {
	eq := NewEnqueuer(discardLogger())

	reqs := make([]EnqueueRequest, channelCap+1)
	for i := range reqs {
		reqs[i] = EnqueueRequest{
			UpstreamURL: "https://image.tmdb.org/t/p/w342/x" + strconv.Itoa(i) + ".jpg",
			Kind:        "poster_w342",
			Extension:   "jpg",
		}
	}

	before := metrics.GetOrCreateCounter(observability.MetricMediaPrewarmDropsTotal).Get()
	eq.Enqueue(context.Background(), reqs)
	after := metrics.GetOrCreateCounter(observability.MetricMediaPrewarmDropsTotal).Get()
	require.Greater(t, after, before)

	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	require.Contains(t, buf.String(), observability.MetricMediaPrewarmDropsTotal)

	// Depth reflects the full channel (channelCap queued, 1 dropped).
	require.Equal(t, float64(channelCap),
		metrics.GetOrCreateGauge(observability.MetricMediaPrewarmQueueDepth, nil).Get())

	eq.Close()
}

// TestMediaMetrics_InflightRisesAndFalls blocks a worker inside the
// download and asserts inflight is exactly 1 during handle and 0 after.
func TestMediaMetrics_InflightRisesAndFalls(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	eq := NewEnqueuer(discardLogger())
	repo := newFakeRepo()
	store := newFakeStore()
	d, err := NewDownloader(eq, DownloaderDeps{
		Store:      store,
		Repo:       repo,
		HTTPClient: srv.Client(),
		Logger:     discardLogger(),
		Workers:    1,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.Start(ctx)

	url := srv.URL + "/held.jpg"
	eq.Enqueue(ctx, []EnqueueRequest{{UpstreamURL: url, Kind: "poster_w342", Extension: "jpg"}})

	// Worker is now blocked inside the download → inflight must be 1.
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("download handler never entered")
	}
	inflight := metrics.GetOrCreateGauge(observability.MetricMediaDownloaderInflight, nil)
	require.Equal(t, float64(1), inflight.Get())

	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	require.Contains(t, buf.String(), observability.MetricMediaDownloaderInflight)

	close(release)

	require.Eventually(t, func() bool {
		a, gErr := repo.Get(ctx, HashFromURL(url))
		return gErr == nil && a.Status == media.StatusStored
	}, 3*time.Second, 20*time.Millisecond)

	// defer DecInflight fires when handle returns → back to 0.
	require.Eventually(t, func() bool {
		return inflight.Get() == 0
	}, time.Second, 10*time.Millisecond)

	cancel()
	eq.Close()
	d.Close()
}
