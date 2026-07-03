package adapters

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// stubCanonReader is the test-only SeriesCanonReader. The production
// adapter wraps *SeriesRepository directly; the test injects a
// canned map so the suite can assert canon-resolution branches
// (TMDB-nil, SeriesNotFoundError) without seeding the canon table on
// every backend.
type stubCanonReader struct {
	rows map[domain.SeriesID]canonView
	err  error
}

func (s stubCanonReader) Get(_ context.Context, id domain.SeriesID) (canonView, error) {
	if s.err != nil {
		return canonView{}, s.err
	}
	row, ok := s.rows[id]
	if !ok {
		return canonView{}, &sharedErrors.SeriesNotFoundError{ID: id}
	}
	return row, nil
}

// seedPersonCreditTV builds a PersonCreditModel literal with the D-7
// discriminator (media_type=tv, tmdb_media_id=tmdbID) preset and a
// validated minimum field set. Callers override Kind/Department/Job
// post-construction.
func seedPersonCreditTV(personID int64, creditID, title string, tmdbID int) database.PersonCreditModel {
	character := "Character " + creditID
	episodes := 10
	return database.PersonCreditModel{
		PersonID:      personID,
		TMDBCreditID:  creditID,
		MediaType:     tmdb.MediaTypeTV,
		TMDBMediaID:   tmdbID,
		Title:         title,
		Kind:          "cast",
		CharacterName: &character,
		EpisodeCount:  &episodes,
	}
}

func tmdbIDPtr(v int) *domain.TMDBID {
	id := domain.TMDBID(v)
	return &id
}

// TestSeriesPeopleAdapter_ListBySeries_FiltersByKind seeds cast + crew
// rows in person_credits keyed by (media_type=tv, tmdb_media_id) and
// asserts the adapter returns the correct kind-filtered subset.
//
// D-0 quality bar: dual-backend (SQLite + Postgres testcontainer).
func TestSeriesPeopleAdapter_ListBySeries_FiltersByKind(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()

			peopleRepo := enrichpersistence.NewPeopleRepository(db)
			pcRepo := enrichpersistence.NewPersonCreditsRepository(db)

			actorName := "Cast Actor"
			actorID, err := peopleRepo.Upsert(ctx, people.Person{
				Name:      actorName,
				Hydration: people.HydrationStub,
				TMDBID:    tmdbIDPtr(8001),
			})
			require.NoError(t, err)

			directorName := "Crew Director"
			directorID, err := peopleRepo.Upsert(ctx, people.Person{
				Name:      directorName,
				Hydration: people.HydrationStub,
				TMDBID:    tmdbIDPtr(8002),
			})
			require.NoError(t, err)

			const tmdbSeriesID = 100088
			castRow := seedPersonCreditTV(actorID, "credit-cast-1", "The Last of Us", tmdbSeriesID)
			castRow.Kind = "cast"
			_, err = pcRepo.Upsert(ctx, castRow)
			require.NoError(t, err)

			crewRow := seedPersonCreditTV(directorID, "credit-crew-1", "The Last of Us", tmdbSeriesID)
			crewRow.Kind = "crew"
			dept := "Directing"
			job := "Director"
			crewRow.Department = &dept
			crewRow.Job = &job
			crewRow.CharacterName = nil
			_, err = pcRepo.Upsert(ctx, crewRow)
			require.NoError(t, err)

			// Seed an unrelated series's credit too — proves the
			// adapter's tmdb_media_id filter on ListByMedia keeps it out.
			otherRow := seedPersonCreditTV(actorID, "credit-other-1", "Different Show", 999999)
			otherRow.Kind = "cast"
			_, err = pcRepo.Upsert(ctx, otherRow)
			require.NoError(t, err)

			reader := stubCanonReader{
				rows: map[domain.SeriesID]canonView{
					42: {TMDBID: tmdbIDPtr(tmdbSeriesID)},
				},
			}
			adapter := newSeriesPeopleFromPersonCreditsForTest(pcRepo, reader)

			cast, err := adapter.ListBySeries(ctx, 42, people.SeriesCreditCast, "en-US")
			require.NoError(t, err)
			require.Len(t, cast, 1, "cast filter must return exactly one row")
			assert.Equal(t, actorID, cast[0].PersonID)
			assert.Equal(t, people.SeriesCreditCast, cast[0].Kind)
			require.NotNil(t, cast[0].CharacterName)
			assert.Equal(t, "Character credit-cast-1", *cast[0].CharacterName)
			assert.Equal(t, domain.SeriesID(42), cast[0].SeriesID,
				"adapter must stamp the requested seriesID (person_credits is keyed by tmdb_media_id, not series.id)")

			crew, err := adapter.ListBySeries(ctx, 42, people.SeriesCreditCrew, "en-US")
			require.NoError(t, err)
			require.Len(t, crew, 1, "crew filter must return exactly one row")
			assert.Equal(t, directorID, crew[0].PersonID)
			assert.Equal(t, people.SeriesCreditCrew, crew[0].Kind)
			require.NotNil(t, crew[0].Department)
			assert.Equal(t, "Directing", *crew[0].Department)
			require.NotNil(t, crew[0].Job)
			assert.Equal(t, "Director", *crew[0].Job)
		})
	}
}

