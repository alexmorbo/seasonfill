package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	domainregrab "github.com/alexmorbo/seasonfill/domain/regrab"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type stubSeasonsLister struct {
	rows []repositories.WatchdogSeasonRow
	next *repositories.WatchdogSeasonsCursor
	err  error

	gotFilter repositories.WatchdogSeasonsFilter
	gotLimit  int
	gotCursor *repositories.WatchdogSeasonsCursor
}

func (s *stubSeasonsLister) ListSeasons(_ context.Context, f repositories.WatchdogSeasonsFilter,
	limit int, cur *repositories.WatchdogSeasonsCursor, _ time.Time,
) ([]repositories.WatchdogSeasonRow, *repositories.WatchdogSeasonsCursor, error) {
	s.gotFilter = f
	s.gotLimit = limit
	s.gotCursor = cur
	if s.err != nil {
		return nil, nil, s.err
	}
	return s.rows, s.next, nil
}

type stubSeriesLister struct {
	rows      []repositories.WatchdogSeasonRow
	stats     map[int]repositories.WatchdogSeasonStats
	decisions map[int][]repositories.RecentDecisionRow
	grabs     map[int][]repositories.RecentGrabRow
}

func (s *stubSeriesLister) SeasonsForSeries(_ context.Context, _ domain.InstanceName, _ int, _ time.Time) ([]repositories.WatchdogSeasonRow, error) {
	return s.rows, nil
}
func (s *stubSeriesLister) SeasonStatsFromDecisions(_ context.Context, _ domain.InstanceName, _ int) (map[int]repositories.WatchdogSeasonStats, error) {
	return s.stats, nil
}
func (s *stubSeriesLister) RecentDecisionsBySeason(_ context.Context, _ domain.InstanceName, _ int, _ int) (map[int][]repositories.RecentDecisionRow, error) {
	return s.decisions, nil
}
func (s *stubSeriesLister) RecentGrabsBySeason(_ context.Context, _ domain.InstanceName, _ int, _ int) (map[int][]repositories.RecentGrabRow, error) {
	return s.grabs, nil
}

type stubSettingsLookup map[string]regrab.Settings

func (s stubSettingsLookup) Lookup(_ context.Context, name domain.InstanceName) (regrab.Settings, error) {
	v, ok := s[string(name)]
	if !ok {
		return regrab.Settings{}, ports.ErrNotFound
	}
	return v, nil
}

func newSeasonsRouter(h *WatchdogSeasonsHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/watchdog/seasons", h.List)
	r.GET("/api/v1/watchdog/series/:instance/:id", h.Series)
	return r
}

func TestWatchdogSeasons_List_Empty(t *testing.T) {
	t.Parallel()
	lister := &stubSeasonsLister{}
	h := NewWatchdogSeasonsHandler(lister, &stubSeriesLister{}, stubSettingsLookup{}, nil)
	r := newSeasonsRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/watchdog/seasons", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got dto.WatchdogSeasonsList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Empty(t, got.Items)
	assert.Empty(t, got.NextCursor)
	assert.Equal(t, 100, lister.gotLimit, "default limit applied")
}

func TestWatchdogSeasons_List_FullRow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	expires := now.Add(2 * time.Hour)
	cd := cooldown.Cooldown{
		Scope: cooldown.ScopeSeries, Key: cooldown.SeriesKey("homelab", 169, 2),
		ExpiresAt: expires, Reason: "series_after_grab", CreatedAt: now,
	}
	nb := domainregrab.NoBetterCounter{
		ID: 1, InstanceID: 1, SeriesID: 169, SeasonNumber: 2,
		Consecutive: 1, LastSeenAt: now, CreatedAt: now, UpdatedAt: now,
	}
	bl := domainregrab.BlacklistEntry{
		ID: 1, InstanceID: 1, SeriesID: 169, SeasonNumber: 2,
		Reason: domainregrab.ReasonConsecutiveNoBetter, Consecutive: 3, CreatedAt: now,
	}
	lastAired := now.Add(-24 * time.Hour)
	row := repositories.WatchdogSeasonRow{
		InstanceID:        1,
		InstanceName:      "homelab",
		SeriesID:          169,
		SeasonNumber:      2,
		SeriesTitle:       "Friends",
		Monitored:         true,
		MissingAiredCount: 0,
		LastAiredAt:       &lastAired,
		OriginGUID:        "g1",
		OriginIndexerName: "Prowlarr",
		OriginFirstSeenAt: now.Add(-time.Hour),
		OriginLastSeenAt:  now,
		OriginLastUsedAt:  &now,
		Cooldown:          &cd,
		NoBetterCounter:   &nb,
		Blacklist:         &bl,
	}
	lister := &stubSeasonsLister{rows: []repositories.WatchdogSeasonRow{row}}
	h := NewWatchdogSeasonsHandler(lister, &stubSeriesLister{}, stubSettingsLookup{
		"homelab": {InstanceName: "homelab", MaxConsecutiveNoBetter: 3},
	}, nil).WithClock(func() time.Time { return now })
	r := newSeasonsRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/watchdog/seasons", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got dto.WatchdogSeasonsList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Items, 1)

	item := got.Items[0]
	assert.Equal(t, domain.InstanceName("homelab"), item.Instance)
	assert.Equal(t, 169, item.SeriesID)
	assert.Equal(t, "Friends", item.SeriesTitle)
	require.NotNil(t, item.Origin)
	assert.Equal(t, "Prowlarr", item.Origin.Indexer)
	require.NotNil(t, item.Cooldown)
	assert.Equal(t, "series_after_grab", item.Cooldown.Reason)
	require.NotNil(t, item.NoBetterCounter)
	assert.Equal(t, 3, item.NoBetterCounter.Max, "max projected from settings")
	require.NotNil(t, item.Blacklist)
	assert.Equal(t, "consecutive_no_better", item.Blacklist.Reason)
}

