package enrichment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeMovieTMDB struct {
	resp    *tmdb.MovieResponse
	err     error
	calls   int
	gotID   int64
	gotLang string
}

func (f *fakeMovieTMDB) GetMovie(_ context.Context, id int64, language string) (*tmdb.MovieResponse, error) {
	f.calls++
	f.gotID = id
	f.gotLang = language
	return f.resp, f.err
}

type fakeMovieCanon struct {
	getResp     movie.Canon
	getErr      error
	upsertCalls int
	upserted    movie.Canon
	markCalls   int
	markedID    domain.MovieID
	// Ф1.1a cast stamp capture.
	castMarkCalls int
	castMarkedID  domain.MovieID
	// Ф1.1b keyword stamp capture.
	keywordsMarkCalls int
	keywordsMarkedID  domain.MovieID
	// Ф1.1c media/recs capture.
	stubUpserts    []movie.Canon
	stubNextID     domain.MovieID
	stubIDByTMDB   map[int64]domain.MovieID // optional UpsertStub id override (self-ref test)
	mediaMarkCalls int
	recsMarkCalls  int
	// S3 text stamp capture.
	textMarkCalls int
	textMarkedID  domain.MovieID
}

func (f *fakeMovieCanon) UpsertStub(_ context.Context, c movie.Canon) (domain.MovieID, error) {
	f.stubUpserts = append(f.stubUpserts, c)
	if f.stubIDByTMDB != nil && c.TMDBID != nil {
		if id, ok := f.stubIDByTMDB[int64(*c.TMDBID)]; ok {
			return id, nil
		}
	}
	f.stubNextID++
	return f.stubNextID, nil
}

func (f *fakeMovieCanon) MarkMediaSynced(_ context.Context, _ domain.MovieID, _ time.Time) error {
	f.mediaMarkCalls++
	return nil
}

func (f *fakeMovieCanon) MarkRecsSynced(_ context.Context, _ domain.MovieID, _ time.Time) error {
	f.recsMarkCalls++
	return nil
}

func (f *fakeMovieCanon) Get(_ context.Context, _ domain.MovieID) (movie.Canon, error) {
	return f.getResp, f.getErr
}

func (f *fakeMovieCanon) Upsert(_ context.Context, c movie.Canon) (domain.MovieID, error) {
	f.upsertCalls++
	f.upserted = c
	return c.ID, nil
}

func (f *fakeMovieCanon) MarkTMDBSynced(_ context.Context, id domain.MovieID, _ time.Time) error {
	f.markCalls++
	f.markedID = id
	return nil
}

func (f *fakeMovieCanon) MarkCastSynced(_ context.Context, id domain.MovieID, _ time.Time) error {
	f.castMarkCalls++
	f.castMarkedID = id
	return nil
}

func (f *fakeMovieCanon) MarkKeywordsSynced(_ context.Context, id domain.MovieID, _ time.Time) error {
	f.keywordsMarkCalls++
	f.keywordsMarkedID = id
	return nil
}

func (f *fakeMovieCanon) MarkTextSynced(_ context.Context, id domain.MovieID, _ time.Time) error {
	f.textMarkCalls++
	f.textMarkedID = id
	return nil
}

type movieI18nWrite struct {
	movieID  domain.MovieID
	lang     string
	title    string
	overview string
	tagline  string
	poster   *string
	backdrop *string
}

type fakeMovieI18n struct {
	calls    int
	movieID  domain.MovieID
	lang     string
	title    string
	overview string
	tagline  string
	writes   []movieI18nWrite
}

func (f *fakeMovieI18n) UpsertEnriched(_ context.Context, movieID domain.MovieID, lang, title, overview, tagline string, poster, backdrop *string, _ time.Time) error {
	f.calls++
	f.movieID = movieID
	f.lang = lang
	f.title = title
	f.overview = overview
	f.tagline = tagline
	f.writes = append(f.writes, movieI18nWrite{movieID, lang, title, overview, tagline, poster, backdrop})
	return nil
}

