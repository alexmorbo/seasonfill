package media

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/alexmorbo/seasonfill/internal/observability"
)

func ondemandResultCount(result string) uint64 {
	return metrics.GetOrCreateCounter(
		`seasonfill_media_ondemand_total{result="` + result + `"}`,
	).Get()
}

func cooldownSizeGauge() float64 {
	return metrics.GetOrCreateGauge(`seasonfill_media_ondemand_cooldown_size`, nil).Get()
}

// FetchSync success (full fetch) → ondemand_total{result="success"} +1, and the metric
// surfaces via WritePrometheus.
func TestOnDemandMetrics_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("hello"))
	}))
	defer server.Close()

	f, err := NewOnDemandFetcher(OnDemandDeps{
		Store: newOndemandFakeStore(), Repo: newOndemandFakeRepo(),
		HTTPClient: server.Client(),
		Limiter:    rate.NewLimiter(rate.Inf, 1),
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	require.NoError(t, err)

	before := ondemandResultCount("success")
	_, ok := f.FetchSync(t.Context(), server.URL+"/img.jpg", "poster_w342", "jpg")
	require.True(t, ok)
	assert.Equal(t, uint64(1), ondemandResultCount("success")-before)

	var buf strings.Builder
	observability.WritePrometheus(&buf)
	assert.Contains(t, buf.String(), `seasonfill_media_ondemand_total{result="success"}`)
}

// FetchSync cooldown short-circuit → ondemand_total{result="cooldown_short_circuit"} +1.
// First call fails (500 upstream) → marks cooldown; second call (clock frozen) short-circuits.
func TestOnDemandMetrics_CooldownShortCircuit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	now := time.Now().UTC()
	f, err := NewOnDemandFetcher(OnDemandDeps{
		Store: newOndemandFakeStore(), Repo: newOndemandFakeRepo(),
		HTTPClient: server.Client(),
		Limiter:    rate.NewLimiter(rate.Inf, 1),
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Clock:      func() time.Time { return now },
	})
	require.NoError(t, err)

	url := server.URL + "/img.jpg"
	beforeFail := ondemandResultCount("fail")
	beforeCD := ondemandResultCount("cooldown_short_circuit")

	_, ok := f.FetchSync(t.Context(), url, "poster_w342", "jpg")
	require.False(t, ok) // real fetch fails → result=fail, cooldown set
	assert.Equal(t, uint64(1), ondemandResultCount("fail")-beforeFail)

	_, ok = f.FetchSync(t.Context(), url, "poster_w342", "jpg")
	require.False(t, ok) // clock frozen → cooldown gate → result=cooldown_short_circuit
	assert.Equal(t, uint64(1), ondemandResultCount("cooldown_short_circuit")-beforeCD)
}

// Cooldown-size gauge: markFailed adds (gauge → n>0), a successful full fetch clears it
// (gauge back toward 0). Exercised through the public FetchSync surface.
func TestOnDemandMetrics_CooldownSizeAddThenRemove(t *testing.T) {
	// Phase 1: a failing upstream → cooldown entry added → gauge >= 1.
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failSrv.Close()

	f, err := NewOnDemandFetcher(OnDemandDeps{
		Store: newOndemandFakeStore(), Repo: newOndemandFakeRepo(),
		HTTPClient: failSrv.Client(),
		Limiter:    rate.NewLimiter(rate.Inf, 1),
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	require.NoError(t, err)

	_, ok := f.FetchSync(t.Context(), failSrv.URL+"/img.jpg", "poster_w342", "jpg")
	require.False(t, ok)
	assert.GreaterOrEqual(t, cooldownSizeGauge(), float64(1),
		"markFailed must Set cooldown_size to the current map length")

	// Phase 2: clearCooldown drops the entry → gauge decrements. Drive it directly
	// through the concrete fetcher's clearCooldown (same-package access), which is the
	// exact call the success paths make.
	impl, isImpl := f.(*onDemandFetcher)
	require.True(t, isImpl)
	sizeBefore := cooldownSizeGauge()
	impl.clearCooldown(HashFromURL(failSrv.URL + "/img.jpg"))
	assert.Less(t, cooldownSizeGauge(), sizeBefore,
		"clearCooldown must Set cooldown_size to the reduced map length")
}
