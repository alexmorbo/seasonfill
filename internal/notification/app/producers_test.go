package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	adminpersistence "github.com/alexmorbo/seasonfill/internal/admin/persistence"
	notifapp "github.com/alexmorbo/seasonfill/internal/notification/app"
	notifpersistence "github.com/alexmorbo/seasonfill/internal/notification/persistence"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
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

// fakeFollowers is the Ф8-U-5c SeriesFollowerLister seam under test: it returns
// a fixed follower set and counts invocations so guard-clause tests can assert
// FollowersOf was (not) consulted.
type fakeFollowers struct {
	ids   []int64
	calls int
}

func (f *fakeFollowers) FollowersOf(_ context.Context, _ int64) ([]int64, error) {
	f.calls++
	return f.ids, nil
}

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// seedNotifUser inserts a users row so per-user outbox/notified_events FKs hold
// on Postgres (SQLite runs with foreign_keys off in tests).
func seedNotifUser(t *testing.T, db *gorm.DB, id int64, role string) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, db.Create(&database.UserModel{
		ID:         uint(id),
		Username:   fmt.Sprintf("u%d", id),
		Role:       role,
		AvatarMode: admin.AvatarModeAuto,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error)
}

func newProducerOutbox(db *gorm.DB) *notifpersistence.OutboxRepository {
	return notifpersistence.NewOutboxRepository(db)
}

func TestPremiereProducer_FiresPerFollower_ThenDeduped(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
			seedNotifUser(t, db, 1, admin.RoleAdmin)
			seedNotifUser(t, db, 10, admin.RoleUser)
			seedNotifUser(t, db, 11, admin.RoleUser)

			cal := &fakeCalendar{events: []notifapp.CalendarEvent{
				{SeriesID: 42, Title: "Foo", Season: 2, Episode: 1,
					AirDate: now.Add(24 * time.Hour), Milestone: "premiere"},
			}}
			followers := &fakeFollowers{ids: []int64{10, 11}}
			outbox := newProducerOutbox(db)
			marks := notifpersistence.NewNotifiedEventsRepository(db)
			p := notifapp.NewPremiereProducer(cal, outbox, marks, followers, nil, nil).WithClock(fixedClock(now))

			p.Run(ctx)
			rows, err := outbox.FetchDueBatch(ctx, now, 100)
			require.NoError(t, err)
			require.Len(t, rows, 2, "one row per follower")
			assert.Equal(t, "followed", cal.lastScope)
			assert.True(t, cal.lastOnlyPrem)
			gotUsers := map[int64]bool{}
			for _, r := range rows {
				assert.Equal(t, "season.premiere", r.EventType)
				gotUsers[r.UserID] = true
				require.NoError(t, outbox.MarkSent(ctx, r.ID))
			}
			assert.True(t, gotUsers[10] && gotUsers[11], "each follower gets its own row")

			// Re-scan: same premiere → deduped per-follower, nothing new.
			p.Run(ctx)
			rows2, err := outbox.FetchDueBatch(ctx, now, 100)
			require.NoError(t, err)
			assert.Empty(t, rows2, "second scan of the same premiere must enqueue nothing")
		})
	}
}

