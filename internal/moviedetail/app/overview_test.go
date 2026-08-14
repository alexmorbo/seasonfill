package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeOverviewCanon struct {
	canon movie.Canon
	err   error
}

func (f fakeOverviewCanon) GetByTMDBID(_ context.Context, _ domain.TMDBID) (movie.Canon, error) {
	return f.canon, f.err
}

type fakeOverviewI18n struct {
	row enrichpersistence.MovieI18nRow
	err error
}

func (f fakeOverviewI18n) Get(_ context.Context, _ domain.MovieID, _ string) (enrichpersistence.MovieI18nRow, error) {
	return f.row, f.err
}

type fakeOverviewTitleLang struct {
	lang string
	err  error
}

func (f fakeOverviewTitleLang) TitleLanguage(_ context.Context, _ domain.MovieID, _ string) (string, error) {
	return f.lang, f.err
}

// Empty ru-RU overview → the ladder (I18nReader.Get) already resolved
// overview to the en-US value; title stayed ru-RU. served_language reports the
// title language (ru-RU) so no missing_lang when ru-RU was requested.
func TestOverviewUseCase_Get_HappyPath_RuTitleEnOverview(t *testing.T) {
	tid := domain.TMDBID(603)
	canon := fakeOverviewCanon{canon: movie.Canon{ID: 7, TMDBID: &tid, Title: "Canon Title"}}
	i18n := fakeOverviewI18n{row: enrichpersistence.MovieI18nRow{
		Title:    new("Дюна"),        // ru-RU title wins
		Overview: new("en overview"), // en-US overview via the per-field ladder
		Tagline:  new("en tagline"),
	}}
	uc := mdapp.NewOverviewUseCase(canon, i18n, fakeOverviewTitleLang{lang: "ru-RU"})

	page, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, "Дюна", page.Title, "localized ru-RU title wins")
	require.NotNil(t, page.Overview)
	assert.Equal(t, "en overview", *page.Overview, "en-US overview via ladder")
	require.NotNil(t, page.Tagline)
	assert.Equal(t, "en tagline", *page.Tagline)
	assert.Equal(t, "ru-RU", page.ServedLanguage)
	assert.Empty(t, page.Degraded, "served==requested → no missing_lang")
}

// Requested ru-RU but the title resolved to en-US → missing_lang.
func TestOverviewUseCase_Get_MissingLang(t *testing.T) {
	tid := domain.TMDBID(603)
	canon := fakeOverviewCanon{canon: movie.Canon{ID: 7, TMDBID: &tid, Title: "Canon Title"}}
	i18n := fakeOverviewI18n{row: enrichpersistence.MovieI18nRow{
		Title:    new("The Matrix"),
		Overview: new("en overview"),
	}}
	uc := mdapp.NewOverviewUseCase(canon, i18n, fakeOverviewTitleLang{lang: "en-US"})

	page, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, "The Matrix", page.Title)
	assert.Equal(t, "en-US", page.ServedLanguage)
	assert.Equal(t, []string{"missing_lang"}, page.Degraded)
}

// Empty-string localized overview/tagline must NOT shadow (stay nil). Guards the
// non-empty check even if a reader ever surfaced an empty string.
func TestOverviewUseCase_Get_EmptyFieldsDoNotShadow(t *testing.T) {
	tid := domain.TMDBID(603)
	canon := fakeOverviewCanon{canon: movie.Canon{ID: 7, TMDBID: &tid, Title: "Canon Title"}}
	empty := ""
	i18n := fakeOverviewI18n{row: enrichpersistence.MovieI18nRow{
		Title:    new("Дюна"),
		Overview: &empty,
		Tagline:  &empty,
	}}
	uc := mdapp.NewOverviewUseCase(canon, i18n, fakeOverviewTitleLang{lang: "ru-RU"})

	page, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, "Дюна", page.Title)
	assert.Nil(t, page.Overview, "empty overview must not be surfaced")
	assert.Nil(t, page.Tagline, "empty tagline must not be surfaced")
}

// No localized row at all → canon title, nil overview/tagline, served "" (nil
// titleLang path), no degraded.
func TestOverviewUseCase_Get_NoI18nRow(t *testing.T) {
	tid := domain.TMDBID(603)
	canon := fakeOverviewCanon{canon: movie.Canon{ID: 7, TMDBID: &tid, Title: "Canon Title"}}
	uc := mdapp.NewOverviewUseCase(canon, fakeOverviewI18n{err: ports.ErrNotFound}, nil)

	page, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, "Canon Title", page.Title)
	assert.Nil(t, page.Overview)
	assert.Empty(t, page.ServedLanguage)
	assert.Empty(t, page.Degraded)
}

func TestOverviewUseCase_Get_NotFoundBubbles(t *testing.T) {
	canon := fakeOverviewCanon{err: ports.ErrNotFound}
	uc := mdapp.NewOverviewUseCase(canon, fakeOverviewI18n{}, nil)

	_, err := uc.Get(context.Background(), domain.TMDBID(1), "")
	require.ErrorIs(t, err, ports.ErrNotFound)
}