// TestSeriesPeopleAdapter_ListBySeries_LocalizesCharacterName covers S-G:
// with a person_credits_texts row seeded for ru-RU, the adapter propagates
// lang through ListByMediaWithTextFallback and returns the localized
// character name; en-US falls back to the base person_credits value.
func TestSeriesPeopleAdapter_ListBySeries_LocalizesCharacterName(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()

			peopleRepo := enrichpersistence.NewPeopleRepository(db)
			pcRepo := enrichpersistence.NewPersonCreditsRepository(db)
			textsRepo := enrichpersistence.NewPersonCreditsTextsRepository(db)

			actorID, err := peopleRepo.Upsert(ctx, people.Person{
				Name:      "Localize Actor",
				Hydration: people.HydrationStub,
				TMDBID:    tmdbIDPtr(8100),
			})
			require.NoError(t, err)

			const tmdbSeriesID = 100200
			row := seedPersonCreditTV(actorID, "credit-loc-1", "R&M", tmdbSeriesID)
			row.Kind = "cast"
			base := "Rick (base)"
			row.CharacterName = &base
			creditID, err := pcRepo.Upsert(ctx, row)
			require.NoError(t, err)

			ru := "Рик"
			require.NoError(t, textsRepo.Upsert(ctx, people.PersonCreditText{
				PersonCreditID: creditID, Language: "ru-RU", CharacterName: &ru,
			}))

			reader := stubCanonReader{
				rows: map[domain.SeriesID]canonView{
					42: {TMDBID: tmdbIDPtr(tmdbSeriesID)},
				},
			}
			adapter := newSeriesPeopleFromPersonCreditsForTest(pcRepo, reader)

			// ru-RU → localized name.
			ruCast, err := adapter.ListBySeries(ctx, 42, people.SeriesCreditCast, "ru-RU")
			require.NoError(t, err)
			require.Len(t, ruCast, 1)
			require.NotNil(t, ruCast[0].CharacterName)
			assert.Equal(t, "Рик", *ruCast[0].CharacterName)

			// en-US → no texts row → base person_credits value.
			enCast, err := adapter.ListBySeries(ctx, 42, people.SeriesCreditCast, "en-US")
			require.NoError(t, err)
			require.Len(t, enCast, 1)
			require.NotNil(t, enCast[0].CharacterName)
			assert.Equal(t, "Rick (base)", *enCast[0].CharacterName)
		})
	}
}

// TestSeriesPeopleAdapter_ListBySeries_SeriesNotFound proves the
// canon Get's typed SeriesNotFoundError is returned untouched so the
// composer middleware can dispatch series_not_found.
func TestSeriesPeopleAdapter_ListBySeries_SeriesNotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			pcRepo := enrichpersistence.NewPersonCreditsRepository(db)
			// Empty stub reader → every lookup misses with
			// SeriesNotFoundError.
			reader := stubCanonReader{rows: map[domain.SeriesID]canonView{}}
			adapter := newSeriesPeopleFromPersonCreditsForTest(pcRepo, reader)

			_, err := adapter.ListBySeries(ctx, 9999, people.SeriesCreditCast, "en-US")
			require.Error(t, err)
			var seriesNF *sharedErrors.SeriesNotFoundError
			require.True(t, errors.As(err, &seriesNF),
				"adapter must surface SeriesNotFoundError untouched for middleware dispatch")
			assert.Equal(t, domain.SeriesID(9999), seriesNF.ID)
		})
	}
}

// TestSeriesPeopleAdapter_ListBySeries_SeriesWithoutTMDBID covers the
// Sonarr-orphan path: canon has no TMDB id, so there cannot be any
// cast/crew in person_credits — the adapter returns an empty list,
// NOT an error. Cast page renders gracefully.
func TestSeriesPeopleAdapter_ListBySeries_SeriesWithoutTMDBID(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			pcRepo := enrichpersistence.NewPersonCreditsRepository(db)
			reader := stubCanonReader{
				rows: map[domain.SeriesID]canonView{
					42: {TMDBID: nil},
				},
			}
			adapter := newSeriesPeopleFromPersonCreditsForTest(pcRepo, reader)

			cast, err := adapter.ListBySeries(ctx, 42, people.SeriesCreditCast, "en-US")
			require.NoError(t, err, "TMDB-less canon must NOT raise an error")
			assert.Empty(t, cast)

			crew, err := adapter.ListBySeries(ctx, 42, people.SeriesCreditCrew, "en-US")
			require.NoError(t, err)
			assert.Empty(t, crew)
		})
	}
}