func movieCanonWithTMDB(id domain.MovieID, tmdbID int) movie.Canon {
	tid := domain.TMDBID(tmdbID)
	return movie.Canon{ID: id, TMDBID: &tid, Title: "stub", Hydration: movie.HydrationStub}
}

// TestMovieWorker_HandleForced_HydratesStubToFull asserts the worker fetches
// with the base language, upserts a FULL canon targeting the existing row,
// writes the localized i18n row under the base lang, and stamps MarkTMDBSynced —
// and that the mapped canon leaves OMDb-owned columns nil (COALESCE preserve).
func TestMovieWorker_HandleForced_HydratesStubToFull(t *testing.T) {
	tmdbClient := &fakeMovieTMDB{resp: &tmdb.MovieResponse{
		ID:            693134,
		IMDBID:        "tt15239678",
		Title:         "Dune: Part Two",
		OriginalTitle: "Dune: Part Two",
		Overview:      "overview text",
		Tagline:       "tagline text",
		Status:        "Released",
		ReleaseDate:   "2024-02-27",
		VoteAverage:   8.2,
		PosterPath:    "/p.jpg",
	}}
	canonRepo := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 693134)}
	i18n := &fakeMovieI18n{}

	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:   tmdbClient,
		Movies: canonRepo,
		I18n:   i18n,
		Clock:  func() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) },
	})
	require.NoError(t, err)

	require.NoError(t, w.HandleForced(context.Background(), 7))

	// language-aware fetch (#1184): default base lang en-US, keyed by tmdb_id.
	assert.Equal(t, 1, tmdbClient.calls)
	assert.Equal(t, int64(693134), tmdbClient.gotID)
	assert.Equal(t, tmdb.DefaultLanguage, tmdbClient.gotLang)

	// canon upsert: full hydration, targets the existing row by PK, real title.
	require.Equal(t, 1, canonRepo.upsertCalls)
	assert.Equal(t, movie.HydrationFull, canonRepo.upserted.Hydration)
	assert.Equal(t, domain.MovieID(7), canonRepo.upserted.ID)
	assert.Equal(t, "Dune: Part Two", canonRepo.upserted.Title)
	// OMDb-owned columns MUST be nil so the COALESCE Upsert preserves them.
	assert.Nil(t, canonRepo.upserted.IMDBRating)
	assert.Nil(t, canonRepo.upserted.OMDBRated)
	// tmdb_changed_at MUST be nil (sole-writer is the changes marker).
	assert.Nil(t, canonRepo.upserted.TMDBChangedAt)

	// localized i18n row under the base lang.
	require.Equal(t, 1, i18n.calls)
	assert.Equal(t, domain.MovieID(7), i18n.movieID)
	assert.Equal(t, tmdb.DefaultLanguage, i18n.lang)
	assert.Equal(t, "Dune: Part Two", i18n.title)
	assert.Equal(t, "overview text", i18n.overview)
	assert.Equal(t, "tagline text", i18n.tagline)

	// freshness stamp.
	require.Equal(t, 1, canonRepo.markCalls)
	assert.Equal(t, domain.MovieID(7), canonRepo.markedID)

	// S3: text-synced stamped on the hydration attempt.
	assert.Equal(t, 1, canonRepo.textMarkCalls)
	assert.Equal(t, domain.MovieID(7), canonRepo.textMarkedID)
}

// TestMovieWorker_HandleForced_NilTMDBSkips asserts a tmdb-less movie is a clean
// skip (no fetch, no upsert, no stamp, no error).
func TestMovieWorker_HandleForced_NilTMDBSkips(t *testing.T) {
	tmdbClient := &fakeMovieTMDB{}
	canonRepo := &fakeMovieCanon{getResp: movie.Canon{ID: 9, Title: "orphan", Hydration: movie.HydrationStub}}

	w, err := NewMovieWorker(MovieWorkerDeps{TMDB: tmdbClient, Movies: canonRepo})
	require.NoError(t, err)

	require.NoError(t, w.HandleForced(context.Background(), 9))
	assert.Equal(t, 0, tmdbClient.calls, "no TMDB fetch for a tmdb-less movie")
	assert.Equal(t, 0, canonRepo.upsertCalls)
	assert.Equal(t, 0, canonRepo.markCalls)
}

