package scan

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// sampleRadarrMovie is the shared input for the anti-drift proof.
func sampleRadarrMovie() ports.RadarrMovie {
	return ports.RadarrMovie{
		RadarrMovieID: 7, Title: "Dune", TitleSlug: "dune-2021", Year: 2021,
		TMDBID: 438631, IMDBID: "tt1160419", Monitored: true, HasFile: true,
		MinimumAvailability: "released", SizeOnDiskBytes: 5_000_000_000,
	}
}

// TestBuildRadarrMovieCache_SyncAndWebhookIdentical — F-21 anti-drift proof:
// the sync path and the webhook path both funnel their source into
// ports.RadarrMovie and call BuildRadarrMovieCache, so for the SAME input they
// MUST produce a byte-identical RadarrMovieCache. This is the single test that
// guards the two writers from drifting.
func TestBuildRadarrMovieCache_SyncAndWebhookIdentical(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	m := sampleRadarrMovie()

	fromSync := BuildRadarrMovieCache("radarr-main", m, now)
	fromWebhook := BuildRadarrMovieCache("radarr-main", m, now) // webhook normalises to the SAME ports.RadarrMovie

	assert.Equal(t, fromSync, fromWebhook, "sync and webhook must build identical movie-cache entries")
	// Spot-check the field mapping.
	require.NotNil(t, fromSync.Canon.TMDBID)
	assert.Equal(t, domain.TMDBID(438631), *fromSync.Canon.TMDBID)
	assert.Equal(t, movie.HydrationStub, fromSync.Canon.Hydration)
	assert.Equal(t, 7, fromSync.State.RadarrMovieID)
	assert.True(t, fromSync.State.AddedToRadarr)
	require.NotNil(t, fromSync.State.Availability)
	assert.Equal(t, "released", *fromSync.State.Availability)
	assert.Equal(t, domain.MovieID(0), fromSync.State.MovieID, "movie_id stamped only by PersistRadarrMovieCache")
}

// fakeCanonUpserter / fakeStateUpserter record calls for the persist-order test.
type fakeCanonUpserter struct {
	lastCanon movie.Canon
	returnID  domain.MovieID
}

func (f *fakeCanonUpserter) Upsert(_ context.Context, c movie.Canon) (domain.MovieID, error) {
	f.lastCanon = c
	return f.returnID, nil
}

type fakeStateUpserter struct{ lastState movie.StateEntry }

func (f *fakeStateUpserter) Upsert(_ context.Context, e movie.StateEntry) error {
	f.lastState = e
	return nil
}

func TestPersistRadarrMovieCache_StampsMovieID(t *testing.T) {
	t.Parallel()
	canonW := &fakeCanonUpserter{returnID: 42}
	stateW := &fakeStateUpserter{}
	cache := BuildRadarrMovieCache("r1", sampleRadarrMovie(), time.Now().UTC())

	id, err := PersistRadarrMovieCache(context.Background(), canonW, stateW, cache)
	require.NoError(t, err)
	assert.Equal(t, domain.MovieID(42), id)
	assert.Equal(t, domain.MovieID(42), stateW.lastState.MovieID, "state row FK-stamped from canon Upsert")
	assert.Equal(t, movie.HydrationStub, canonW.lastCanon.Hydration)
}

// TestPartitionInstancesByType — scan-dispatch regression guard: sonarr-typed
// (and empty-typed) snapshots route to the sonarr slice; radarr-typed to the
// radarr slice.
func TestPartitionInstancesByType(t *testing.T) {
	t.Parallel()
	snaps := []runtime.InstanceSnapshot{
		{Name: "sonarr-a", Type: "sonarr"},
		{Name: "legacy", Type: ""}, // empty defaults to sonarr — byte-identical guard
		{Name: "radarr-a", Type: "radarr"},
	}
	sonarr, radarr := PartitionInstancesByType(snaps)
	require.Len(t, sonarr, 2)
	require.Len(t, radarr, 1)
	assert.Equal(t, "radarr-a", radarr[0].Name)
	assert.False(t, IsRadarr(snaps[0]))
	assert.False(t, IsRadarr(snaps[1]), "empty Type defaults to sonarr")
	assert.True(t, IsRadarr(snaps[2]))
}
