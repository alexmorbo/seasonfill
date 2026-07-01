package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	seriesdomain "github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/watchdog/app/regrab"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
	domainregrab "github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
	watchdogpersistence "github.com/alexmorbo/seasonfill/internal/watchdog/persistence"
)

type stubSeasonsLister struct {
	rows []watchdogpersistence.WatchdogSeasonRow
	next *watchdogpersistence.WatchdogSeasonsCursor
	err  error

	gotFilter watchdogpersistence.WatchdogSeasonsFilter
	gotLimit  int
	gotCursor *watchdogpersistence.WatchdogSeasonsCursor
}

func (s *stubSeasonsLister) ListSeasons(_ context.Context, f watchdogpersistence.WatchdogSeasonsFilter,
	limit int, cur *watchdogpersistence.WatchdogSeasonsCursor, _ time.Time,
) ([]watchdogpersistence.WatchdogSeasonRow, *watchdogpersistence.WatchdogSeasonsCursor, error) {
	s.gotFilter = f
	s.gotLimit = limit
	s.gotCursor = cur
	if s.err != nil {
		return nil, nil, s.err
	}
	return s.rows, s.next, nil
}

type stubSeriesLister struct {
	rows      []watchdogpersistence.WatchdogSeasonRow
	stats     map[int]watchdogpersistence.WatchdogSeasonStats
	decisions map[int][]watchdogpersistence.RecentDecisionRow
	grabs     map[int][]watchdogpersistence.RecentGrabRow
}