func TestPremiereProducer_NoFollowers_NoRows(t *testing.T) {
	t.Parallel()
	db := testhelpers.AllBackends(t)[0].NewDB(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	seedNotifUser(t, db, 1, admin.RoleAdmin)
	cal := &fakeCalendar{events: []notifapp.CalendarEvent{
		{SeriesID: 42, Title: "Foo", Season: 2, Episode: 1, AirDate: now.Add(24 * time.Hour), Milestone: "premiere"},
	}}
	followers := &fakeFollowers{ids: nil}
	outbox := newProducerOutbox(db)
	marks := notifpersistence.NewNotifiedEventsRepository(db)
	notifapp.NewPremiereProducer(cal, outbox, marks, followers, nil, nil).WithClock(fixedClock(now)).Run(ctx)
	rows, err := outbox.FetchDueBatch(ctx, now, 100)
	require.NoError(t, err)
	assert.Empty(t, rows, "no followers → no rows")
}

func TestPremiereProducer_PartialMarker_OnlyMissingFollowerFires(t *testing.T) {
	t.Parallel()
	db := testhelpers.AllBackends(t)[0].NewDB(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	seedNotifUser(t, db, 1, admin.RoleAdmin)
	seedNotifUser(t, db, 10, admin.RoleUser)
	seedNotifUser(t, db, 11, admin.RoleUser)
	marks := notifpersistence.NewNotifiedEventsRepository(db)
	// Follower 10 already notified for this premiere key.
	created, err := marks.MarkIfNew(ctx, 10, "season.premiere", "42:2", now)
	require.NoError(t, err)
	require.True(t, created)

	cal := &fakeCalendar{events: []notifapp.CalendarEvent{
		{SeriesID: 42, Title: "Foo", Season: 2, Episode: 1, AirDate: now.Add(24 * time.Hour), Milestone: "premiere"},
	}}
	followers := &fakeFollowers{ids: []int64{10, 11}}
	outbox := newProducerOutbox(db)
	notifapp.NewPremiereProducer(cal, outbox, marks, followers, nil, nil).WithClock(fixedClock(now)).Run(ctx)

	rows, err := outbox.FetchDueBatch(ctx, now, 100)
	require.NoError(t, err)
	require.Len(t, rows, 1, "only the un-marked follower fires")
	assert.EqualValues(t, 11, rows[0].UserID)
}

func TestPremiereProducer_IgnoresNonPremiere(t *testing.T) {
	t.Parallel()
	db := testhelpers.AllBackends(t)[0].NewDB(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	seedNotifUser(t, db, 1, admin.RoleAdmin)
	cal := &fakeCalendar{events: []notifapp.CalendarEvent{
		{SeriesID: 7, Title: "Bar", Season: 3, Episode: 10, AirDate: now, Milestone: "finale"},
	}}
	followers := &fakeFollowers{ids: []int64{10}}
	outbox := newProducerOutbox(db)
	marks := notifpersistence.NewNotifiedEventsRepository(db)
	notifapp.NewPremiereProducer(cal, outbox, marks, followers, nil, nil).WithClock(fixedClock(now)).Run(ctx)
	rows, err := outbox.FetchDueBatch(ctx, now, 100)
	require.NoError(t, err)
	assert.Empty(t, rows)
	assert.Zero(t, followers.calls, "non-premiere must not consult followers")
}

func TestDigestProducer_OneAdminRow_ThenWeekDeduped(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			now := time.Date(2026, 8, 9, 9, 0, 0, 0, time.UTC) // a Sunday
			seedNotifUser(t, db, 1, admin.RoleAdmin)
			cal := &fakeCalendar{events: []notifapp.CalendarEvent{
				{SeriesID: 1, Title: "A", Season: 1, Episode: 1, AirDate: now.Add(24 * time.Hour), Milestone: "premiere"},
				{SeriesID: 2, Title: "B", Season: 4, Episode: 8, AirDate: now.Add(48 * time.Hour), Milestone: "finale"},
				{SeriesID: 3, Title: "C", Season: 2, Episode: 5, AirDate: now.Add(72 * time.Hour), Milestone: "return"}, // ignored
			}}
			outbox := newProducerOutbox(db)
			marks := notifpersistence.NewNotifiedEventsRepository(db)
			users := adminpersistence.NewUserRepository(db)
			d := notifapp.NewDigestProducer(cal, outbox, marks, users, nil, nil).WithClock(fixedClock(now))

			d.Run(ctx)
			rows, err := outbox.FetchDueBatch(ctx, now, 100)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, "digest.weekly", rows[0].EventType)
			assert.EqualValues(t, 1, rows[0].UserID, "digest targets the seed admin")
			assert.Equal(t, "all", cal.lastScope)
			assert.False(t, cal.lastOnlyPrem)
			// Unmarshal rather than substring-match: Postgres jsonb reserializes
			// with whitespace/key-reorder, so a compact-form Contains is fragile.
			var digest map[string]any
			require.NoError(t, json.Unmarshal(rows[0].Payload, &digest))
			assert.EqualValues(t, 1, digest["premiere_count"])
			assert.EqualValues(t, 1, digest["finale_count"])

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
	seedNotifUser(t, db, 1, admin.RoleAdmin)
	cal := &fakeCalendar{events: nil}
	outbox := newProducerOutbox(db)
	marks := notifpersistence.NewNotifiedEventsRepository(db)
	users := adminpersistence.NewUserRepository(db)
	notifapp.NewDigestProducer(cal, outbox, marks, users, nil, nil).WithClock(fixedClock(now)).Run(ctx)
	rows, err := outbox.FetchDueBatch(ctx, now, 100)
	require.NoError(t, err)
	assert.Empty(t, rows, "an empty week must not send a digest")
	// And no marker was written, so a later non-empty run this week still fires:
	// MarkIfNew on the current ISO-week key must report created==true.
	y, w := now.ISOWeek()
	created, err := marks.MarkIfNew(ctx, 1, "digest.weekly", fmt.Sprintf("%04d-W%02d", y, w), now)
	require.NoError(t, err)
	assert.True(t, created)
}
