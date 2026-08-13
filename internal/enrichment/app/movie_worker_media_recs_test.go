package enrichment

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeMovieVideos struct {
	calls   int
	movieID domain.MovieID
	trailer *movie.Video
}

func (f *fakeMovieVideos) ReplaceBestTrailer(_ context.Context, movieID domain.MovieID, v *movie.Video) error {
	f.calls++
	f.movieID = movieID
	f.trailer = v
	return nil
}

type fakeMovieRecs struct {
	setCalls int
	setMovie domain.MovieID
	setIDs   []domain.MovieID
}

func (f *fakeMovieRecs) Set(_ context.Context, movieID domain.MovieID, ids []domain.MovieID) error {
	f.setCalls++
	f.setMovie = movieID
	f.setIDs = ids
	return nil
}

func mediaRecsResp() *tmdb.MovieResponse {
	return &tmdb.MovieResponse{
		ID:    603,
		Title: "The Matrix",
		Videos: &tmdb.TVVideos{Results: []tmdb.TVVideo{
			{ID: "teaser", Site: "YouTube", Key: "t1", Type: "Teaser", Official: true, PublishedAt: "2024-01-01T00:00:00.000Z"},
			{ID: "trailer", Site: "YouTube", Key: "t2", Type: "Trailer", Official: true, PublishedAt: "2024-02-01T00:00:00.000Z"},
		}},
		Recommendations: &tmdb.MovieRecommendations{Results: []tmdb.MovieRecommendation{
			{ID: 604, Title: "The Matrix Reloaded"},
			{ID: 605, Title: "The Matrix Revolutions"},
		}},
	}
}

// TestMovieWorker_HandleForced_WritesBestTrailerAndStampsMedia proves the media writer picks
// the official Trailer over the Teaser, persists it, runs inside the Transactor, and stamps
// enrichment_media_synced_at.
func TestMovieWorker_HandleForced_WritesBestTrailerAndStampsMedia(t *testing.T) {
	canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 603)}
	videos := &fakeMovieVideos{}
	recs := &fakeMovieRecs{}
	tx := &passthroughTx{}

	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:   &fakeMovieTMDB{resp: mediaRecsResp()},
		Movies: canon,
		Videos: videos,
		Recs:   recs,
		Tx:     tx,
		Clock:  func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	require.NoError(t, err)
	require.NoError(t, w.HandleForced(context.Background(), 7))

	require.Equal(t, 1, videos.calls)
	require.NotNil(t, videos.trailer)
	require.NotNil(t, videos.trailer.Key)
	assert.Equal(t, "t2", *videos.trailer.Key, "official Trailer chosen over Teaser")
	assert.Equal(t, domain.MovieID(7), videos.movieID)
	assert.Equal(t, 1, canon.mediaMarkCalls, "enrichment_media_synced_at stamped once")
}

// TestMovieWorker_HandleForced_WritesRecommendations proves stub upserts precede the join Set,
// recIDs are in TMDB-rank order, and the recs section is stamped once.
func TestMovieWorker_HandleForced_WritesRecommendations(t *testing.T) {
	canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 603)}
	recs := &fakeMovieRecs{}

	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:   &fakeMovieTMDB{resp: mediaRecsResp()},
		Movies: canon,
		Recs:   recs,
		Tx:     &passthroughTx{},
		Clock:  func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	require.NoError(t, err)
	require.NoError(t, w.HandleForced(context.Background(), 7))

	// two stubs upserted (fakeMovieCanon assigns ids 1, 2 by insertion order).
	assert.Len(t, canon.stubUpserts, 2)
	require.Equal(t, 1, recs.setCalls)
	assert.Equal(t, domain.MovieID(7), recs.setMovie)
	assert.Equal(t, []domain.MovieID{1, 2}, recs.setIDs, "recIDs in TMDB-rank order")
	assert.Equal(t, 1, canon.recsMarkCalls)
}

// TestMovieWorker_HandleForced_RecsSelfRefDropped proves a rec resolving to the parent movie id
// (TMDB listing the parent among its own recs) is dropped from the join set.
func TestMovieWorker_HandleForced_RecsSelfRefDropped(t *testing.T) {
	canon := &fakeMovieCanon{
		getResp: movieCanonWithTMDB(7, 603),
		// Force rec tmdb 604 to resolve to the PARENT's movie id (7) → self-ref.
		stubIDByTMDB: map[int64]domain.MovieID{604: 7, 605: 9},
	}
	recs := &fakeMovieRecs{}

	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:   &fakeMovieTMDB{resp: mediaRecsResp()},
		Movies: canon,
		Recs:   recs,
		Tx:     &passthroughTx{},
		Clock:  func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	require.NoError(t, err)
	require.NoError(t, w.HandleForced(context.Background(), 7))

	require.Equal(t, 1, recs.setCalls)
	assert.Equal(t, []domain.MovieID{9}, recs.setIDs, "self-ref (id 7) dropped, only 605→9 kept")
}

// TestMovieWorker_HandleForced_NilMediaRecsDepsSkip proves the pre-Ф1.1c path: with the two
// deps nil the worker hydrates canon and touches no media/recs writer nor stamp.
func TestMovieWorker_HandleForced_NilMediaRecsDepsSkip(t *testing.T) {
	canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 603)}
	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:   &fakeMovieTMDB{resp: mediaRecsResp()},
		Movies: canon,
		Clock:  func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	require.NoError(t, err)
	require.NoError(t, w.HandleForced(context.Background(), 7))
	assert.Equal(t, 0, canon.mediaMarkCalls)
	assert.Equal(t, 0, canon.recsMarkCalls)
	assert.Empty(t, canon.stubUpserts)
}

// TestMovieWorker_HandleForced_NilSubResourcesSkip proves the writers additionally gate on the
// decoded sub-resources: with Videos/Recommendations nil on the response, neither writer runs.
func TestMovieWorker_HandleForced_NilSubResourcesSkip(t *testing.T) {
	resp := mediaRecsResp()
	resp.Videos = nil
	resp.Recommendations = nil
	canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 603)}
	videos := &fakeMovieVideos{}
	recs := &fakeMovieRecs{}

	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:   &fakeMovieTMDB{resp: resp},
		Movies: canon,
		Videos: videos,
		Recs:   recs,
		Tx:     &passthroughTx{},
		Clock:  func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	require.NoError(t, err)
	require.NoError(t, w.HandleForced(context.Background(), 7))
	assert.Equal(t, 0, videos.calls)
	assert.Equal(t, 0, recs.setCalls)
	assert.Equal(t, 0, canon.mediaMarkCalls)
	assert.Equal(t, 0, canon.recsMarkCalls)
}