func TestWatchdogSeasons_List_FiltersAndCursor(t *testing.T) {
	t.Parallel()
	lister := &stubSeasonsLister{
		next: &repositories.WatchdogSeasonsCursor{InstanceName: "homelab", SeriesID: 200, SeasonNumber: 1},
	}
	h := NewWatchdogSeasonsHandler(lister, &stubSeriesLister{}, stubSettingsLookup{}, nil)
	r := newSeasonsRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/watchdog/seasons?instance=homelab&cooldown_only=1&blacklisted_only=true&q=Friends&limit=50", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	assert.Equal(t, domain.InstanceName("homelab"), lister.gotFilter.Instance)
	assert.True(t, lister.gotFilter.CooldownOnly)
	assert.True(t, lister.gotFilter.BlacklistedOnly)
	assert.Equal(t, "Friends", lister.gotFilter.Q)
	assert.Equal(t, 50, lister.gotLimit)

	var got dto.WatchdogSeasonsList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.NotEmpty(t, got.NextCursor, "cursor populated from repo")

	// Round-trip cursor.
	cur, err := decodeSeasonsCursor(got.NextCursor)
	require.NoError(t, err)
	assert.Equal(t, domain.InstanceName("homelab"), cur.InstanceName)
	assert.Equal(t, 200, cur.SeriesID)
	assert.Equal(t, 1, cur.SeasonNumber)

	// Drive the next page with that cursor.
	req2 := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/watchdog/seasons?cursor="+got.NextCursor, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	require.NotNil(t, lister.gotCursor)
	assert.Equal(t, 200, lister.gotCursor.SeriesID)
}

func TestWatchdogSeasons_List_RejectsInvalidLimit(t *testing.T) {
	t.Parallel()
	lister := &stubSeasonsLister{}
	h := NewWatchdogSeasonsHandler(lister, &stubSeriesLister{}, stubSettingsLookup{}, nil)
	r := newSeasonsRouter(h)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/watchdog/seasons?limit=99999", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWatchdogSeasons_List_RejectsInvalidCursor(t *testing.T) {
	t.Parallel()
	lister := &stubSeasonsLister{}
	h := NewWatchdogSeasonsHandler(lister, &stubSeriesLister{}, stubSettingsLookup{}, nil)
	r := newSeasonsRouter(h)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/watchdog/seasons?cursor=$$$$", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWatchdogSeasons_Series_Aggregates(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	row := repositories.WatchdogSeasonRow{
		InstanceID:        1,
		InstanceName:      "homelab",
		SeriesID:          169,
		SeriesTitle:       "Friends",
		Monitored:         true,
		SeasonNumber:      2,
		OriginGUID:        "g1",
		OriginIndexerName: "Prowlarr",
		OriginFirstSeenAt: now.Add(-time.Hour),
		OriginLastSeenAt:  now,
	}
	series := &stubSeriesLister{
		rows: []repositories.WatchdogSeasonRow{row},
		stats: map[int]repositories.WatchdogSeasonStats{
			2: {AiredEpisodes: 10, ExistingEpisodes: 9},
		},
		decisions: map[int][]repositories.RecentDecisionRow{
			2: {{ID: "d1", ScanRunID: "s1", Decision: "skip", Reason: "skip_all_complete", CreatedAt: now}},
		},
		grabs: map[int][]repositories.RecentGrabRow{
			2: {{ID: "g-uuid", ReleaseTitle: "Title", Status: "imported", CreatedAt: now}},
		},
	}
	h := NewWatchdogSeasonsHandler(&stubSeasonsLister{}, series, stubSettingsLookup{}, nil)
	r := newSeasonsRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/watchdog/series/homelab/169", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got dto.WatchdogSeriesDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, domain.InstanceName("homelab"), got.Instance)
	assert.Equal(t, 169, got.SeriesID)
	assert.Equal(t, "Friends", got.SeriesTitle)
	require.Len(t, got.Seasons, 1)
	s := got.Seasons[0]
	assert.Equal(t, 2, s.SeasonNumber)
	assert.Equal(t, 10, s.Stats.AiredEpisodeCount)
	assert.Equal(t, 9, s.Stats.EpisodeFileCount)
	assert.Equal(t, 1, s.Stats.MissingAiredCount, "aired-existing clamps non-negative")
	require.NotNil(t, s.Origin)
	require.Len(t, s.RecentDecisions, 1)
	assert.Equal(t, "skip", s.RecentDecisions[0].Decision)
	require.Len(t, s.RecentGrabs, 1)
	assert.Equal(t, "imported", s.RecentGrabs[0].Status)
}

