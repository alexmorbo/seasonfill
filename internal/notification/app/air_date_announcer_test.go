package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	notifapp "github.com/alexmorbo/seasonfill/internal/notification/app"
	notifpersistence "github.com/alexmorbo/seasonfill/internal/notification/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestAirDateAnnouncer_DeltaOnly_And_StormDedup(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			now := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
			outbox := notifpersistence.NewOutboxRepository(db)
			marks := notifpersistence.NewNotifiedEventsRepository(db)
			a := notifapp.NewAirDateAnnouncer(outbox, marks, nil, nil).WithClock(func() time.Time { return now })

			future := now.Add(21 * 24 * time.Hour)

			// (a) nil → future: fires.
			a.MaybeAnnounce(ctx, 42, "Foo", nil, new(future))
			rows, _ := outbox.FetchDueBatch(ctx, now, 100)
			require.Len(t, rows, 1)
			assert.Equal(t, "air_date.announced", rows[0].EventType)
			require.NoError(t, outbox.MarkSent(ctx, rows[0].ID))

			// (b) storm: same future date again → deduped.
			a.MaybeAnnounce(ctx, 42, "Foo", new(future), new(future))
			rows, _ = outbox.FetchDueBatch(ctx, now, 100)
			assert.Empty(t, rows, "unchanged date must not re-announce")

			// (c) shift to a NEW future date → re-fires.
			future2 := future.Add(7 * 24 * time.Hour)
			a.MaybeAnnounce(ctx, 42, "Foo", new(future), new(future2))
			rows, _ = outbox.FetchDueBatch(ctx, now, 100)
			require.Len(t, rows, 1)
			require.NoError(t, outbox.MarkSent(ctx, rows[0].ID))

			// (d) past date → no fire.
			past := now.Add(-24 * time.Hour)
			a.MaybeAnnounce(ctx, 42, "Foo", new(future2), new(past))
			rows, _ = outbox.FetchDueBatch(ctx, now, 100)
			assert.Empty(t, rows, "past date is not an announcement")

			// (e) nil new → no fire.
			a.MaybeAnnounce(ctx, 42, "Foo", new(future2), nil)
			rows, _ = outbox.FetchDueBatch(ctx, now, 100)
			assert.Empty(t, rows)
		})
	}
}
