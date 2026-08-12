package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// calNow — deterministic clock anchor; aired = calNow-1d, future = calNow+30d.
var calNow = time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

// calTestUserID owns the followed_series rows (Ф8-U-5 per-user FK). calSeedUser
// inserts the matching users row so followed_series_user_id_fkey holds.
const calTestUserID int64 = 1

func calSeedUser(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Create(&database.UserModel{
		ID:         uint(calTestUserID),
		Username:   "admin",
		Role:       admin.RoleAdmin,
		AvatarMode: admin.AvatarModeAuto,
		CreatedAt:  calNow,
		UpdatedAt:  calNow,
	}).Error)
}

func calWindow() (time.Time, time.Time) {
	return calNow.AddDate(0, -3, 0), calNow.AddDate(0, 3, 0)
}

func calSeedCache(t *testing.T, db *gorm.DB, instance string, sonarrID int, seriesID domain.SeriesID) {
	t.Helper()
	sid := seriesID
	row := database.SeriesCacheModel{
		InstanceName:   domain.InstanceName(instance),
		SonarrSeriesID: domain.SonarrSeriesID(sonarrID),
		SeriesID:       &sid,
		TitleSlug:      "slug",
		UpdatedAt:      calNow,
	}
	require.NoError(t, db.Create(&row).Error)
}

func calSeedFollowed(t *testing.T, db *gorm.DB, seriesID domain.SeriesID) {
	t.Helper()
	require.NoError(t, db.Create(&database.FollowedSeriesModel{
		UserID: calTestUserID, SeriesID: int64(seriesID), CreatedAt: calNow,
	}).Error)
}

func calSeedText(t *testing.T, db *gorm.DB, seriesID domain.SeriesID, lang, title string) {
	t.Helper()
	tt := title
	require.NoError(t, db.Create(&database.SeriesTextModel{
		SeriesID: seriesID, Language: lang, Title: &tt, UpdatedAt: calNow,
	}).Error)
}

func calSeedMediaText(t *testing.T, db *gorm.DB, seriesID domain.SeriesID, lang, poster string) {
	t.Helper()
	pa := poster
	require.NoError(t, db.Create(&database.SeriesMediaTextModel{
		SeriesID: seriesID, Language: lang, PosterAsset: &pa, UpdatedAt: calNow,
	}).Error)
}

func calQuery(scope, instance string) ports.CalendarQuery {
	from, to := calWindow()
	return ports.CalendarQuery{From: from, To: to, Scope: scope, Instance: instance, Limit: 5000}
}

// byEpisode indexes rows by episode id for order-independent assertions.
func byEpisode(rows []ports.CalendarEventRow) map[domain.EpisodeID][]ports.CalendarEventRow {
	m := map[domain.EpisodeID][]ports.CalendarEventRow{}
	for _, r := range rows {
		m[r.EpisodeID] = append(m[r.EpisodeID], r)
	}
	return m
}

func TestCalendarRepository_ScopeMilestoneStateMatrix(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewCalendarRepository(db)
			ctx := context.Background()
			calSeedUser(t, db)

			aired := calNow.Add(-24 * time.Hour)
			future := calNow.Add(30 * 24 * time.Hour)
			longAgo := calNow.Add(-60 * 24 * time.Hour) // in-window, >35d before `aired` → return gap

			// Series 201 — IN LIBRARY (instance "main"), season 1: E1 premiere+downloaded,
			// E2 missing (aired monitored fileless), E3 upcoming. Plus a special (excluded).
			seedHealthSeries(t, db, 201, nil, nil, nil)
			calSeedCache(t, db, "main", 9001, 201)
			seedEpisode(t, db, 2010, 201, 1, 1, &aired)
			seedEpisode(t, db, 2011, 201, 1, 2, &aired)
			seedEpisode(t, db, 2012, 201, 1, 3, &future)
			seedEpisode(t, db, 2013, 201, 0, 1, &aired)             // SPECIAL → excluded
			seedEpisodeState(t, db, "main", 2010, true, true, nil)  // downloaded
			seedEpisodeState(t, db, "main", 2011, true, false, nil) // missing
			seedEpisodeState(t, db, "main", 2012, true, false, nil) // upcoming (future)
			seedEpisodeState(t, db, "main", 2013, true, false, nil)

			// Series 202 — FOLLOWED, NOT in library. E1 premiere; a later ep after a
			// >35d gap → return. ru-RU-only title (any-lang no-hole proof).
			seedHealthSeries(t, db, 202, nil, nil, nil)
			calSeedFollowed(t, db, 202)
			calSeedText(t, db, 202, "ru-RU", "Приход")
			calSeedMediaText(t, db, 202, "ru-RU", "poster/ru.jpg") // ru-RU-only poster (any-lang no-hole proof)
			seedEpisode(t, db, 2020, 202, 1, 1, &longAgo)          // premiere
			seedEpisode(t, db, 2021, 202, 1, 2, &aired)            // E2 & season max ep → finale

			// Series 203 — neither library nor followed → excluded from all scopes.
			seedHealthSeries(t, db, 203, nil, nil, nil)
			seedEpisode(t, db, 2030, 203, 1, 1, &aired)

			// --- scope=all ---
			rows, err := repo.Events(ctx, calQuery("all", ""))
			require.NoError(t, err)
			idx := byEpisode(rows)
			// 203 excluded, special 2013 excluded.
			require.NotContains(t, idx, domain.EpisodeID(2030))
			require.NotContains(t, idx, domain.EpisodeID(2013))
			// 201 present (library) + 202 present (followed).
			require.Contains(t, idx, domain.EpisodeID(2010))
			require.Contains(t, idx, domain.EpisodeID(2020))

			// 201 E1: premiere flag, in library "main", has_file.
			e1 := idx[domain.EpisodeID(2010)][0]
			assert.True(t, e1.IsPremiere)
			require.NotNil(t, e1.InstanceName)
			assert.Equal(t, "main", *e1.InstanceName)
			require.NotNil(t, e1.HasFile)
			assert.True(t, *e1.HasFile)
			assert.False(t, e1.Followed)

			// 202 E1: any-lang title resolves to the ru-RU title (NO HOLE),
			// followed=true, no instance (LEFT JOIN NULL).
			f1 := idx[domain.EpisodeID(2020)][0]
			assert.Equal(t, "Приход", f1.Title)
			require.NotNil(t, f1.Poster, "poster resolves from ru-RU-only media row (NO HOLE)")
			assert.Equal(t, "poster/ru.jpg", *f1.Poster)
			assert.True(t, f1.Followed)
			assert.Nil(t, f1.InstanceName)
			assert.True(t, f1.IsPremiere)

			// 202 E2: is_finale (max ep of season), prev_air_date = 2020's air_date.
			f2 := idx[domain.EpisodeID(2021)][0]
			assert.True(t, f2.IsFinale)
			require.NotNil(t, f2.PrevAirDate)
			assert.WithinDuration(t, longAgo, *f2.PrevAirDate, 48*time.Hour)

			// --- scope=library: only 201 (202 followed-not-in-library drops) ---
			libRows, err := repo.Events(ctx, calQuery("library", ""))
			require.NoError(t, err)
			libIdx := byEpisode(libRows)
			assert.Contains(t, libIdx, domain.EpisodeID(2010))
			assert.NotContains(t, libIdx, domain.EpisodeID(2020))

			// --- scope=followed: only 202 ---
			folRows, err := repo.Events(ctx, calQuery("followed", ""))
			require.NoError(t, err)
			folIdx := byEpisode(folRows)
			assert.Contains(t, folIdx, domain.EpisodeID(2020))
			assert.NotContains(t, folIdx, domain.EpisodeID(2010))
		})
	}
}

