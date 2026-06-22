package enrichment

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// TestSeriesWorker_EpisodeCredits_WrittenToPersonCredits is the D-7
// (468b) Part A integration test: a hydrated season with guest_stars +
// crew MUST be projected onto person_credits with
// media_type='tv_episode' and tmdb_media_id=<episode tmdb_id>. Replaces
// the dropped episode_people write path.
//
// The worker upserts a stub Person for every unseen guest-star tmdb_id
// (series-level aggregate_credits does NOT carry per-episode guest
// stars), so the FK target exists by the time PersonCredits.BatchUpsert
// runs.
func TestSeriesWorker_EpisodeCredits_WrittenToPersonCredits(t *testing.T) {
	t.Parallel()
	tv := minimalTV()
	const (
		ep1ID      int64 = 700001
		ep2ID      int64 = 700002
		guestATMDB       = 900001
		guestBTMDB       = 900002
		crewATMDB        = 900003
	)
	guestACharacter := "Guest A"
	guestBCharacter := "Guest B"
	seasons := map[int]*tmdb.SeasonResponse{
		1: {
			ID: 100, SeasonNumber: 1, AirDate: "2025-12-01",
			Episodes: []tmdb.SeasonEpisode{
				{
					ID: ep1ID, EpisodeNumber: 1, SeasonNumber: 1,
					Name: "Pilot", Overview: "pilot ov", EpisodeType: "standard",
					GuestStars: []tmdb.SeasonGuestStar{
						{ID: guestATMDB, Name: "Actor A", CreditID: "ep-credit-A1", Character: guestACharacter, Order: 1},
						{ID: guestBTMDB, Name: "Actor B", CreditID: "ep-credit-B1", Character: guestBCharacter, Order: 2},
					},
					Crew: []tmdb.SeasonCrewMember{
						{ID: crewATMDB, Name: "Director X", CreditID: "ep-credit-X1", Department: "Directing", Job: "Director"},
					},
				},
				{
					ID: ep2ID, EpisodeNumber: 2, SeasonNumber: 1,
					Name: "Episode Two", Overview: "ep2 ov", EpisodeType: "standard",
					GuestStars: []tmdb.SeasonGuestStar{
						// Guest A re-appears — natural-key (person_id,
						// tmdb_credit_id) is unique per role, so the second
						// row with a new credit_id is its own person_credit.
						{ID: guestATMDB, Name: "Actor A", CreditID: "ep-credit-A2", Character: guestACharacter, Order: 1},
					},
				},
			},
		},
	}

	f := newWorkerFixture(t, tv, seasons)
	tmdbID := domain.TMDBID(42)
	f.seedCanon(1, &tmdbID)

	require.NoError(t, f.worker.Handle(context.Background(), 1))

	// Filter the recorded rows down to episode-level credits.
	episodeRows := make([]people.PersonCredit, 0)
	for _, pc := range f.personCredits.rows {
		if pc.MediaType == tmdb.MediaTypeTVEpisode {
			episodeRows = append(episodeRows, pc)
		}
	}
	require.Len(t, episodeRows, 4,
		"expected 4 episode_credits: ep1 guests (A,B), ep1 crew (X), ep2 guest (A reappears)")

	// Group by tmdb_media_id (which is tmdb episode id) so each row's
	// FK is verified — must point at the SeasonEpisode.ID, NOT the
	// season id and NOT the canon series id.
	byEpisode := map[int64][]people.PersonCredit{}
	for _, r := range episodeRows {
		byEpisode[r.TMDBMediaID] = append(byEpisode[r.TMDBMediaID], r)
	}
	require.Len(t, byEpisode[ep1ID], 3, "ep1 contributes 2 guests + 1 crew")
	require.Len(t, byEpisode[ep2ID], 1, "ep2 contributes 1 guest")

	for _, r := range episodeRows {
		assert.NotEqual(t, int64(0), r.PersonID,
			"PersonID MUST be resolved — stub upsert ran before BatchUpsert")
		assert.Equal(t, tmdb.MediaTypeTVEpisode, r.MediaType)
		assert.NotEmpty(t, r.TMDBCreditID, "tmdb_credit_id required by repo guard")
		assert.NotEmpty(t, r.Title, "title required by repo guard (episode Name)")
	}

	// Kind breakdown
	kindCount := map[people.SeriesCreditKind]int{}
	for _, r := range episodeRows {
		kindCount[r.Kind]++
	}
	assert.Equal(t, 3, kindCount[people.SeriesCreditCast], "guest_stars → cast kind")
	assert.Equal(t, 1, kindCount[people.SeriesCreditCrew], "episode crew → crew kind")

	// Character / Department / Job propagation
	for _, r := range episodeRows {
		switch r.TMDBCreditID {
		case "ep-credit-A1", "ep-credit-A2", "ep-credit-B1":
			require.NotNil(t, r.CharacterName)
			assert.NotEmpty(t, *r.CharacterName)
			assert.Nil(t, r.Department)
			assert.Nil(t, r.Job)
		case "ep-credit-X1":
			require.NotNil(t, r.Department)
			require.NotNil(t, r.Job)
			assert.Equal(t, "Directing", *r.Department)
			assert.Equal(t, "Director", *r.Job)
		}
	}
}

