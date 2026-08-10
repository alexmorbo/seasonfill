package app_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	notifapp "github.com/alexmorbo/seasonfill/internal/notification/app"
	notifpersistence "github.com/alexmorbo/seasonfill/internal/notification/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// fakeCalendar returns canned events regardless of query, capturing the last
// scope/onlyPremieres so tests can assert the producer's query intent.
type fakeCalendar struct {
	events           []notifapp.CalendarEvent
	lastScope        string
	lastOnlyPrem     bool
	lastFrom, lastTo time.Time
}

func (f *fakeCalendar) Upcoming(_ context.Context, from, to time.Time, scope string, onlyPremieres bool) ([]notifapp.CalendarEvent, error) {
	f.lastScope, f.lastOnlyPrem, f.lastFrom, f.lastTo = scope, onlyPremieres, from, to
	return f.events, nil
}

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestPremiereProducer_FiresOnce_ThenDeduped(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)

			cal := &fakeCalendar{events: []notifapp.CalendarEvent{
				{SeriesID: 42, Title: "Foo", Season: 2, Episode: 1,
					AirDate: now.Add(24 * time.Hour), Milestone: "premiere"},
			}}
			outbox := notifpersistence.NewOutboxRepository(db)
			marks := notifpersistence.NewNotifiedEventsRepository(db)
			p := notifapp.NewPremiereProducer(cal, outbox, marks, nil, nil).WithClock(fixedClock(now))

			p.Run(ctx)
			rows, err := outbox.FetchDueBatch(ctx, now, 100)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, "season.premiere", rows[0].EventType)
			assert.Equal(t, "followed", cal.lastScope)
			assert.True(t, cal.lastOnlyPrem)

			// Re-scan: same premiere → deduped, still exactly one row (the first
			// is still pending — drain it first to isolate the second scan).
			require.NoError(t, outbox.MarkSent(ctx, rows[0].ID))
			p.Run(ctx)
			rows2, err := outbox.FetchDueBatch(ctx, now, 100)
			require.NoError(t, err)
			assert.Empty(t, rows2, "second scan of the same premiere must enqueue nothing")
		})
	}
}

func TestPremiereProducer_IgnoresNonPremiere(t *testing.T) {
	t.Parallel()
	db := testhelpers.AllBackends(t)[0].NewDB(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	cal := &fakeCalendar{events: []notifapp.CalendarEvent{
		{SeriesID: 7, Title: "Bar", Season: 3, Episode: 10, AirDate: now, Milestone: "finale"},
	}}
	outbox := notifpersistence.NewOutboxRepository(db)
	marks := notifpersistence.NewNotifiedEventsRepository(db)
	notifapp.NewPremiereProducer(cal, outbox, marks, nil, nil).WithClock(fixedClock(now)).Run(ctx)
	rows, err := outbox.FetchDueBatch(ctx, now, 100)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestDigestProducer_OneAggregatedRow_ThenWeekDeduped(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			now := time.Date(2026, 8, 9, 9, 0, 0, 0, time.UTC) // a Sunday
			cal := &fakeCalendar{events: []notifapp.CalendarEvent{
				{SeriesID: 1, Title: "A", Season: 1, Episode: 1, AirDate: now.Add(24 * time.Hour), Milestone: "premiere"},
				{SeriesID: 2, Title: "B", Season: 4, Episode: 8, AirDate: now.Add(48 * time.Hour), Milestone: "finale"},
				{SeriesID: 3, Title: "C", Season: 2, Episode: 5, AirDate: now.Add(72 * time.Hour), Milestone: "return"}, // ignored
			}}
			outbox := notifpersistence.NewOutboxRepository(db)
			marks := notifpersistence.NewNotifiedEventsRepository(db)
			d := notifapp.NewDigestProducer(cal, outbox, marks, nil, nil).WithClock(fixedClock(now))

			d.Run(ctx)
			rows, err := outbox.FetchDueBatch(ctx, now, 100)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, "digest.weekly", rows[0].EventType)
			assert.Equal(t, "all", cal.lastScope)
			assert.False(t, cal.lastOnlyPrem)
			assert.Contains(t, string(rows[0].Payload), `"premiere_count":1`)
			assert.Contains(t, string(rows[0].Payload), `"finale_count":1`)

			// Same ISO week → deduped.
			require.NoError(t, outbox.MarkSent(ctx, rows[0].ID))
			d.Run(ctx)
			rows2, err := outbox.FetchDueBatch(ctx, now, 100)
			require.NoError(t, err)
			assert.Empty(t, rows2, "second digest in the same ISO week must enqueue nothing")
		})
	}
}

func TestDigestProducer_EmptyWeek_NoRow(t *testing.T) {
	t.Parallel()
	db := testhelpers.AllBackends(t)[0].NewDB(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 9, 9, 0, 0, 0, time.UTC)
	cal := &fakeCalendar{events: nil}
	outbox := notifpersistence.NewOutboxRepository(db)
	marks := notifpersistence.NewNotifiedEventsRepository(db)
	notifapp.NewDigestProducer(cal, outbox, marks, nil, nil).WithClock(fixedClock(now)).Run(ctx)
	rows, err := outbox.FetchDueBatch(ctx, now, 100)
	require.NoError(t, err)
	assert.Empty(t, rows, "an empty week must not send a digest")
	// And no marker was written, so a later non-empty run this week still fires:
	// MarkIfNew on the current ISO-week key must report created==true.
	y, w := now.ISOWeek()
	created, err := marks.MarkIfNew(ctx, "digest.weekly", fmt.Sprintf("%04d-W%02d", y, w), now)
	require.NoError(t, err)
	assert.True(t, created)
}
