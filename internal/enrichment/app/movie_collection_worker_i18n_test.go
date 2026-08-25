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
}

func (f *fakeCollectionTexts) IDByTMDBCollectionID(_ context.Context, _ int) (int64, error) {
	return f.pk, f.pkErr
}

func (f *fakeCollectionTexts) UpsertCollectionTexts(_ context.Context, collectionID int64, language, name, overview string, _ time.Time) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.writes = append(f.writes, collTextWrite{collectionID, language, name, overview})
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