// TestSeriesWorker_EpisodeCredits_NoSeasons_NoOp covers the case where
// the worker fetches no seasons (refresh path with fully-closed
// seasons). Step 7b must not call BatchUpsert with episode rows.
func TestSeriesWorker_EpisodeCredits_NoSeasons_NoOp(t *testing.T) {
	t.Parallel()
	tv := minimalTV()
	// minimalSeason has no GuestStars / Crew → 7b walks but emits zero
	// tv_episode rows; series-level (tv) rows from step 7 still ship.
	f := newWorkerFixture(t, tv, map[int]*tmdb.SeasonResponse{1: minimalSeason()})
	tmdbID := domain.TMDBID(42)
	f.seedCanon(1, &tmdbID)

	require.NoError(t, f.worker.Handle(context.Background(), 1))

	for _, pc := range f.personCredits.rows {
		assert.NotEqual(t, tmdb.MediaTypeTVEpisode, pc.MediaType,
			"empty seasons MUST NOT surface tv_episode rows")
	}
}

// TestSeriesWorker_EpisodeCredits_EmptyCreditID_Dropped guards against
// TMDB payload anomalies where guest_stars[*].credit_id is empty —
// the repo would reject the row on BatchUpsert; the worker drops it
// up front so the batch succeeds.
func TestSeriesWorker_EpisodeCredits_EmptyCreditID_Dropped(t *testing.T) {
	t.Parallel()
	tv := minimalTV()
	seasons := map[int]*tmdb.SeasonResponse{
		1: {
			ID: 100, SeasonNumber: 1, AirDate: "2025-12-01",
			Episodes: []tmdb.SeasonEpisode{
				{
					ID: 700001, EpisodeNumber: 1, SeasonNumber: 1,
					Name: "Pilot",
					GuestStars: []tmdb.SeasonGuestStar{
						{ID: 900001, Name: "No Credit", CreditID: "", Character: "ghost", Order: 1},
						{ID: 900002, Name: "Has Credit", CreditID: "ep-credit-keep", Character: "real", Order: 2},
					},
				},
			},
		},
	}
	f := newWorkerFixture(t, tv, seasons)
	tmdbID := domain.TMDBID(42)
	f.seedCanon(1, &tmdbID)

	require.NoError(t, f.worker.Handle(context.Background(), 1))

	kept := make([]people.PersonCredit, 0)
	for _, pc := range f.personCredits.rows {
		if pc.MediaType == tmdb.MediaTypeTVEpisode {
			kept = append(kept, pc)
		}
	}
	require.Len(t, kept, 1, "empty credit_id MUST be dropped")
	assert.Equal(t, "ep-credit-keep", kept[0].TMDBCreditID)
}
