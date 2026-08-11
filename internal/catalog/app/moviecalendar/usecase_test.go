package moviecalendar

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRepo struct {
	rows []Row
	err  error
	from time.Time
	to   time.Time
}

func (f *fakeRepo) Events(_ context.Context, from, to time.Time) ([]Row, error) {
	f.from, f.to = from, to
	return f.rows, f.err
}

var fixedNow = time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

func TestBuild_DefaultWindow(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{}
	uc := NewUseCase(repo).WithClock(func() time.Time { return fixedNow })

	rep, err := uc.Build(context.Background(), Query{})
	require.NoError(t, err)
	assert.Equal(t, fixedNow.AddDate(0, -3, 0), repo.from)
	assert.Equal(t, fixedNow.AddDate(0, 3, 0), repo.to)
	assert.Equal(t, fixedNow, rep.GeneratedAt)
	assert.Empty(t, rep.Days)
}

func TestBuild_DayGroupingAndMilestones(t *testing.T) {
	t.Parallel()
	d1 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	repo := &fakeRepo{rows: []Row{
		{MovieID: 1, Title: "A", Milestone: "theatrical", Date: d1},
		{MovieID: 1, Title: "A", Milestone: "digital", Date: d1},
		{MovieID: 2, Title: "B", Milestone: "physical", Date: d2},
	}}
	uc := NewUseCase(repo).WithClock(func() time.Time { return fixedNow })

	rep, err := uc.Build(context.Background(), Query{})
	require.NoError(t, err)
	require.Len(t, rep.Days, 2)
	assert.Equal(t, "2026-08-01", rep.Days[0].Date)
	assert.Len(t, rep.Days[0].Events, 2)
	assert.Equal(t, "2026-08-02", rep.Days[1].Date)
	assert.Len(t, rep.Days[1].Events, 1)
	assert.Equal(t, "physical", rep.Days[1].Events[0].Milestone)
}

func TestBuild_ExplicitWindow(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{}
	uc := NewUseCase(repo).WithClock(func() time.Time { return fixedNow })
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	_, err := uc.Build(context.Background(), Query{From: from, To: to})
	require.NoError(t, err)
	assert.Equal(t, from, repo.from)
	assert.Equal(t, to, repo.to)
}

func TestBuild_RepoError(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{err: errors.New("boom")}
	uc := NewUseCase(repo).WithClock(func() time.Time { return fixedNow })

	_, err := uc.Build(context.Background(), Query{})
	require.Error(t, err)
}
