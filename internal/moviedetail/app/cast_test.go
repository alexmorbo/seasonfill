package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeCastCanon struct {
	canon movie.Canon
	err   error
}

func (f fakeCastCanon) GetByTMDBID(_ context.Context, _ domain.TMDBID) (movie.Canon, error) {
	return f.canon, f.err
}

type fakeCastRows struct {
	rows    []enrichpersistence.PersonCredit
	err     error
	gotType string
	gotID   int
	gotLang string
}

func (f *fakeCastRows) ListByMediaWithTextFallback(_ context.Context, mediaType string, tmdbMediaID int, lang string) ([]enrichpersistence.PersonCredit, error) {
	f.gotType, f.gotID, f.gotLang = mediaType, tmdbMediaID, lang
	return f.rows, f.err
}

type fakeCastPeople struct {
	rows map[int64]people.Person
	err  error
}

func (f fakeCastPeople) ListByIDsWithNameFallback(_ context.Context, ids []int64, _ string) ([]people.Person, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]people.Person, 0, len(ids))
	for _, id := range ids {
		if p, ok := f.rows[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

type fakeTitleLang struct {
	lang string
	err  error
}

func (f fakeTitleLang) TitleLanguage(_ context.Context, _ domain.MovieID, _ string) (string, error) {
	return f.lang, f.err
}

func creditRow(personID int64, kind string, character *string, order *int) enrichpersistence.PersonCredit {
	return database.PersonCreditModel{
		PersonID:      personID,
		TMDBCreditID:  "c" + string(rune('0'+personID)),
		MediaType:     "movie",
		TMDBMediaID:   603,
		Title:         "The Matrix",
		Kind:          kind,
		CharacterName: character,
		CreditOrder:   order,
	}
}

func TestCastUseCase_Get_HappyPath(t *testing.T) {
	tid := domain.TMDBID(603)
	canon := fakeCastCanon{canon: movie.Canon{ID: 7, TMDBID: &tid}}
	rows := &fakeCastRows{rows: []enrichpersistence.PersonCredit{
		creditRow(1, "cast", new("Neo"), new(0)),
		creditRow(2, "crew", nil, nil), // crew filtered out
		creditRow(3, "cast", new("Trinity"), new(1)),
	}}
	ppl := fakeCastPeople{rows: map[int64]people.Person{
		1: {ID: 1, Name: "Keanu Reeves", ProfileAsset: new("/k.jpg")},
		3: {ID: 3, Name: "Carrie-Anne Moss"},
	}}
	uc := mdapp.NewCastUseCase(canon, rows, ppl, fakeTitleLang{lang: "en-US"})

	page, err := uc.Get(context.Background(), tid, "en-US")
	require.NoError(t, err)
	require.Len(t, page.Cast, 2, "crew row filtered out")
	assert.Equal(t, "movie", rows.gotType)
	assert.Equal(t, 603, rows.gotID)
	assert.Equal(t, "Keanu Reeves", page.Cast[0].Person.Name)
	assert.Equal(t, "Neo", *page.Cast[0].CharacterName)
	assert.Empty(t, page.Degraded, "served==requested → no missing_lang")
	assert.Equal(t, "en-US", page.ServedLanguage)
}

func TestCastUseCase_Get_MissingLang(t *testing.T) {
	tid := domain.TMDBID(603)
	canon := fakeCastCanon{canon: movie.Canon{ID: 7, TMDBID: &tid}}
	rows := &fakeCastRows{rows: []enrichpersistence.PersonCredit{creditRow(1, "cast", new("Neo"), new(0))}}
	ppl := fakeCastPeople{rows: map[int64]people.Person{1: {ID: 1, Name: "Keanu Reeves"}}}
	// Requested ru-RU but the title resolved to en-US → missing_lang.
	uc := mdapp.NewCastUseCase(canon, rows, ppl, fakeTitleLang{lang: "en-US"})

	page, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, "en-US", page.ServedLanguage)
	assert.Equal(t, []string{"missing_lang"}, page.Degraded)
}

func TestCastUseCase_Get_NilTitleLang(t *testing.T) {
	tid := domain.TMDBID(603)
	canon := fakeCastCanon{canon: movie.Canon{ID: 7, TMDBID: &tid}}
	rows := &fakeCastRows{}
	uc := mdapp.NewCastUseCase(canon, rows, fakeCastPeople{}, nil)

	page, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err)
	assert.Empty(t, page.ServedLanguage)
	assert.Empty(t, page.Degraded)
	assert.Empty(t, page.Cast)
}

func TestCastUseCase_Get_NotFoundBubbles(t *testing.T) {
	canon := fakeCastCanon{err: ports.ErrNotFound}
	uc := mdapp.NewCastUseCase(canon, &fakeCastRows{}, fakeCastPeople{}, nil)

	_, err := uc.Get(context.Background(), domain.TMDBID(1), "")
	require.ErrorIs(t, err, ports.ErrNotFound)
}
