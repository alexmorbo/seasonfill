package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeCanon struct {
	canon movie.Canon
	err   error
}

func (f fakeCanon) GetByTMDBID(_ context.Context, _ domain.TMDBID) (movie.Canon, error) {
	return f.canon, f.err
}

type fakeI18n struct {
	row enrichpersistence.MovieI18nRow
	err error
}

func (f fakeI18n) Get(_ context.Context, _ domain.MovieID, _ string) (enrichpersistence.MovieI18nRow, error) {
	return f.row, f.err
}

type fakeCollection struct {
	col movie.CollectionCanon
	err error
}

func (f fakeCollection) GetByTMDBCollectionID(_ context.Context, _ int) (movie.CollectionCanon, error) {
	return f.col, f.err
}

type fakeMembership struct {
	states []movie.StateEntry
	err    error
}

func (f fakeMembership) ListActiveByMovieID(_ context.Context, _ domain.MovieID) ([]movie.StateEntry, error) {
	return f.states, f.err
}

func TestUseCase_Get_HappyPath(t *testing.T) {
	t.Parallel()
	collID := 726871
	canon := movie.Canon{
		ID:           domain.MovieID(42),
		Title:        "Dune: Part Two",
		CollectionID: &collID,
		PosterAsset:  new("/canon-poster.jpg"),
	}
	avail := "released"
	quality, resolution := "Bluray-1080p", 1080
	video, audio := "x265", "EAC3"
	uc := New(
		fakeCanon{canon: canon},
		fakeI18n{row: enrichpersistence.MovieI18nRow{Title: new("Дюна: Часть вторая"), Overview: new("обзор"), Poster: new("/ru-poster.jpg")}},
		fakeCollection{col: movie.CollectionCanon{TMDBCollectionID: collID, Name: "Dune Collection", RadarrMonitored: true}},
		fakeMembership{states: []movie.StateEntry{
			{InstanceName: "radarr-alpha", RadarrMovieID: 7, MovieID: 42, Monitored: true, HasFile: true,
				Availability: &avail, SizeOnDiskBytes: 99, Quality: &quality, Resolution: &resolution,
				VideoCodec: &video, AudioCodec: &audio},
			{InstanceName: "radarr-beta", RadarrMovieID: 8, MovieID: 42},
		}},
	)

	d, err := uc.Get(context.Background(), domain.TMDBID(693134), "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, "Дюна: Часть вторая", d.Title, "localized title wins over canon")
	require.NotNil(t, d.Overview)
	assert.Equal(t, "обзор", *d.Overview)
	require.NotNil(t, d.Poster)
	assert.Equal(t, "/ru-poster.jpg", *d.Poster, "localized poster wins over canon")
	require.NotNil(t, d.Collection)
	assert.Equal(t, "Dune Collection", d.Collection.Name)
	require.Len(t, d.Library, 2)
	assert.Equal(t, "radarr-alpha", d.Library[0].InstanceName)
	assert.Equal(t, int64(99), d.Library[0].SizeOnDisk)
	require.NotNil(t, d.Library[0].Quality)
	assert.Equal(t, "Bluray-1080p", *d.Library[0].Quality)
	require.NotNil(t, d.Library[0].Resolution)
	assert.Equal(t, 1080, *d.Library[0].Resolution)
	require.NotNil(t, d.Library[0].VideoCodec)
	assert.Equal(t, "x265", *d.Library[0].VideoCodec)
	require.NotNil(t, d.Library[0].AudioCodec)
	assert.Equal(t, "EAC3", *d.Library[0].AudioCodec)
	// radarr-beta has no file → the quality/codec facts stay nil.
	assert.Nil(t, d.Library[1].Quality)
	assert.Nil(t, d.Library[1].Resolution)
	assert.Nil(t, d.Library[1].VideoCodec)
	assert.Nil(t, d.Library[1].AudioCodec)
	assert.Empty(t, d.Degraded)
}

func TestUseCase_Get_I18nMissDegrades(t *testing.T) {
	t.Parallel()
	canon := movie.Canon{ID: domain.MovieID(42), Title: "Canon Title", PosterAsset: new("/canon.jpg")}
	uc := New(
		fakeCanon{canon: canon},
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{},
		fakeMembership{},
	)

	d, err := uc.Get(context.Background(), domain.TMDBID(1), "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, "Canon Title", d.Title, "falls back to canon title")
	require.NotNil(t, d.Poster)
	assert.Equal(t, "/canon.jpg", *d.Poster)
	assert.Nil(t, d.Overview)
	assert.Equal(t, []string{"movie_i18n"}, d.Degraded)
}

func TestUseCase_Get_CanonMissBubblesNotFound(t *testing.T) {
	t.Parallel()
	uc := New(
		fakeCanon{err: ports.ErrNotFound},
		fakeI18n{},
		fakeCollection{},
		fakeMembership{},
	)

	_, err := uc.Get(context.Background(), domain.TMDBID(1), "ru-RU")
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestUseCase_Get_NoCollectionID(t *testing.T) {
	t.Parallel()
	canon := movie.Canon{ID: domain.MovieID(42), Title: "No Collection"} // CollectionID nil
	uc := New(
		fakeCanon{canon: canon},
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{err: errors.New("must not be called")},
		fakeMembership{},
	)

	d, err := uc.Get(context.Background(), domain.TMDBID(1), "ru-RU")
	require.NoError(t, err)
	assert.Nil(t, d.Collection, "no collection_id → nil collection, reader not consulted")
}

func TestUseCase_Get_CollectionMissTolerated(t *testing.T) {
	t.Parallel()
	collID := 1
	canon := movie.Canon{ID: domain.MovieID(42), Title: "Orphan", CollectionID: &collID}
	uc := New(
		fakeCanon{canon: canon},
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{err: ports.ErrNotFound},
		fakeMembership{},
	)

	d, err := uc.Get(context.Background(), domain.TMDBID(1), "ru-RU")
	require.NoError(t, err)
	assert.Nil(t, d.Collection, "collection NotFound is tolerated, not fatal")
}

func TestUseCase_Get_MembershipErrorFatal(t *testing.T) {
	t.Parallel()
	canon := movie.Canon{ID: domain.MovieID(42), Title: "X"}
	uc := New(
		fakeCanon{canon: canon},
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{},
		fakeMembership{err: errors.New("db down")},
	)

	_, err := uc.Get(context.Background(), domain.TMDBID(1), "ru-RU")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ports.ErrNotFound)
}

func TestUseCase_Get_EmptyLangSkipsI18n(t *testing.T) {
	t.Parallel()
	canon := movie.Canon{ID: domain.MovieID(42), Title: "Canon"}
	uc := New(
		fakeCanon{canon: canon},
		fakeI18n{err: errors.New("must not be called")},
		fakeCollection{},
		fakeMembership{},
	)

	d, err := uc.Get(context.Background(), domain.TMDBID(1), "")
	require.NoError(t, err)
	assert.Equal(t, "Canon", d.Title)
	assert.Empty(t, d.Degraded, "empty lang skips i18n entirely, no degrade")
}

func TestUseCase_Get_PerFieldI18nSurfaced(t *testing.T) {
	t.Parallel()
	canon := movie.Canon{ID: domain.MovieID(42), Title: "Canon Title", PosterAsset: new("/canon.jpg")}
	uc := New(
		fakeCanon{canon: canon},
		// Reader resolves per-field: ru-RU title, but overview/tagline fell through to en-US.
		fakeI18n{row: enrichpersistence.MovieI18nRow{
			Title:    new("Русский"),
			Overview: new("en overview"),
			Tagline:  new("en tagline"),
			Poster:   new("/ru-poster.jpg"),
		}},
		fakeCollection{},
		fakeMembership{},
	)

	d, err := uc.Get(context.Background(), domain.TMDBID(1), "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, "Русский", d.Title)
	require.NotNil(t, d.Overview)
	assert.Equal(t, "en overview", *d.Overview)
	require.NotNil(t, d.Tagline)
	assert.Equal(t, "en tagline", *d.Tagline)
	assert.Empty(t, d.Degraded)
}

func TestUseCase_Get_EmptyI18nOverviewDoesNotShadow(t *testing.T) {
	t.Parallel()
	canon := movie.Canon{ID: domain.MovieID(42), Title: "Canon Title", PosterAsset: new("/canon.jpg")}
	// Belt-and-suspenders: even if a reader ever returned an empty-string overview,
	// the usecase guard must not surface it (d.Overview stays nil, no shadow).
	empty := ""
	uc := New(
		fakeCanon{canon: canon},
		fakeI18n{row: enrichpersistence.MovieI18nRow{Title: new("Русский"), Overview: &empty, Tagline: &empty, Poster: new("/ru.jpg")}},
		fakeCollection{},
		fakeMembership{},
	)

	d, err := uc.Get(context.Background(), domain.TMDBID(1), "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, "Русский", d.Title, "localized title still wins")
	assert.Nil(t, d.Overview, "empty overview must not be surfaced (no shadow)")
	assert.Nil(t, d.Tagline, "empty tagline must not be surfaced")
}
