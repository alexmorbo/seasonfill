package gc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWeeklyJob_AllNil_NoOp(t *testing.T) {
	t.Parallel()
	WeeklyJob{}.Run(context.Background())
}

func TestWeeklyJob_SubtaskErrors_Continue(t *testing.T) {
	t.Parallel()
	called := []string{}
	job := WeeklyJob{
		OrphanSeries: func(_ context.Context) (OrphanSeriesResult, error) {
			called = append(called, "orphan")
			return OrphanSeriesResult{}, errors.New("boom")
		},
		MediaSweep: func(_ context.Context) (MediaSweepResult, error) {
			called = append(called, "media")
			return MediaSweepResult{}, nil
		},
		EventPrune: func(_ context.Context) (EventPruneResult, error) {
			called = append(called, "events")
			return EventPruneResult{Skipped: true, SkipReason: "test"}, nil
		},
	}
	job.Run(context.Background())
	assert.Equal(t, []string{"orphan", "media", "events"}, called)
}

func TestWeeklyJob_AllSucceed(t *testing.T) {
	t.Parallel()
	job := WeeklyJob{
		OrphanSeries: func(_ context.Context) (OrphanSeriesResult, error) {
			return OrphanSeriesResult{Candidates: 5, Deleted: 3}, nil
		},
		MediaSweep: func(_ context.Context) (MediaSweepResult, error) {
			return MediaSweepResult{LiveHashCount: 100, Candidates: 20, Deleted: 5}, nil
		},
		EventPrune: func(_ context.Context) (EventPruneResult, error) {
			return EventPruneResult{Deleted: 42}, nil
		},
	}
	job.Run(context.Background())
}