func TestWatchdogSeasons_Series_RejectsInvalidID(t *testing.T) {
	t.Parallel()
	h := NewWatchdogSeasonsHandler(&stubSeasonsLister{}, &stubSeriesLister{}, stubSettingsLookup{}, nil)
	r := newSeasonsRouter(h)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/watchdog/series/homelab/abc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWatchdogSeasons_Series_OriginTorrentHash_Present(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	hash := "a1b2c3d4e5f60718293a4b5c6d7e8f9001122334"
	row := repositories.WatchdogSeasonRow{
		InstanceID:        1,
		InstanceName:      "homelab",
		SeriesID:          169,
		SeriesTitle:       "Friends",
		Monitored:         true,
		SeasonNumber:      2,
		OriginGUID:        "g1",
		OriginIndexerName: "Prowlarr",
		OriginFirstSeenAt: now.Add(-time.Hour),
		OriginLastSeenAt:  now,
	}
	series := &stubSeriesLister{
		rows: []repositories.WatchdogSeasonRow{row},
		grabs: map[int][]repositories.RecentGrabRow{
			2: {
				{ID: "newer", ReleaseTitle: "Title", Status: "imported", TorrentHash: &hash, CreatedAt: now},
				{ID: "older", ReleaseTitle: "Old", Status: "import_failed", CreatedAt: now.Add(-time.Hour)},
			},
		},
	}
	h := NewWatchdogSeasonsHandler(&stubSeasonsLister{}, series, stubSettingsLookup{}, nil)
	r := newSeasonsRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/watchdog/series/homelab/169", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got dto.WatchdogSeriesDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Seasons, 1)
	require.NotNil(t, got.Seasons[0].Origin)
	assert.Equal(t, hash, got.Seasons[0].Origin.TorrentHash)
}

func TestWatchdogSeasons_Series_OriginTorrentHash_Absent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	row := repositories.WatchdogSeasonRow{
		InstanceID:        1,
		InstanceName:      "homelab",
		SeriesID:          169,
		SeriesTitle:       "Friends",
		Monitored:         true,
		SeasonNumber:      2,
		OriginGUID:        "g1",
		OriginIndexerName: "Prowlarr",
		OriginFirstSeenAt: now.Add(-time.Hour),
		OriginLastSeenAt:  now,
	}
	series := &stubSeriesLister{
		rows: []repositories.WatchdogSeasonRow{row},
		grabs: map[int][]repositories.RecentGrabRow{
			2: {{ID: "no-hash", ReleaseTitle: "Title", Status: "imported", CreatedAt: now}},
		},
	}
	h := NewWatchdogSeasonsHandler(&stubSeasonsLister{}, series, stubSettingsLookup{}, nil)
	r := newSeasonsRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/watchdog/series/homelab/169", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// Confirm the JSON omits torrent_hash entirely (omitempty).
	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	seasons := raw["seasons"].([]any)
	require.Len(t, seasons, 1)
	origin := seasons[0].(map[string]any)["origin"].(map[string]any)
	_, hasHash := origin["torrent_hash"]
	assert.False(t, hasHash, "torrent_hash key omitted when no grab has a hash")
}
