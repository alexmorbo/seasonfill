package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
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
			seedNotifUser(t, db, 1, admin.RoleAdmin)
			seedNotifUser(t, db, 10, admin.RoleUser)
			outbox := newProducerOutbox(db)
			marks := notifpersistence.NewNotifiedEventsRepository(db)
			followers := &fakeFollowers{ids: []int64{10}}
			a := notifapp.NewAirDateAnnouncer(outbox, marks, followers, nil, nil).WithClock(func() time.Time { return now })

			future := now.Add(21 * 24 * time.Hour)

			// (a) nil → future: fires for the follower.
			a.MaybeAnnounce(ctx, 42, "Foo", nil, new(future))
			rows, _ := outbox.FetchDueBatch(ctx, now, 100)
			require.Len(t, rows, 1)
			assert.Equal(t, "air_date.announced", rows[0].EventType)
			assert.EqualValues(t, 10, rows[0].UserID)
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

func TestAirDateAnnouncer_FanOutPerFollower(t *testing.T) {
	t.Parallel()
	db := testhelpers.AllBackends(t)[0].NewDB(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	seedNotifUser(t, db, 1, admin.RoleAdmin)
	seedNotifUser(t, db, 10, admin.RoleUser)
	seedNotifUser(t, db, 11, admin.RoleUser)
	outbox := newProducerOutbox(db)
	marks := notifpersistence.NewNotifiedEventsRepository(db)
	followers := &fakeFollowers{ids: []int64{10, 11}}
	a := notifapp.NewAirDateAnnouncer(outbox, marks, followers, nil, nil).WithClock(func() time.Time { return now })

	future := now.Add(21 * 24 * time.Hour)
	a.MaybeAnnounce(ctx, 42, "Foo", nil, new(future))
	rows, err := outbox.FetchDueBatch(ctx, now, 100)
	require.NoError(t, err)
	require.Len(t, rows, 2, "one row per follower")
	got := map[int64]bool{}
	for _, r := range rows {
		got[r.UserID] = true
	}
	assert.True(t, got[10] && got[11])
}

// TestAirDateAnnouncer_GuardsShortCircuit proves the nil/past/unchanged guards
// return BEFORE FollowersOf is consulted (Ф8-U-5c: no wasted follower lookup).
func TestAirDateAnnouncer_GuardsShortCircuit(t *testing.T) {
	t.Parallel()
	db := testhelpers.AllBackends(t)[0].NewDB(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	seedNotifUser(t, db, 1, admin.RoleAdmin)
	outbox := newProducerOutbox(db)
	marks := notifpersistence.NewNotifiedEventsRepository(db)
	followers := &fakeFollowers{ids: []int64{10}}
	a := notifapp.NewAirDateAnnouncer(outbox, marks, followers, nil, nil).WithClock(func() time.Time { return now })

	future := now.Add(21 * 24 * time.Hour)
	past := now.Add(-24 * time.Hour)

	a.MaybeAnnounce(ctx, 42, "Foo", nil, nil)                 // nil newNext
	a.MaybeAnnounce(ctx, 42, "Foo", nil, new(past))           // past date
	a.MaybeAnnounce(ctx, 42, "Foo", new(future), new(future)) // unchanged date

	assert.Zero(t, followers.calls, "guards must short-circuit before FollowersOf")
	rows, err := outbox.FetchDueBatch(ctx, now, 100)
	require.NoError(t, err)
	assert.Empty(t, rows)
}