func (s *stubSeriesLister) SeasonsForSeries(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID, _ time.Time) ([]watchdogpersistence.WatchdogSeasonRow, error) {
	return s.rows, nil
}
func (s *stubSeriesLister) SeasonStatsFromDecisions(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID) (map[int]watchdogpersistence.WatchdogSeasonStats, error) {
	return s.stats, nil
}
func (s *stubSeriesLister) RecentDecisionsBySeason(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID, _ int) (map[int][]watchdogpersistence.RecentDecisionRow, error) {
	return s.decisions, nil
}
func (s *stubSeriesLister) RecentGrabsBySeason(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID, _ int) (map[int][]watchdogpersistence.RecentGrabRow, error) {
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
	ws := domainregrab.WatchdogState{
		InstanceName: "homelab", SonarrSeriesID: 169, SeasonNumber: 2,
		AttemptCount: 1, LastAttemptAt: now, UpdatedAt: now,
	}
	bl := domainregrab.BlacklistEntry{
		InstanceName: "homelab", SeriesID: 169, SeasonNumber: 2,
		Reason: domainregrab.ReasonConsecutiveNoBetter, Consecutive: 3, CreatedAt: now,
	}
	lastAired := now.Add(-24 * time.Hour)
	row := watchdogpersistence.WatchdogSeasonRow{
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
		WatchdogState:     &ws,
		Blacklist:         &bl,
	}
	lister := &stubSeasonsLister{rows: []watchdogpersistence.WatchdogSeasonRow{row}}
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
	assert.Equal(t, domain.SonarrSeriesID(169), item.SeriesID)
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
		next: &watchdogpersistence.WatchdogSeasonsCursor{InstanceName: "homelab", SeriesID: 200, SeasonNumber: 1},
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
	assert.Equal(t, domain.SonarrSeriesID(200), cur.SeriesID)
	assert.Equal(t, 1, cur.SeasonNumber)

	// Drive the next page with that cursor.
	req2 := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/watchdog/seasons?cursor="+got.NextCursor, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	require.NotNil(t, lister.gotCursor)
	assert.Equal(t, domain.SonarrSeriesID(200), lister.gotCursor.SeriesID)
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
	row := watchdogpersistence.WatchdogSeasonRow{
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
		rows: []watchdogpersistence.WatchdogSeasonRow{row},
		stats: map[int]watchdogpersistence.WatchdogSeasonStats{
			2: {AiredEpisodes: 10, ExistingEpisodes: 9},
		},
		decisions: map[int][]watchdogpersistence.RecentDecisionRow{
			2: {{ID: "d1", ScanRunID: "s1", Decision: "skip", Reason: "skip_all_complete", CreatedAt: now}},
		},
		grabs: map[int][]watchdogpersistence.RecentGrabRow{
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
	assert.Equal(t, domain.SonarrSeriesID(169), got.SeriesID)
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
	hash := domain.QbitHash("a1b2c3d4e5f60718293a4b5c6d7e8f9001122334")
	row := watchdogpersistence.WatchdogSeasonRow{
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
		rows: []watchdogpersistence.WatchdogSeasonRow{row},
		grabs: map[int][]watchdogpersistence.RecentGrabRow{
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
	row := watchdogpersistence.WatchdogSeasonRow{
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
		rows: []watchdogpersistence.WatchdogSeasonRow{row},
		grabs: map[int][]watchdogpersistence.RecentGrabRow{
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

// fakeSeriesTextLocalizer counts calls (to assert no N+1) and returns a
// seeded title map. Satisfies rest.SeriesTextLocalizer. Story E-1-B7.
type fakeSeriesTextLocalizer struct {
	calls    int
	titles   map[domain.SeriesID]string // canonID -> localized title
	lastIDs  []domain.SeriesID
	lastLang string
	err      error
}

func (f *fakeSeriesTextLocalizer) ListByIDsWithFallback(
	_ context.Context, ids []domain.SeriesID, lang string,
) (map[domain.SeriesID]seriesdomain.SeriesText, error) {
	f.calls++
	f.lastIDs = ids
	f.lastLang = lang
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[domain.SeriesID]seriesdomain.SeriesText, len(f.titles))
	for _, id := range ids {
		if title, ok := f.titles[id]; ok {
			t := title // local copy for &
			out[id] = seriesdomain.SeriesText{SeriesID: id, Language: lang, Title: &t}
		}
	}
	return out, nil
}

func seasonRowWithCanon(canonID domain.SeriesID, sonarrID domain.SonarrSeriesID, title string) watchdogpersistence.WatchdogSeasonRow {
	return watchdogpersistence.WatchdogSeasonRow{
		InstanceName:      "homelab",
		SeriesID:          sonarrID,
		CanonSeriesID:     canonID,
		SeasonNumber:      1,
		SeriesTitle:       title,
		Monitored:         true,
		OriginGUID:        "g1",
		OriginIndexerName: "Prowlarr",
	}
}

func doSeasonsList(t *testing.T, h *WatchdogSeasonsHandler, url string) dto.WatchdogSeasonsList {
	t.Helper()
	r := newSeasonsRouter(h)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var got dto.WatchdogSeasonsList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	return got
}

func TestWatchdogSeasons_List_Localize_RuRU(t *testing.T) {
	t.Parallel()
	lister := &stubSeasonsLister{rows: []watchdogpersistence.WatchdogSeasonRow{
		seasonRowWithCanon(10, 169, "Rick and Morty"),
		seasonRowWithCanon(11, 170, "Friends"),
	}}
	loc := &fakeSeriesTextLocalizer{titles: map[domain.SeriesID]string{
		10: "Рик и Морти",
		11: "Друзья",
	}}
	h := NewWatchdogSeasonsHandler(lister, &stubSeriesLister{}, stubSettingsLookup{}, nil).WithLocalizer(loc)

	got := doSeasonsList(t, h, "/api/v1/watchdog/seasons?lang=ru-RU")
	require.Len(t, got.Items, 2)
	assert.Equal(t, "Рик и Морти", got.Items[0].SeriesTitle)
	assert.Equal(t, "Друзья", got.Items[1].SeriesTitle)
	assert.Equal(t, 1, loc.calls, "single batch call, no N+1")
	assert.Equal(t, "ru-RU", loc.lastLang, "raw BCP-47 pass-through, not normalized")
}

func TestWatchdogSeasons_List_Localize_EnFallbackModeledByRepo(t *testing.T) {
	t.Parallel()
	// The repo's fallback is modeled by the fake returning the en-US row
	// under the ru request; the handler just overrides with whatever came back.
	lister := &stubSeasonsLister{rows: []watchdogpersistence.WatchdogSeasonRow{
		seasonRowWithCanon(10, 169, "Canon Title"),
	}}
	loc := &fakeSeriesTextLocalizer{titles: map[domain.SeriesID]string{10: "English Title"}}
	h := NewWatchdogSeasonsHandler(lister, &stubSeriesLister{}, stubSettingsLookup{}, nil).WithLocalizer(loc)

	got := doSeasonsList(t, h, "/api/v1/watchdog/seasons?lang=ru-RU")
	require.Len(t, got.Items, 1)
	assert.Equal(t, "English Title", got.Items[0].SeriesTitle)
	assert.Equal(t, 1, loc.calls)
}

func TestWatchdogSeasons_List_Localize_BothMissingKeepsCanon(t *testing.T) {
	t.Parallel()
	lister := &stubSeasonsLister{rows: []watchdogpersistence.WatchdogSeasonRow{
		seasonRowWithCanon(10, 169, "Canon Title"),
	}}
	loc := &fakeSeriesTextLocalizer{titles: map[domain.SeriesID]string{}} // empty map => miss
	h := NewWatchdogSeasonsHandler(lister, &stubSeriesLister{}, stubSettingsLookup{}, nil).WithLocalizer(loc)

	got := doSeasonsList(t, h, "/api/v1/watchdog/seasons?lang=ru-RU")
	require.Len(t, got.Items, 1)
	assert.Equal(t, "Canon Title", got.Items[0].SeriesTitle)
	assert.Equal(t, 1, loc.calls)
}

func TestWatchdogSeasons_List_Localize_EmptyLangZeroCalls(t *testing.T) {
	t.Parallel()
	lister := &stubSeasonsLister{rows: []watchdogpersistence.WatchdogSeasonRow{
		seasonRowWithCanon(10, 169, "Canon Title"),
	}}
	loc := &fakeSeriesTextLocalizer{titles: map[domain.SeriesID]string{10: "Рик и Морти"}}
	h := NewWatchdogSeasonsHandler(lister, &stubSeriesLister{}, stubSettingsLookup{}, nil).WithLocalizer(loc)

	got := doSeasonsList(t, h, "/api/v1/watchdog/seasons")
	require.Len(t, got.Items, 1)
	assert.Equal(t, "Canon Title", got.Items[0].SeriesTitle, "canon unchanged without ?lang=")
	assert.Equal(t, 0, loc.calls, "zero DB work when lang absent (non-breaking)")
}

func TestWatchdogSeasons_List_Localize_Unwired(t *testing.T) {
	t.Parallel()
	lister := &stubSeasonsLister{rows: []watchdogpersistence.WatchdogSeasonRow{
		seasonRowWithCanon(10, 169, "Canon Title"),
	}}
	h := NewWatchdogSeasonsHandler(lister, &stubSeriesLister{}, stubSettingsLookup{}, nil) // no WithLocalizer

	got := doSeasonsList(t, h, "/api/v1/watchdog/seasons?lang=ru-RU")
	require.Len(t, got.Items, 1)
	assert.Equal(t, "Canon Title", got.Items[0].SeriesTitle)
}

func TestWatchdogSeasons_List_Localize_ZeroCanonSkipped(t *testing.T) {
	t.Parallel()
	// Row with CanonSeriesID==0 keeps canon; sibling with valid id localizes.
	lister := &stubSeasonsLister{rows: []watchdogpersistence.WatchdogSeasonRow{
		seasonRowWithCanon(0, 169, "Broken Canon"),
		seasonRowWithCanon(11, 170, "Friends"),
	}}
	loc := &fakeSeriesTextLocalizer{titles: map[domain.SeriesID]string{11: "Друзья"}}
	h := NewWatchdogSeasonsHandler(lister, &stubSeriesLister{}, stubSettingsLookup{}, nil).WithLocalizer(loc)

	got := doSeasonsList(t, h, "/api/v1/watchdog/seasons?lang=ru-RU")
	require.Len(t, got.Items, 2)
	assert.Equal(t, "Broken Canon", got.Items[0].SeriesTitle)
	assert.Equal(t, "Друзья", got.Items[1].SeriesTitle)
	assert.Equal(t, 1, loc.calls)
	assert.NotContains(t, loc.lastIDs, domain.SeriesID(0), "zero canon id not sent to the batch")
}

func TestWatchdogSeasons_List_Localize_ErrorSoftFail(t *testing.T) {
	t.Parallel()
	lister := &stubSeasonsLister{rows: []watchdogpersistence.WatchdogSeasonRow{
		seasonRowWithCanon(10, 169, "Canon Title"),
	}}
	loc := &fakeSeriesTextLocalizer{err: errors.New("db down")}
	h := NewWatchdogSeasonsHandler(lister, &stubSeriesLister{}, stubSettingsLookup{}, nil).WithLocalizer(loc)

	got := doSeasonsList(t, h, "/api/v1/watchdog/seasons?lang=ru-RU")
	require.Len(t, got.Items, 1)
	assert.Equal(t, "Canon Title", got.Items[0].SeriesTitle, "soft-fail: canon on localizer error")
}

func TestWatchdogSeasons_Series_Localize_RuRU(t *testing.T) {
	t.Parallel()
	row := seasonRowWithCanon(10, 169, "Rick and Morty")
	row.SeasonNumber = 2
	series := &stubSeriesLister{rows: []watchdogpersistence.WatchdogSeasonRow{row}}
	loc := &fakeSeriesTextLocalizer{titles: map[domain.SeriesID]string{10: "Рик и Морти"}}
	h := NewWatchdogSeasonsHandler(&stubSeasonsLister{}, series, stubSettingsLookup{}, nil).WithLocalizer(loc)
	r := newSeasonsRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/watchdog/series/homelab/169?lang=ru-RU", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got dto.WatchdogSeriesDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "Рик и Морти", got.SeriesTitle)
	assert.Equal(t, 1, loc.calls)
	assert.Equal(t, "ru-RU", loc.lastLang)
}

func TestWatchdogSeasons_Series_Localize_EmptyLangZeroCalls(t *testing.T) {
	t.Parallel()
	row := seasonRowWithCanon(10, 169, "Rick and Morty")
	row.SeasonNumber = 2
	series := &stubSeriesLister{rows: []watchdogpersistence.WatchdogSeasonRow{row}}
	loc := &fakeSeriesTextLocalizer{titles: map[domain.SeriesID]string{10: "Рик и Морти"}}
	h := NewWatchdogSeasonsHandler(&stubSeasonsLister{}, series, stubSettingsLookup{}, nil).WithLocalizer(loc)
	r := newSeasonsRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/watchdog/series/homelab/169", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got dto.WatchdogSeriesDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "Rick and Morty", got.SeriesTitle, "canon unchanged without ?lang=")
	assert.Equal(t, 0, loc.calls)
}