// TestSeriesPeopleAdapter_ListBySeries_NoCredits covers the empty
// person_credits case: canon has a TMDB id but no rows are persisted
// (cold series, never enriched). Empty list, no error.
func TestSeriesPeopleAdapter_ListBySeries_NoCredits(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			pcRepo := enrichpersistence.NewPersonCreditsRepository(db)
			reader := stubCanonReader{
				rows: map[domain.SeriesID]canonView{
					42: {TMDBID: tmdbIDPtr(123456)},
				},
			}
			adapter := newSeriesPeopleFromPersonCreditsForTest(pcRepo, reader)

			cast, err := adapter.ListBySeries(ctx, 42, people.SeriesCreditCast, "en-US")
			require.NoError(t, err)
			assert.Empty(t, cast, "no person_credits rows for tmdb_id ⇒ empty cast")
		})
	}
}

// TestSeriesPeopleAdapter_ListBySeries_CanonGetWrapsGenericError
// proves non-SeriesNotFoundError canon errors get wrapped with the
// "series_people adapter: canon lookup" prefix so log lines have a
// stable identifying tag.
func TestSeriesPeopleAdapter_ListBySeries_CanonGetWrapsGenericError(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			pcRepo := enrichpersistence.NewPersonCreditsRepository(db)

			boom := errors.New("canon read boom")
			reader := stubCanonReader{err: boom}
			adapter := newSeriesPeopleFromPersonCreditsForTest(pcRepo, reader)

			_, err := adapter.ListBySeries(ctx, 42, people.SeriesCreditCast, "en-US")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "series_people adapter")
			assert.True(t, errors.Is(err, boom),
				"wrapped error must keep the underlying chain via errors.Is")
		})
	}
}

// TestSeriesPeopleAdapter_ListBySeries_BatchUpsertIdempotent_NoOrphanBranches
// is the D-0 OnConflict regression: walk the series_worker write path
// end-to-end (BatchUpsert) and confirm a re-batch on Postgres does NOT
// surface the orphan-branch SQLSTATE 42601 the bare-OnConflict pattern
// raised before D-1 — and the adapter still reads the original rows.
//
// Covers the §2 D-0 prod-bug surface for the new series_worker write
// path (mapSeriesCreditsToPersonCredits → PersonCreditsRepository.
// BatchUpsert → adapter.ListBySeries round-trip).
func TestSeriesPeopleAdapter_ListBySeries_BatchUpsertIdempotent_NoOrphanBranches(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()

			peopleRepo := enrichpersistence.NewPeopleRepository(db)
			pcRepo := enrichpersistence.NewPersonCreditsRepository(db)

			const tmdbSeriesID = 100088
			const n = 20
			rows := make([]database.PersonCreditModel, 0, n)
			for i := range n {
				personName := fmt.Sprintf("Cast Member %02d", i)
				personID, err := peopleRepo.Upsert(ctx, people.Person{
					Name:      personName,
					Hydration: people.HydrationStub,
					TMDBID:    tmdbIDPtr(9000 + i),
				})
				require.NoError(t, err)
				row := seedPersonCreditTV(personID,
					fmt.Sprintf("credit-%02d", i),
					"Series Title", tmdbSeriesID)
				rows = append(rows, row)
			}

			ids1, err := pcRepo.BatchUpsert(ctx, rows)
			require.NoError(t, err, "first BatchUpsert must NOT raise SQLSTATE 42601")
			require.Len(t, ids1, n)

			ids2, err := pcRepo.BatchUpsert(ctx, rows)
			require.NoError(t, err, "second BatchUpsert (re-ingest) must NOT raise SQLSTATE 42601")
			require.Equal(t, ids1, ids2,
				"re-batch must round-trip to the same ids via (person_id, tmdb_credit_id)")

			reader := stubCanonReader{
				rows: map[domain.SeriesID]canonView{
					42: {TMDBID: tmdbIDPtr(tmdbSeriesID)},
				},
			}
			adapter := newSeriesPeopleFromPersonCreditsForTest(pcRepo, reader)
			cast, err := adapter.ListBySeries(ctx, 42, people.SeriesCreditCast, "en-US")
			require.NoError(t, err)
			require.Len(t, cast, n,
				"adapter must read back the same N cast rows the write side persisted")
		})
	}
}