// TestMovieWorker_HandleForced_FetchErrorNoStamp asserts a GetMovie error
// bubbles up and the freshness stamp is NOT written (so the movie re-picks).
func TestMovieWorker_HandleForced_FetchErrorNoStamp(t *testing.T) {
	tmdbClient := &fakeMovieTMDB{err: errors.New("boom")}
	canonRepo := &fakeMovieCanon{getResp: movieCanonWithTMDB(3, 111)}

	w, err := NewMovieWorker(MovieWorkerDeps{TMDB: tmdbClient, Movies: canonRepo})
	require.NoError(t, err)

	err = w.HandleForced(context.Background(), 3)
	require.Error(t, err)
	assert.Equal(t, 0, canonRepo.upsertCalls)
	assert.Equal(t, 0, canonRepo.markCalls, "no stamp on fetch failure")
}

// TestNewMovieWorker_RequiresPorts asserts the constructor rejects missing deps.
func TestNewMovieWorker_RequiresPorts(t *testing.T) {
	_, err := NewMovieWorker(MovieWorkerDeps{Movies: &fakeMovieCanon{}})
	require.Error(t, err)
	_, err = NewMovieWorker(MovieWorkerDeps{TMDB: &fakeMovieTMDB{}})
	require.Error(t, err)
}

// TestMovieWorker_HandleForced_WritesAllConfiguredLanguages asserts the worker
// fans out over locale.SupportedUserLanguages: the base row from the response
// root, and every other language from resp.Translations — from ONE GetMovie
// fetch. A language absent from Translations is skipped (no empty row). Every
// written row carries the language-independent canon poster/backdrop.
func TestMovieWorker_HandleForced_WritesAllConfiguredLanguages(t *testing.T) {
	poster := "/canon_poster.jpg"
	backdrop := "/canon_backdrop.jpg"

	cases := []struct {
		name         string
		translations *tmdb.MovieTranslations
		wantLangs    []string          // langs expected to be written, in order
		wantTitles   map[string]string // lang -> expected title
	}{
		{
			name: "ru translation present writes en-US and ru-RU",
			translations: &tmdb.MovieTranslations{Translations: []tmdb.MovieTranslation{
				{ISO6391: "en", Data: tmdb.MovieTranslationData{Title: "Dune: Part Two", Overview: "en ov", Tagline: "en tag"}},
				{ISO6391: "ru", Data: tmdb.MovieTranslationData{Title: "Дюна: Часть вторая", Overview: "ру описание", Tagline: "ру слоган"}},
			}},
			wantLangs: []string{"en-US", "ru-RU"},
			wantTitles: map[string]string{
				"en-US": "Dune: Part Two",     // base = response root
				"ru-RU": "Дюна: Часть вторая", // from translations
			},
		},
		{
			name:         "no ru translation writes en-US only",
			translations: &tmdb.MovieTranslations{Translations: []tmdb.MovieTranslation{}},
			wantLangs:    []string{"en-US"},
			wantTitles:   map[string]string{"en-US": "Dune: Part Two"},
		},
		{
			name: "ru translation present but blank title skips the ru write (S-HEAL-FIX A)",
			translations: &tmdb.MovieTranslations{Translations: []tmdb.MovieTranslation{
				{ISO6391: "en", Data: tmdb.MovieTranslationData{Title: "Dune: Part Two", Overview: "en ov", Tagline: "en tag"}},
				// TMDB gave a ru overview but a BLANK ru title — the no-progress case.
				{ISO6391: "ru", Data: tmdb.MovieTranslationData{Title: "", Overview: "ру описание", Tagline: "ру слоган"}},
			}},
			wantLangs:  []string{"en-US"}, // ru row skipped: no title-less UpsertEnriched
			wantTitles: map[string]string{"en-US": "Dune: Part Two"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmdbClient := &fakeMovieTMDB{resp: &tmdb.MovieResponse{
				ID:           693134,
				Title:        "Dune: Part Two", // response root = base lang (en-US)
				Overview:     "en ov",
				Tagline:      "en tag",
				PosterPath:   poster,
				BackdropPath: backdrop,
				Translations: tc.translations,
			}}
			canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(42, 693134)}
			i18n := &fakeMovieI18n{}
			w, err := NewMovieWorker(MovieWorkerDeps{TMDB: tmdbClient, Movies: canon, I18n: i18n})
			require.NoError(t, err)

			require.NoError(t, w.HandleForced(context.Background(), 42))

			// exactly ONE TMDB fetch regardless of language count.
			assert.Equal(t, 1, tmdbClient.calls, "one GetMovie fetch, translations reused")

			gotLangs := make([]string, 0, len(i18n.writes))
			for _, wr := range i18n.writes {
				gotLangs = append(gotLangs, wr.lang)
				// every row carries the canon poster/backdrop (poster-bearing).
				require.NotNil(t, wr.poster)
				assert.NotEmpty(t, *wr.poster)
			}
			assert.Equal(t, tc.wantLangs, gotLangs)

			for _, wr := range i18n.writes {
				if want, ok := tc.wantTitles[wr.lang]; ok {
					assert.Equal(t, want, wr.title, "title for %s", wr.lang)
				}
			}

			// S3 anti-storm: text-synced is stamped ONCE per attempt regardless of
			// whether TMDB carried a non-base (ru) translation — so a movie with no
			// ru overview is re-picked once, not forever.
			assert.Equal(t, 1, canon.textMarkCalls,
				"MarkTextSynced must fire on every hydration attempt, incl. no-ru")
			assert.Equal(t, domain.MovieID(42), canon.textMarkedID)
		})
	}
}

