package enrichment

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	enrichdomain "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
)

type fakeMoviePicker struct {
	rows []MovieRefreshCandidate
	err  error
	ttl  enrichdomain.RefreshTTL
}

func (f *fakeMoviePicker) PickMovieRefreshCandidates(_ context.Context, _ time.Time, ttl enrichdomain.RefreshTTL, _ int) ([]MovieRefreshCandidate, error) {
	f.ttl = ttl
	return f.rows, f.err
}

type recordingMovieRefresher struct {
	mu   sync.Mutex
	seen []int64
}

func (r *recordingMovieRefresher) HandleForced(_ context.Context, movieID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, movieID)
	return nil
}

// TestMovieRefreshScheduler_Tick_DrivesMovieWorker asserts the movie scheduler
// picks over the movie picker and drives the MOVIE worker for each candidate —
// the budget-isolation guarantee (it never touches the series picker/worker).
func TestMovieRefreshScheduler_Tick_DrivesMovieWorker(t *testing.T) {
	picker := &fakeMoviePicker{rows: []MovieRefreshCandidate{
		{MovieID: 10, Tier: enrichdomain.RefreshTierChanged},
		{MovieID: 20, Tier: enrichdomain.RefreshTierNormal},
	}}
	worker := &recordingMovieRefresher{}

	s, err := NewMovieRefreshScheduler(MovieRefreshSchedulerDeps{
		Picker: picker,
		Worker: worker,
	})
	require.NoError(t, err)

	s.Tick(context.Background())

	assert.Equal(t, []int64{10, 20}, worker.seen, "each movie candidate hydrated exactly once")
	// TTL defaulted (movies reuse the domain RefreshTTL, reading .Normal).
	assert.Equal(t, enrichdomain.DefaultRefreshTTL(), picker.ttl)
}

// TestMovieRefreshScheduler_Tick_EmptyBatch is a clean no-op.
func TestMovieRefreshScheduler_Tick_EmptyBatch(t *testing.T) {
	picker := &fakeMoviePicker{rows: nil}
	worker := &recordingMovieRefresher{}
	s, err := NewMovieRefreshScheduler(MovieRefreshSchedulerDeps{Picker: picker, Worker: worker})
	require.NoError(t, err)
	s.Tick(context.Background())
	assert.Empty(t, worker.seen)
}

func TestNewMovieRefreshScheduler_RequiresPorts(t *testing.T) {
	_, err := NewMovieRefreshScheduler(MovieRefreshSchedulerDeps{Worker: &recordingMovieRefresher{}})
	require.Error(t, err)
	_, err = NewMovieRefreshScheduler(MovieRefreshSchedulerDeps{Picker: &fakeMoviePicker{}})
	require.Error(t, err)
}

type recMovieRefreshMetrics struct {
	mu   sync.Mutex
	heal int
}

func (r *recMovieRefreshMetrics) IncRefresh(enrichdomain.RefreshTier, string) {}
func (r *recMovieRefreshMetrics) ObserveBatchSize(int)                        {}
func (r *recMovieRefreshMetrics) ObserveTickDuration(time.Duration)           {}
func (r *recMovieRefreshMetrics) IncRefreshPickedHeal() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.heal++
}

// TestMovieRefreshScheduler_HealMetricTicksPerCandidate asserts IncRefreshPickedHeal
// fires exactly once per Heal candidate (mirror of the series scheduler test).
func TestMovieRefreshScheduler_HealMetricTicksPerCandidate(t *testing.T) {
	picker := &fakeMoviePicker{rows: []MovieRefreshCandidate{
		{MovieID: 10, Tier: enrichdomain.RefreshTierChanged, Heal: false},
		{MovieID: 20, Tier: enrichdomain.RefreshTierNormal, Heal: true},
		{MovieID: 30, Tier: enrichdomain.RefreshTierNormal, Heal: false},
		{MovieID: 40, Tier: enrichdomain.RefreshTierNormal, Heal: true},
	}}
	worker := &recordingMovieRefresher{}
	m := &recMovieRefreshMetrics{}

	s, err := NewMovieRefreshScheduler(MovieRefreshSchedulerDeps{
		Picker: picker, Worker: worker, Metrics: m,
	})
	require.NoError(t, err)

	s.Tick(context.Background())

	assert.Equal(t, []int64{10, 20, 30, 40}, worker.seen, "every candidate still hydrated")
	assert.Equal(t, 2, m.heal, "one tick per heal candidate")
}
