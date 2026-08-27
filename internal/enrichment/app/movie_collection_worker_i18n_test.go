package enrichment

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

// fakeCollectionTexts captures IDByTMDBCollectionID + UpsertCollectionTexts.
type fakeCollectionTexts struct {
	pk       int64
	pkErr    error
	writes   []collTextWrite
	writeErr error
}

type collTextWrite struct {
	collectionID   int64
	language       string
	name, overview string
	poster         *string
}

func (f *fakeCollectionTexts) IDByTMDBCollectionID(_ context.Context, _ int) (int64, error) {
	return f.pk, f.pkErr
}

func (f *fakeCollectionTexts) UpsertCollectionTexts(_ context.Context, collectionID int64, language, name, overview string, poster *string, _ time.Time) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.writes = append(f.writes, collTextWrite{collectionID, language, name, overview, poster})
	return nil
}

// collectionRespWithTranslations carries a base en name/overview + a ru
// translation (data.title = localized name).
func collectionRespWithTranslations(ru bool, ruName string) *tmdb.CollectionResponse {
	r := &tmdb.CollectionResponse{
		ID: 726871, Name: "Dune Collection", Overview: "Epic saga.",
		Translations: &tmdb.MovieTranslations{
			Translations: []tmdb.MovieTranslation{
				{ISO6391: "en", Data: tmdb.MovieTranslationData{Title: "Dune Collection", Overview: "Epic saga."}},
			},
		},
	}
	if ru {
		r.Translations.Translations = append(r.Translations.Translations,
			tmdb.MovieTranslation{ISO6391: "ru", Data: tmdb.MovieTranslationData{Title: ruName, Overview: "Эпическая сага."}})
	}
	return r
}

func TestPopulateCollection_WritesEnAndRuTexts(t *testing.T) {
	ftmdb := &fakeCollectionTMDB{resp: collectionRespWithTranslations(true, "Дюна: Коллекция")}
	texts := &fakeCollectionTexts{pk: 42}

	w, err := NewMovieCollectionWorker(MovieCollectionWorkerDeps{
		TMDB: ftmdb, Collections: &fakeCollectionUpserter{}, Movies: &recordingMovieCanon{},
		Texts: texts,
	})
	require.NoError(t, err)

	require.NoError(t, w.PopulateCollection(context.Background(), 726871))

	require.Len(t, texts.writes, 2, "en-US + ru-RU rows written")
	byLang := map[string]collTextWrite{}
	for _, wr := range texts.writes {
		byLang[wr.language] = wr
		assert.Equal(t, int64(42), wr.collectionID, "resolved local PK used")
	}
	assert.Equal(t, "Dune Collection", byLang["en-US"].name)
	assert.Equal(t, "Epic saga.", byLang["en-US"].overview)
	assert.Equal(t, "Дюна: Коллекция", byLang["ru-RU"].name, "ru name from data.title")
	assert.Equal(t, "Эпическая сага.", byLang["ru-RU"].overview)
}

// Guard (a): TMDB has no ru translation → no ru row (only en-US written).
func TestPopulateCollection_SkipsMissingRuTranslation(t *testing.T) {
	ftmdb := &fakeCollectionTMDB{resp: collectionRespWithTranslations(false, "")}
	texts := &fakeCollectionTexts{pk: 7}

	w, err := NewMovieCollectionWorker(MovieCollectionWorkerDeps{
		TMDB: ftmdb, Collections: &fakeCollectionUpserter{}, Movies: &recordingMovieCanon{}, Texts: texts,
	})
	require.NoError(t, err)
	require.NoError(t, w.PopulateCollection(context.Background(), 726871))

	require.Len(t, texts.writes, 1)
	assert.Equal(t, "en-US", texts.writes[0].language, "no ru row when TMDB lacks the translation")
}

// Guard (b): ru translation present but NAME blank → skipped (no-progress write).
func TestPopulateCollection_SkipsBlankRuName(t *testing.T) {
	ftmdb := &fakeCollectionTMDB{resp: collectionRespWithTranslations(true, "" /* blank ru name */)}
	texts := &fakeCollectionTexts{pk: 9}

	w, err := NewMovieCollectionWorker(MovieCollectionWorkerDeps{
		TMDB: ftmdb, Collections: &fakeCollectionUpserter{}, Movies: &recordingMovieCanon{}, Texts: texts,
	})
	require.NoError(t, err)
	require.NoError(t, w.PopulateCollection(context.Background(), 726871))

	require.Len(t, texts.writes, 1)
	assert.Equal(t, "en-US", texts.writes[0].language, "blank ru name → skipped")
}

