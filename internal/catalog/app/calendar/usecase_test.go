package calendar

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

var ucNow = time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

type fakeCalRepo struct {
	lastQuery ports.CalendarQuery
	rows      []ports.CalendarEventRow
	err       error
}

func (f *fakeCalRepo) Events(_ context.Context, q ports.CalendarQuery) ([]ports.CalendarEventRow, error) {
	f.lastQuery = q
	return f.rows, f.err
}

func newUC(repo ports.CalendarRepository) *UseCase {
	return NewUseCase(repo).WithClock(func() time.Time { return ucNow })
}

func TestBuild_DefaultWindowAndScope(t *testing.T) {
	t.Parallel()
	repo := &fakeCalRepo{}
	_, err := newUC(repo).Build(context.Background(), Query{})
	require.NoError(t, err)
	assert.Equal(t, ucNow.AddDate(0, -3, 0), repo.lastQuery.From)
	assert.Equal(t, ucNow.AddDate(0, 3, 0), repo.lastQuery.To)
	assert.Equal(t, "all", repo.lastQuery.Scope, "unknown/empty scope → all")
	assert.Equal(t, eventRowCap, repo.lastQuery.Limit)
}

func TestBuild_ExplicitWindowAndScopeNormalize(t *testing.T) {
	t.Parallel()
	repo := &fakeCalRepo{}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	_, err := newUC(repo).Build(context.Background(), Query{From: from, To: to, Scope: "library", Instance: "main"})
	require.NoError(t, err)
	assert.Equal(t, from, repo.lastQuery.From)
	assert.Equal(t, to, repo.lastQuery.To)
	assert.Equal(t, "library", repo.lastQuery.Scope)
	assert.Equal(t, "main", repo.lastQuery.Instance)
}

func TestBuild_MilestonePrecedenceAndState(t *testing.T) {
	t.Parallel()
	aired := ucNow.Add(-24 * time.Hour)
	future := ucNow.Add(48 * time.Hour)
	prev := ucNow.Add(-100 * 24 * time.Hour)
	main := "main"

	repo := &fakeCalRepo{rows: []ports.CalendarEventRow{
		// premiere + downloaded (has_file), also a long prev gap → premiere wins.
		{EpisodeID: 1, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 1, AirDate: aired,
			IsPremiere: true, PrevAirDate: &prev, InstanceName: &main, HasFile: new(true), Monitored: new(true)},
		// finale (not E1) → finale, missing (monitored, no file, in library).
		// Distinct series (13) so the by-SeriesID flatten below keeps series 10's
		// premiere event — same-series episodes are separate events in assembly.
		{EpisodeID: 2, SeriesID: 13, SeasonNumber: 1, EpisodeNumber: 8, AirDate: aired,
			IsFinale: true, InstanceName: &main, HasFile: new(false), Monitored: new(true)},
		// return (gap>35d, not premiere/finale), upcoming (future air).
		{EpisodeID: 3, SeriesID: 11, SeasonNumber: 2, EpisodeNumber: 4, AirDate: future,
			PrevAirDate: &prev, InstanceName: &main, HasFile: new(false), Monitored: new(true)},
		// followed, no instance, aired, no file → followed_not_in_library. No milestone.
		{EpisodeID: 4, SeriesID: 12, SeasonNumber: 1, EpisodeNumber: 5, AirDate: aired,
			Followed: true, InstanceName: nil},
	}}

	rep, err := newUC(repo).Build(context.Background(), Query{})
	require.NoError(t, err)

	// Flatten events.
	got := map[domain.SeriesID]Event{}
	for _, d := range rep.Days {
		for _, e := range d.Events {
			got[e.SeriesID] = e
		}
	}
	assert.Equal(t, "premiere", *got[10].Milestone) // premiere > return
	assert.Equal(t, "downloaded", got[10].State)
	assert.True(t, got[10].SeasonPremiere)
	assert.Equal(t, []string{"main"}, got[10].InLibraryInstances)
	assert.Equal(t, "tv", got[10].MediaType)

	// series 11 event id=3 (return + upcoming).
	e11 := got[11]
	assert.Equal(t, "return", *e11.Milestone)
	assert.Equal(t, "upcoming", e11.State) // future air wins over anything

	e12 := got[12]
	assert.Nil(t, e12.Milestone)
	assert.Equal(t, "followed_not_in_library", e12.State)
	assert.Empty(t, e12.InLibraryInstances)
}

func TestBuild_OnlyPremieresFilter(t *testing.T) {
	t.Parallel()
	aired := ucNow.Add(-24 * time.Hour)
	repo := &fakeCalRepo{rows: []ports.CalendarEventRow{
		{EpisodeID: 1, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 1, AirDate: aired, IsPremiere: true},
		{EpisodeID: 2, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 8, AirDate: aired, IsFinale: true},
	}}
	rep, err := newUC(repo).Build(context.Background(), Query{OnlyPremieres: true})
	require.NoError(t, err)
	total := 0
	for _, d := range rep.Days {
		total += len(d.Events)
	}
	assert.Equal(t, 1, total, "only the premiere survives")
}

func TestBuild_MultiInstanceAggregation(t *testing.T) {
	t.Parallel()
	aired := ucNow.Add(-24 * time.Hour)
	main, anime := "main", "anime"
	repo := &fakeCalRepo{rows: []ports.CalendarEventRow{
		{EpisodeID: 1, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 2, AirDate: aired,
			InstanceName: &main, HasFile: new(false), Monitored: new(true)},
		{EpisodeID: 1, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 2, AirDate: aired,
			InstanceName: &anime, HasFile: new(true), Monitored: new(true)},
	}}
	rep, err := newUC(repo).Build(context.Background(), Query{})
	require.NoError(t, err)
	require.Len(t, rep.Days, 1)
	require.Len(t, rep.Days[0].Events, 1, "two rows → one aggregated event")
	e := rep.Days[0].Events[0]
	assert.Equal(t, []string{"anime", "main"}, e.InLibraryInstances) // sorted
	assert.Equal(t, "downloaded", e.State)                           // anyHasFile true
}

func TestBuild_DayGroupingChronological(t *testing.T) {
	t.Parallel()
	d1 := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	repo := &fakeCalRepo{rows: []ports.CalendarEventRow{
		{EpisodeID: 1, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 1, AirDate: d1},
		{EpisodeID: 2, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 2, AirDate: d1},
		{EpisodeID: 3, SeriesID: 11, SeasonNumber: 1, EpisodeNumber: 1, AirDate: d2},
	}}
	rep, err := newUC(repo).Build(context.Background(), Query{})
	require.NoError(t, err)
	require.Len(t, rep.Days, 2)
	assert.Equal(t, "2026-06-20", rep.Days[0].Date)
	assert.Len(t, rep.Days[0].Events, 2)
	assert.Equal(t, "2026-06-21", rep.Days[1].Date)
	assert.Len(t, rep.Days[1].Events, 1)
}