// TestMovieWorker_HandleForced_TitlelessRuSkipsUpsertKeepsTextStamp is the S-HEAL-FIX
// writer A assertion in isolation: a ru translation with a BLANK title must NOT trigger
// UpsertEnriched for ru-RU (so movie_i18n.enriched_at is not advanced on a no-title
// write), while MarkTextSynced STILL fires (the attempt clock advances every attempt).
func TestMovieWorker_HandleForced_TitlelessRuSkipsUpsertKeepsTextStamp(t *testing.T) {
	tmdbClient := &fakeMovieTMDB{resp: &tmdb.MovieResponse{
		ID:           693134,
		Title:        "Dune: Part Two", // en-US root
		Overview:     "en ov",
		Tagline:      "en tag",
		PosterPath:   "/p.jpg",
		BackdropPath: "/b.jpg",
		Translations: &tmdb.MovieTranslations{Translations: []tmdb.MovieTranslation{
			{ISO6391: "en", Data: tmdb.MovieTranslationData{Title: "Dune: Part Two", Overview: "en ov", Tagline: "en tag"}},
			{ISO6391: "ru", Data: tmdb.MovieTranslationData{Title: "", Overview: "ру описание", Tagline: "ру слоган"}},
		}},
	}}
	canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(42, 693134)}
	i18n := &fakeMovieI18n{}
	w, err := NewMovieWorker(MovieWorkerDeps{TMDB: tmdbClient, Movies: canon, I18n: i18n})
	require.NoError(t, err)

	require.NoError(t, w.HandleForced(context.Background(), 42))

	// Only the en-US base row is written; the blank-title ru row is skipped.
	require.Equal(t, 1, i18n.calls, "exactly one i18n write (en-US); blank ru title skipped")
	langs := make([]string, 0, len(i18n.writes))
	for _, wr := range i18n.writes {
		langs = append(langs, wr.lang)
	}
	assert.Equal(t, []string{"en-US"}, langs, "ru-RU must NOT be written on a blank title")

	// Attempt clock STILL advances: MarkTextSynced fired once for this attempt.
	assert.Equal(t, 1, canon.textMarkCalls, "MarkTextSynced must fire even when ru is skipped")
	assert.Equal(t, domain.MovieID(42), canon.textMarkedID)
}