// TestCalendarRepository_WindowBounds — episodes outside [from,to] are excluded,
// boundary air_date == to is included.
func TestCalendarRepository_WindowBounds(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewCalendarRepository(db)
			ctx := context.Background()
			calSeedUser(t, db)

			from, to := calWindow()
			before := from.Add(-24 * time.Hour)
			after := to.Add(24 * time.Hour)
			inside := calNow

			seedHealthSeries(t, db, 301, nil, nil, nil)
			calSeedFollowed(t, db, 301)
			seedEpisode(t, db, 3010, 301, 1, 1, &before)
			seedEpisode(t, db, 3011, 301, 1, 2, &inside)
			seedEpisode(t, db, 3012, 301, 1, 3, &after)
			seedEpisode(t, db, 3013, 301, 1, 4, &to) // exactly == to → included

			rows, err := repo.Events(ctx, ports.CalendarQuery{From: from, To: to, Scope: "followed", Limit: 5000})
			require.NoError(t, err)
			idx := byEpisode(rows)
			assert.NotContains(t, idx, domain.EpisodeID(3010), "before-window excluded")
			assert.Contains(t, idx, domain.EpisodeID(3011))
			assert.NotContains(t, idx, domain.EpisodeID(3012), "after-window excluded")
			assert.Contains(t, idx, domain.EpisodeID(3013), "air_date == to is inclusive")
		})
	}
}

// TestCalendarRepository_InstanceFilterAndMultiInstance — an episode in two
// instances yields two rows; the instance filter narrows which series appear.
func TestCalendarRepository_InstanceFilterAndMultiInstance(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewCalendarRepository(db)
			ctx := context.Background()

			aired := calNow.Add(-24 * time.Hour)

			// Series 401 in BOTH "main" and "anime".
			seedHealthSeries(t, db, 401, nil, nil, nil)
			calSeedCache(t, db, "main", 4001, 401)
			calSeedCache(t, db, "anime", 4002, 401)
			seedEpisode(t, db, 4010, 401, 1, 1, &aired)
			seedEpisodeState(t, db, "main", 4010, true, true, nil)
			seedEpisodeState(t, db, "anime", 4010, true, false, nil)

			// Series 402 only in "anime".
			seedHealthSeries(t, db, 402, nil, nil, nil)
			calSeedCache(t, db, "anime", 4003, 402)
			seedEpisode(t, db, 4020, 402, 1, 1, &aired)
			seedEpisodeState(t, db, "anime", 4020, true, true, nil)

			// No filter: 401 has 2 rows (main+anime).
			all, err := repo.Events(ctx, calQuery("library", ""))
			require.NoError(t, err)
			assert.Len(t, byEpisode(all)[domain.EpisodeID(4010)], 2, "episode in 2 instances → 2 rows")

			// Filter instance=main: 402 (anime-only) is gone; 401 still present.
			mainOnly, err := repo.Events(ctx, calQuery("library", "main"))
			require.NoError(t, err)
			idx := byEpisode(mainOnly)
			assert.Contains(t, idx, domain.EpisodeID(4010))
			assert.NotContains(t, idx, domain.EpisodeID(4020), "anime-only series excluded by instance=main filter")
		})
	}
}

// TestCalendarRepository_Empty — empty DB returns no rows, no error.
func TestCalendarRepository_Empty(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewCalendarRepository(db)
			rows, err := repo.Events(context.Background(), calQuery("all", ""))
			require.NoError(t, err)
			assert.Empty(t, rows)
		})
	}
}
