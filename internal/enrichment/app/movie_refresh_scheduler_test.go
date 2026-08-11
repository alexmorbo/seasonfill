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
