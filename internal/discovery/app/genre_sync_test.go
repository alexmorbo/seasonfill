package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// stubLister returns a pre-baked list per language; tracks call count
// so the test can assert the loop visits every supplied language.
type stubLister struct {
	byLang map[string]*GenreListResult
	calls  int
	failOn map[string]error
}

func (s *stubLister) GenreListTV(_ context.Context, lang string) (*GenreListResult, error) {
	s.calls++
	if err, ok := s.failOn[lang]; ok {
		return nil, err
	}
	r, ok := s.byLang[lang]
	if !ok {
		return &GenreListResult{}, nil
	}
	return r, nil
}

func TestGenreSyncer_Tick_WritesAllLanguages(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			genres := enrichpersistence.NewGenresRepository(db)
			i18n := enrichpersistence.NewGenresI18nRepository(db)

			lister := &stubLister{
				byLang: map[string]*GenreListResult{
					"en-US": {Items: []GenreListItem{
						{ID: 18, Name: "Drama"}, {ID: 35, Name: "Comedy"},
					}},
					"ru-RU": {Items: []GenreListItem{
						{ID: 18, Name: "драма"}, {ID: 35, Name: "комедия"},
					}},
				},
			}
			s := NewGenreSyncer(GenreSyncerDeps{
				TMDB: lister, Genres: genres, I18n: i18n,
				Languages: []string{"en-US", "ru-RU"},
			})
			require.NoError(t, s.Tick(context.Background()))
			assert.Equal(t, 2, lister.calls)

			tmdb18 := domain.TMDBID(18)
			g, err := genres.GetByTMDBID(context.Background(), tmdb18)
			require.NoError(t, err)
			en, err := i18n.Get(context.Background(), g.ID, "en-US")
			require.NoError(t, err)
			assert.Equal(t, "Drama", en.Name)
			ru, err := i18n.Get(context.Background(), g.ID, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, "драма", ru.Name)
		})
	}
}

func TestGenreSyncer_Tick_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			genres := enrichpersistence.NewGenresRepository(db)
			i18n := enrichpersistence.NewGenresI18nRepository(db)
			lister := &stubLister{byLang: map[string]*GenreListResult{
				"en-US": {Items: []GenreListItem{{ID: 18, Name: "Drama"}}},
			}}
			s := NewGenreSyncer(GenreSyncerDeps{
				TMDB: lister, Genres: genres, I18n: i18n,
				Languages: []string{"en-US"},
			})
			require.NoError(t, s.Tick(context.Background()))
			require.NoError(t, s.Tick(context.Background()))

			tmdb18 := domain.TMDBID(18)
			g, err := genres.GetByTMDBID(context.Background(), tmdb18)
			require.NoError(t, err)
			en, err := i18n.Get(context.Background(), g.ID, "en-US")
			require.NoError(t, err)
			assert.Equal(t, "Drama", en.Name)
		})
	}
}

func TestGenreSyncer_Tick_LanguageFailure_DoesNotBlockSiblings(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			genres := enrichpersistence.NewGenresRepository(db)
			i18n := enrichpersistence.NewGenresI18nRepository(db)
			lister := &stubLister{
				byLang: map[string]*GenreListResult{
					"en-US": {Items: []GenreListItem{{ID: 35, Name: "Comedy"}}},
				},
				failOn: map[string]error{"ru-RU": errors.New("upstream 503")},
			}
			s := NewGenreSyncer(GenreSyncerDeps{
				TMDB: lister, Genres: genres, I18n: i18n,
				Languages: []string{"en-US", "ru-RU"},
			})
			err := s.Tick(context.Background())
			require.Error(t, err, "must surface ru-RU failure")
			tmdb35 := domain.TMDBID(35)
			g, gErr := genres.GetByTMDBID(context.Background(), tmdb35)
			require.NoError(t, gErr)
			en, eErr := i18n.Get(context.Background(), g.ID, "en-US")
			require.NoError(t, eErr)
			assert.Equal(t, "Comedy", en.Name)
		})
	}
}

func TestGenreSyncer_Tick_EmptyLanguages_Errors(t *testing.T) {
	t.Parallel()
	s := NewGenreSyncer(GenreSyncerDeps{
		TMDB: &stubLister{}, Languages: nil,
	})
	err := s.Tick(context.Background())
	require.Error(t, err)
}

func TestGenreSyncer_Tick_SkipsInvalidRows(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			genres := enrichpersistence.NewGenresRepository(db)
			i18n := enrichpersistence.NewGenresI18nRepository(db)
			lister := &stubLister{byLang: map[string]*GenreListResult{
				"en-US": {Items: []GenreListItem{
					{ID: 0, Name: "ZeroID"},
					{ID: 35, Name: ""},
					{ID: 18, Name: "Drama"},
				}},
			}}
			s := NewGenreSyncer(GenreSyncerDeps{
				TMDB: lister, Genres: genres, I18n: i18n,
				Languages: []string{"en-US"},
			})
			require.NoError(t, s.Tick(context.Background()))
			tmdb18 := domain.TMDBID(18)
			g, err := genres.GetByTMDBID(context.Background(), tmdb18)
			require.NoError(t, err)
			en, err := i18n.Get(context.Background(), g.ID, "en-US")
			require.NoError(t, err)
			assert.Equal(t, "Drama", en.Name)
			_ = taxonomy.GenreI18n{}
		})
	}
}