// collectionRespWithPosters carries en + ru name translations AND images.posters[]
// tagged en / ru (plus a higher-vote ru to prove the VoteAverage ranking).
func collectionRespWithPosters() *tmdb.CollectionResponse {
	iso := func(s string) *string { return &s }
	return &tmdb.CollectionResponse{
		ID: 726871, Name: "Dune Collection", Overview: "Epic saga.", PosterPath: "/en_root.jpg",
		Translations: &tmdb.MovieTranslations{
			Translations: []tmdb.MovieTranslation{
				{ISO6391: "en", Data: tmdb.MovieTranslationData{Title: "Dune Collection", Overview: "Epic saga."}},
				{ISO6391: "ru", Data: tmdb.MovieTranslationData{Title: "Дюна: Коллекция", Overview: "Эпическая сага."}},
			},
		},
		Images: &tmdb.TVImages{
			Posters: []tmdb.TVImage{
				{FilePath: "/en_poster.jpg", ISO6391: iso("en"), VoteAverage: 7.0, VoteCount: 100},
				{FilePath: "/ru_low.jpg", ISO6391: iso("ru"), VoteAverage: 5.0, VoteCount: 10},
				{FilePath: "/ru_top.jpg", ISO6391: iso("ru"), VoteAverage: 8.0, VoteCount: 50},
				{FilePath: "/agnostic.jpg", ISO6391: nil, VoteAverage: 9.9, VoteCount: 999}, // must NOT win a strict lang tier
			},
		},
	}
}

func TestPopulateCollection_PicksLocalizedPosters(t *testing.T) {
	ftmdb := &fakeCollectionTMDB{resp: collectionRespWithPosters()}
	texts := &fakeCollectionTexts{pk: 42}
	w, err := NewMovieCollectionWorker(MovieCollectionWorkerDeps{
		TMDB: ftmdb, Collections: &fakeCollectionUpserter{}, Movies: &recordingMovieCanon{}, Texts: texts,
	})
	require.NoError(t, err)
	require.NoError(t, w.PopulateCollection(context.Background(), 726871))

	byLang := map[string]collTextWrite{}
	for _, wr := range texts.writes {
		byLang[wr.language] = wr
	}
	require.NotNil(t, byLang["ru-RU"].poster, "ru row must carry a poster")
	assert.Equal(t, "/ru_top.jpg", *byLang["ru-RU"].poster, "highest-vote ru poster wins; agnostic must not cross into the ru tier")
	require.NotNil(t, byLang["en-US"].poster)
	assert.Equal(t, "/en_poster.jpg", *byLang["en-US"].poster, "en tier picks en poster")
}

// No ru-tagged poster → ru row poster nil (reader ladders to canon).
func TestPopulateCollection_NoRuPosterLeavesNil(t *testing.T) {
	resp := collectionRespWithPosters()
	// strip ru posters, keep ru name translation
	resp.Images.Posters = []tmdb.TVImage{
		{FilePath: "/en_poster.jpg", ISO6391: func(s string) *string { return &s }("en"), VoteAverage: 7.0},
	}
	ftmdb := &fakeCollectionTMDB{resp: resp}
	texts := &fakeCollectionTexts{pk: 42}
	w, _ := NewMovieCollectionWorker(MovieCollectionWorkerDeps{
		TMDB: ftmdb, Collections: &fakeCollectionUpserter{}, Movies: &recordingMovieCanon{}, Texts: texts,
	})
	require.NoError(t, w.PopulateCollection(context.Background(), 726871))
	byLang := map[string]collTextWrite{}
	for _, wr := range texts.writes {
		byLang[wr.language] = wr
	}
	assert.Nil(t, byLang["ru-RU"].poster, "no ru-tagged poster → nil (strict, no agnostic/en fallback)")
}

// nil Texts writer → no i18n writes, no panic (pre-S2 behavior preserved).
func TestPopulateCollection_NilTextsWriterSafe(t *testing.T) {
	ftmdb := &fakeCollectionTMDB{resp: collectionRespWithTranslations(true, "Дюна")}
	fups := &fakeCollectionUpserter{}
	w, err := NewMovieCollectionWorker(MovieCollectionWorkerDeps{
		TMDB: ftmdb, Collections: fups, Movies: &recordingMovieCanon{}, // Texts nil
	})
	require.NoError(t, err)
	require.NoError(t, w.PopulateCollection(context.Background(), 726871))
	assert.Equal(t, 1, fups.calls, "canon upsert still happens; no i18n panic")
}
