package seriesdetail

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeNEEpisodes struct {
	rows []series.CanonEpisode
	err  error
}

func (f *fakeNEEpisodes) ListBySeries(context.Context, domain.SeriesID) ([]series.CanonEpisode, error) {
	return f.rows, f.err
}

type fakeNETexts struct {
	title *string
	err   error
}

func (f *fakeNETexts) GetWithFallback(context.Context, domain.EpisodeID, string) (series.EpisodeText, error) {
	if f.err != nil {
		return series.EpisodeText{}, f.err
	}
	return series.EpisodeText{Title: f.title}, nil
}
func (f *fakeNETexts) ListByEpisodeIDsWithFallback(context.Context, []domain.EpisodeID, string) (map[domain.EpisodeID]series.EpisodeText, error) {
	return nil, nil
}

func neAt(days int) *time.Time { t := time.Now().UTC().AddDate(0, 0, days); return &t }

func TestNextEpisodeAdapter_PicksEarliestFutureAndLocalizes(t *testing.T) {
	title := "Эпизод"
	eps := &fakeNEEpisodes{rows: []series.CanonEpisode{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 5, AirDate: neAt(-3)}, // past — skip
		{ID: 2, SeasonNumber: 2, EpisodeNumber: 2, AirDate: neAt(10)},
		{ID: 3, SeasonNumber: 2, EpisodeNumber: 1, AirDate: neAt(3)}, // earliest future
	}}
	a := NewNextEpisodeAdapter(eps, &fakeNETexts{title: &title}, func() time.Time { return time.Now().UTC() })
	ref, ok, err := a.NextAired(context.Background(), 42, "ru-RU")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 2, ref.SeasonNumber)
	assert.Equal(t, 1, ref.EpisodeNumber)
	assert.Equal(t, "Эпизод", ref.Title)
}

func TestNextEpisodeAdapter_SkipsSpecials(t *testing.T) {
	eps := &fakeNEEpisodes{rows: []series.CanonEpisode{
		{ID: 1, SeasonNumber: 0, EpisodeNumber: 1, AirDate: neAt(1)}, // S0 special — skip
	}}
	a := NewNextEpisodeAdapter(eps, &fakeNETexts{}, nil)
	_, ok, err := a.NextAired(context.Background(), 42, "en-US")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestNextEpisodeAdapter_NoFutureEpisode(t *testing.T) {
	eps := &fakeNEEpisodes{rows: []series.CanonEpisode{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, AirDate: neAt(-1)},
		{ID: 2, SeasonNumber: 1, EpisodeNumber: 2, AirDate: nil},
	}}
	a := NewNextEpisodeAdapter(eps, &fakeNETexts{}, nil)
	_, ok, err := a.NextAired(context.Background(), 42, "en-US")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestNextEpisodeAdapter_ListErrorPropagates(t *testing.T) {
	a := NewNextEpisodeAdapter(&fakeNEEpisodes{err: errors.New("db down")}, &fakeNETexts{}, nil)
	_, _, err := a.NextAired(context.Background(), 42, "en-US")
	require.Error(t, err)
}

func TestNextEpisodeAdapter_TitleMissEmptyTitleOK(t *testing.T) {
	eps := &fakeNEEpisodes{rows: []series.CanonEpisode{
		{ID: 7, SeasonNumber: 1, EpisodeNumber: 1, AirDate: neAt(2)},
	}}
	a := NewNextEpisodeAdapter(eps, &fakeNETexts{err: errors.New("not found")}, nil)
	ref, ok, err := a.NextAired(context.Background(), 42, "en-US")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "", ref.Title)
}
