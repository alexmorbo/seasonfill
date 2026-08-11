// Package moviecalendar assembles the read-only movie release calendar backing
// GET /api/v1/movies/calendar (Ф6-R-6a). Distinct from the TV calendar package:
// movie milestones are the three intrinsic release dates (theatrical/digital/
// physical), not episode-derived premiere/finale/return.
package moviecalendar

import (
	"context"
	"fmt"
	"sort"
	"time"
)

const defaultWindowMonths = 3

// Row is one repository result: a single movie×milestone within the window.
type Row struct {
	MovieID   int64
	TMDBID    *int64
	Title     string
	Poster    *string
	Milestone string // theatrical|digital|physical
	Date      time.Time
}

// Event is the assembled per-day event (mirrors Row; separated so the wire
// projection stays decoupled from the repository row shape).
type Event struct {
	MovieID   int64
	TMDBID    *int64
	Title     string
	Poster    *string
	Milestone string
	Date      time.Time
}

// Day groups the events falling on a single UTC calendar date.
type Day struct {
	Date   string
	Events []Event
}

// Report is the assembled calendar over [From,To].
type Report struct {
	GeneratedAt time.Time
	From        time.Time
	To          time.Time
	Days        []Day
}

// Query narrows the window. Zero From/To default to ±defaultWindowMonths.
type Query struct {
	From time.Time
	To   time.Time
}

// Repository reads release milestones within a window.
type Repository interface {
	Events(ctx context.Context, from, to time.Time) ([]Row, error)
}

// UseCase groups the repository rows by UTC day.
type UseCase struct {
	repo  Repository
	clock func() time.Time
}

func NewUseCase(repo Repository) *UseCase {
	return &UseCase{repo: repo, clock: func() time.Time { return time.Now().UTC() }}
}

// WithClock overrides the clock (tests).
func (uc *UseCase) WithClock(clock func() time.Time) *UseCase { uc.clock = clock; return uc }

// Build resolves the window (defaulting to ±3 months), reads the rows and groups
// them by UTC date ascending.
func (uc *UseCase) Build(ctx context.Context, q Query) (Report, error) {
	now := uc.clock()
	from, to := q.From, q.To
	if from.IsZero() {
		from = now.AddDate(0, -defaultWindowMonths, 0)
	}
	if to.IsZero() {
		to = now.AddDate(0, defaultWindowMonths, 0)
	}
	rows, err := uc.repo.Events(ctx, from, to)
	if err != nil {
		return Report{}, fmt.Errorf("movie calendar build: %w", err)
	}
	byDay := map[string][]Event{}
	for _, r := range rows {
		key := r.Date.UTC().Format("2006-01-02")
		byDay[key] = append(byDay[key], Event(r))
	}
	days := make([]Day, 0, len(byDay))
	for k, evs := range byDay {
		days = append(days, Day{Date: k, Events: evs})
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })
	return Report{GeneratedAt: now, From: from, To: to, Days: days}, nil
}
