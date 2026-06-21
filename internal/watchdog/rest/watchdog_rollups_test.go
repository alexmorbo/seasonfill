package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/qbit"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/watchdog/app/regrab"
)

type stubSettings map[string]regrab.Settings

func (s stubSettings) Lookup(_ context.Context, name domain.InstanceName) (regrab.Settings, error) {
	v, ok := s[string(name)]
	if !ok {
		return regrab.Settings{}, ports.ErrNotFound
	}
	return v, nil
}

type stubSnapshots map[string]regrab.RuntimeState

func (s stubSnapshots) Snapshot(name domain.InstanceName) (regrab.RuntimeState, bool) {
	v, ok := s[string(name)]
	return v, ok
}
func (s stubSnapshots) SnapshotAll() map[domain.InstanceName]regrab.RuntimeState {
	out := make(map[domain.InstanceName]regrab.RuntimeState, len(s))
	for k, v := range s {
		out[domain.InstanceName(k)] = v
	}
	return out
}

// fixedNow is the clock the tests pin so the duration-match in stubGrabs
// is stable.
var fixedNow = time.Date(2026, 6, 7, 1, 30, 0, 0, time.UTC)

type stubGrabs struct {
	replaysSince map[string]map[time.Duration]int
	replaysAll   map[string]int
}

func (s *stubGrabs) CountReplaysSince(_ context.Context, name domain.InstanceName, since time.Time) (int, error) {
	for delta, v := range s.replaysSince[string(name)] {
		want := fixedNow.Add(-delta)
		if absDuration(since.Sub(want)) < time.Minute {
			return v, nil
		}
	}
	return 0, nil
}
func (s *stubGrabs) CountReplaysAll(_ context.Context, name domain.InstanceName) (int, error) {
	return s.replaysAll[string(name)], nil
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

type stubBlacklistCount map[domain.InstanceName]int

func (s stubBlacklistCount) CountByInstance(_ context.Context, instance domain.InstanceName) (int, error) {
	return s[instance], nil
}

type stubLister []string

func (s stubLister) ListNames(context.Context) ([]string, error) { return []string(s), nil }

type stubLookup map[string]uint

func (s stubLookup) IDByName(_ context.Context, n string) (uint, bool, error) {
	v, ok := s[n]
	return v, ok, nil
}

func setupHandler(t *testing.T) (*gin.Engine, *stubGrabs, stubBlacklistCount, stubSnapshots, stubSettings) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	grabs := &stubGrabs{replaysSince: map[string]map[time.Duration]int{}, replaysAll: map[string]int{}}
	blist := stubBlacklistCount{}
	snaps := stubSnapshots{}
	settings := stubSettings{}
	h := NewWatchdogRollupHandler(
		settings, snaps, grabs, blist,
		stubLister{"4k", "homelab"},
		stubLookup{"homelab": 1, "4k": 2},
		nil,
	).WithClock(func() time.Time { return fixedNow })
	r := gin.New()
	r.GET("/api/v1/instances/:name/watchdog/rollups", h.One)
	r.GET("/api/v1/watchdog/rollups", h.All)
	return r, grabs, blist, snaps, settings
}

func TestWatchdogRollupHandler_OneReturnsPopulatedRow(t *testing.T) {
	t.Parallel()
	r, grabs, blist, snaps, settings := setupHandler(t)
	settings["homelab"] = regrab.Settings{
		InstanceID: 1, InstanceName: "homelab", Enabled: true,
		PollInterval: 30 * time.Minute, RegrabCooldown: 120 * time.Hour, MaxConsecutiveNoBetter: 3,
	}
	snaps["homelab"] = regrab.RuntimeState{
		LastPollAt: fixedNow.Add(-15 * time.Minute), LastPollResult: regrab.PollResultOK,
		QbitReachable: true, Watched: 12,
	}
	grabs.replaysAll["homelab"] = 24
	grabs.replaysSince["homelab"] = map[time.Duration]int{
		24 * time.Hour:     1,
		7 * 24 * time.Hour: 5,
	}
	blist["homelab"] = 3

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/rollups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	var got dto.WatchdogRollup
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.InstanceName != "homelab" || !got.Enabled || !got.Active {
		t.Errorf("envelope: %+v", got)
	}
	// Story 094 swapped Unregistered's source from lifetime replays
	// (CountReplaysAll) to live qBT-derived counts. Without a
	// QbitTorrentsLister wired (this test doesn't inject one), the
	// counter falls back to zero — exactly what the cold-start UX
	// should show before the first list call returns.
	if got.Watched != 12 || got.Unregistered != 0 || got.Regrabs24h != 1 || got.Regrabs7d != 5 || got.BlacklistSize != 3 {
		t.Errorf("counters: %+v", got)
	}
	if got.LastPollAt == nil || got.LastPollResult == nil || *got.LastPollResult != regrab.PollResultOK || got.NextPollAt == nil {
		t.Errorf("poll bookkeeping: %+v", got)
	}
}

func TestWatchdogRollupHandler_OneUnknownInstance(t *testing.T) {
	t.Parallel()
	r, _, _, _, _ := setupHandler(t)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/ghost/watchdog/rollups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status: %d", w.Code)
	}
}

func TestWatchdogRollupHandler_OneNoSettings(t *testing.T) {
	t.Parallel()
	r, _, _, _, _ := setupHandler(t)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/rollups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	var got dto.WatchdogRollup
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Enabled || got.Active {
		t.Errorf("expected disabled row: %+v", got)
	}
}

func TestWatchdogRollupHandler_AllSorted(t *testing.T) {
	t.Parallel()
	r, _, _, _, settings := setupHandler(t)
	settings["homelab"] = regrab.Settings{InstanceID: 1, InstanceName: "homelab", Enabled: true, PollInterval: 30 * time.Minute}
	settings["4k"] = regrab.Settings{InstanceID: 2, InstanceName: "4k", Enabled: true, PollInterval: 30 * time.Minute}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/watchdog/rollups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	var got dto.WatchdogRollupList
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Items) != 2 {
		t.Fatalf("len: %d", len(got.Items))
	}
	names := []string{string(got.Items[0].InstanceName), string(got.Items[1].InstanceName)}
	if !sort.StringsAreSorted(names) {
		t.Errorf("names not sorted: %v", names)
	}
}

func TestWatchdogRollupHandler_AggregateLatencyUnder100ms(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	names := make([]string, 10)
	lookup := stubLookup{}
	settings := stubSettings{}
	snaps := stubSnapshots{}
	for i := range 10 {
		name := "inst" + string(rune('a'+i))
		names[i] = name
		lookup[name] = uint(i + 1)
		settings[name] = regrab.Settings{InstanceID: uint(i + 1), InstanceName: domain.InstanceName(name), Enabled: true, PollInterval: time.Minute}
		snaps[name] = regrab.RuntimeState{LastPollAt: fixedNow, LastPollResult: regrab.PollResultOK, QbitReachable: true, Watched: i}
	}
	h := NewWatchdogRollupHandler(
		settings, snaps,
		&stubGrabs{replaysSince: map[string]map[time.Duration]int{}, replaysAll: map[string]int{}},
		stubBlacklistCount{}, stubLister(names), lookup, nil,
	).WithClock(func() time.Time { return fixedNow })
	r := gin.New()
	r.GET("/api/v1/watchdog/rollups", h.All)

	start := time.Now()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/watchdog/rollups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	elapsed := time.Since(start)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("aggregate latency: %v want < 100ms", elapsed)
	}
}

// --- Story 090: on-demand qBT reachability probe ---------------------------

// stubProbe satisfies QbitProbe; tracks call counts so the cache test
// can assert hits/misses.
type stubProbe struct {
	mu        sync.Mutex
	reachable bool
	err       error
	calls     map[string]int
}

func newStubProbe(reachable bool, err error) *stubProbe {
	return &stubProbe{reachable: reachable, err: err, calls: map[string]int{}}
}

func (p *stubProbe) Probe(_ context.Context, s regrab.Settings) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[string(s.InstanceName)]++
	return p.reachable, p.err
}

func (p *stubProbe) callsFor(name string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[name]
}

func setupProbeHandler(t *testing.T, snaps stubSnapshots, settings stubSettings, probe QbitProbe) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewWatchdogRollupHandler(
		settings, snaps,
		&stubGrabs{replaysSince: map[string]map[time.Duration]int{}, replaysAll: map[string]int{}},
		stubBlacklistCount{},
		stubLister{"homelab"},
		stubLookup{"homelab": 1},
		nil,
	).WithClock(func() time.Time { return fixedNow }).
		WithQbitProbe(probe).
		WithProbeTimeout(50 * time.Millisecond)
	r := gin.New()
	r.GET("/api/v1/instances/:name/watchdog/rollups", h.One)
	return r
}

func TestWatchdogRollupHandler_ProbeWhenSnapshotMissing(t *testing.T) {
	t.Parallel()
	settings := stubSettings{}
	settings["homelab"] = regrab.Settings{
		InstanceID: 1, InstanceName: "homelab", Enabled: true,
		URL: "http://qbit.local", PollInterval: 30 * time.Minute,
	}
	probe := newStubProbe(true, nil)
	r := setupProbeHandler(t, stubSnapshots{}, settings, probe)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/rollups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	var got dto.WatchdogRollup
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.QbitReachable {
		t.Errorf("expected QbitReachable=true via probe, got %+v", got)
	}
	if !got.Active {
		t.Errorf("expected Active=true, got %+v", got)
	}
	if probe.callsFor("homelab") != 1 {
		t.Errorf("expected exactly 1 probe call, got %d", probe.callsFor("homelab"))
	}
}

func TestWatchdogRollupHandler_ProbeCached(t *testing.T) {
	t.Parallel()
	settings := stubSettings{}
	settings["homelab"] = regrab.Settings{
		InstanceID: 1, InstanceName: "homelab", Enabled: true,
		URL: "http://qbit.local", PollInterval: 30 * time.Minute,
	}
	probe := newStubProbe(true, nil)
	r := setupProbeHandler(t, stubSnapshots{}, settings, probe)

	for i := range 3 {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/rollups", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("iter %d status: %d", i, w.Code)
		}
	}
	if probe.callsFor("homelab") != 1 {
		t.Errorf("expected 1 probe call (TTL cache), got %d", probe.callsFor("homelab"))
	}
}

func TestWatchdogRollupHandler_ProbeSkippedWhenDisabled(t *testing.T) {
	t.Parallel()
	settings := stubSettings{}
	settings["homelab"] = regrab.Settings{
		InstanceID: 1, InstanceName: "homelab", Enabled: false,
		URL: "http://qbit.local", PollInterval: 30 * time.Minute,
	}
	probe := newStubProbe(true, nil)
	r := setupProbeHandler(t, stubSnapshots{}, settings, probe)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/rollups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if probe.callsFor("homelab") != 0 {
		t.Errorf("expected 0 probe calls when watchdog disabled, got %d", probe.callsFor("homelab"))
	}
}

func TestWatchdogRollupHandler_ProbeSkippedWhenSnapshotFresh(t *testing.T) {
	t.Parallel()
	settings := stubSettings{}
	settings["homelab"] = regrab.Settings{
		InstanceID: 1, InstanceName: "homelab", Enabled: true,
		URL: "http://qbit.local", PollInterval: 30 * time.Minute,
	}
	snaps := stubSnapshots{}
	snaps["homelab"] = regrab.RuntimeState{
		LastPollAt: fixedNow.Add(-5 * time.Minute), LastPollResult: regrab.PollResultOK,
		QbitReachable: true, Watched: 12,
	}
	probe := newStubProbe(false, errors.New("should not be called"))
	r := setupProbeHandler(t, snaps, settings, probe)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/rollups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if probe.callsFor("homelab") != 0 {
		t.Errorf("expected 0 probe calls when snapshot fresh, got %d", probe.callsFor("homelab"))
	}
	var got dto.WatchdogRollup
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if !got.QbitReachable {
		t.Errorf("expected QbitReachable=true from snapshot, got %+v", got)
	}
}

// --- Story 094: on-demand torrents counters --------------------------------

// stubLister satisfies QbitTorrentsLister. Tracks call counts per
// instance so the cache test can assert hits/misses, and mirrors
// stubProbe's shape on purpose.
type stubLister2 struct {
	mu       sync.Mutex
	torrents []qbit.Torrent
	err      error
	calls    map[string]int
}

func newStubLister(torrents []qbit.Torrent, err error) *stubLister2 {
	return &stubLister2{torrents: torrents, err: err, calls: map[string]int{}}
}

func (s *stubLister2) ListTorrents(_ context.Context, sett regrab.Settings) ([]qbit.Torrent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[string(sett.InstanceName)]++
	return s.torrents, s.err
}

func (s *stubLister2) callsFor(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[name]
}

func setupListerHandler(t *testing.T, snaps stubSnapshots, settings stubSettings, probe QbitProbe, lister QbitTorrentsLister) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewWatchdogRollupHandler(
		settings, snaps,
		&stubGrabs{replaysSince: map[string]map[time.Duration]int{}, replaysAll: map[string]int{}},
		stubBlacklistCount{},
		stubLister{"homelab"},
		stubLookup{"homelab": 1},
		nil,
	).WithClock(func() time.Time { return fixedNow }).
		WithQbitProbe(probe).
		WithProbeTimeout(50 * time.Millisecond).
		WithQbitTorrentsLister(lister).
		WithTorrentsTimeout(50 * time.Millisecond)
	r := gin.New()
	r.GET("/api/v1/instances/:name/watchdog/rollups", h.One)
	return r
}

func TestWatchdogRollupHandler_ListTorrentsFillsCountersWhenSnapshotMissing(t *testing.T) {
	t.Parallel()
	settings := stubSettings{}
	settings["homelab"] = regrab.Settings{
		InstanceID: 1, InstanceName: "homelab", Enabled: true,
		URL: "http://qbit.local", PollInterval: 30 * time.Minute,
		Category: "sonarr",
	}
	probe := newStubProbe(true, nil)
	lister := newStubLister([]qbit.Torrent{
		{Hash: "a", Category: "sonarr", Tags: ""},
		{Hash: "b", Category: "sonarr", Tags: "issue"},
		{Hash: "c", Category: "sonarr", Tags: "foo,unregistered"},
		{Hash: "d", Category: "sonarr", Tags: "Issue, hd"},
		{Hash: "e", Category: "other", Tags: "issue"}, // filtered out by category
	}, nil)
	r := setupListerHandler(t, stubSnapshots{}, settings, probe, lister)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/rollups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	var got dto.WatchdogRollup
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Watched != 4 {
		t.Errorf("watched: want 4, got %d (row=%+v)", got.Watched, got)
	}
	if got.Unregistered != 3 {
		t.Errorf("unregistered: want 3, got %d (row=%+v)", got.Unregistered, got)
	}
	if lister.callsFor("homelab") != 1 {
		t.Errorf("expected 1 list call, got %d", lister.callsFor("homelab"))
	}
}

func TestWatchdogRollupHandler_ListTorrentsCached(t *testing.T) {
	t.Parallel()
	settings := stubSettings{}
	settings["homelab"] = regrab.Settings{
		InstanceID: 1, InstanceName: "homelab", Enabled: true,
		URL: "http://qbit.local", PollInterval: 30 * time.Minute,
	}
	probe := newStubProbe(true, nil)
	lister := newStubLister([]qbit.Torrent{{Hash: "a", Tags: "issue"}}, nil)
	r := setupListerHandler(t, stubSnapshots{}, settings, probe, lister)

	for i := range 3 {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/rollups", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("iter %d status: %d", i, w.Code)
		}
	}
	if lister.callsFor("homelab") != 1 {
		t.Errorf("expected 1 list call (TTL cache), got %d", lister.callsFor("homelab"))
	}
}

func TestWatchdogRollupHandler_ListTorrentsFallsBackToSnapshotOnError(t *testing.T) {
	t.Parallel()
	settings := stubSettings{}
	settings["homelab"] = regrab.Settings{
		InstanceID: 1, InstanceName: "homelab", Enabled: true,
		URL: "http://qbit.local", PollInterval: 30 * time.Minute,
	}
	snaps := stubSnapshots{}
	snaps["homelab"] = regrab.RuntimeState{
		LastPollAt: fixedNow.Add(-2 * time.Hour), LastPollResult: regrab.PollResultOK,
		QbitReachable: true, Watched: 42,
	}
	probe := newStubProbe(true, nil)
	lister := newStubLister(nil, errors.New("qbit list boom"))
	r := setupListerHandler(t, snaps, settings, probe, lister)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/rollups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	var got dto.WatchdogRollup
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Watched != 42 {
		t.Errorf("expected snapshot Watched=42 fallback, got %d", got.Watched)
	}
	if got.Unregistered != 0 {
		t.Errorf("unregistered fallback default: want 0, got %d", got.Unregistered)
	}
}

func TestWatchdogRollupHandler_ListTorrentsSkippedWhenSnapshotFresh(t *testing.T) {
	t.Parallel()
	settings := stubSettings{}
	settings["homelab"] = regrab.Settings{
		InstanceID: 1, InstanceName: "homelab", Enabled: true,
		URL: "http://qbit.local", PollInterval: 30 * time.Minute,
	}
	snaps := stubSnapshots{}
	snaps["homelab"] = regrab.RuntimeState{
		LastPollAt: fixedNow.Add(-5 * time.Minute), LastPollResult: regrab.PollResultOK,
		QbitReachable: true, Watched: 12,
	}
	probe := newStubProbe(true, nil)
	lister := newStubLister(nil, errors.New("should not be called"))
	r := setupListerHandler(t, snaps, settings, probe, lister)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/rollups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if lister.callsFor("homelab") != 0 {
		t.Errorf("expected 0 list calls when snapshot fresh, got %d", lister.callsFor("homelab"))
	}
	var got dto.WatchdogRollup
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Watched != 12 {
		t.Errorf("expected snapshot Watched=12, got %d", got.Watched)
	}
}

func TestWatchdogRollupHandler_ListTorrentsSkippedWhenQbitUnreachable(t *testing.T) {
	t.Parallel()
	settings := stubSettings{}
	settings["homelab"] = regrab.Settings{
		InstanceID: 1, InstanceName: "homelab", Enabled: true,
		URL: "http://qbit.local", PollInterval: 30 * time.Minute,
	}
	probe := newStubProbe(false, nil)
	lister := newStubLister(nil, errors.New("should not be called"))
	r := setupListerHandler(t, stubSnapshots{}, settings, probe, lister)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/rollups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if lister.callsFor("homelab") != 0 {
		t.Errorf("expected 0 list calls when qBT unreachable, got %d", lister.callsFor("homelab"))
	}
}

func TestWatchdogRollupHandler_ListTorrentsSkippedWhenDisabled(t *testing.T) {
	t.Parallel()
	settings := stubSettings{}
	settings["homelab"] = regrab.Settings{
		InstanceID: 1, InstanceName: "homelab", Enabled: false,
		URL: "http://qbit.local", PollInterval: 30 * time.Minute,
	}
	probe := newStubProbe(true, nil)
	lister := newStubLister(nil, errors.New("should not be called"))
	r := setupListerHandler(t, stubSnapshots{}, settings, probe, lister)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/rollups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if lister.callsFor("homelab") != 0 {
		t.Errorf("expected 0 list calls when watchdog disabled, got %d", lister.callsFor("homelab"))
	}
}

func TestCountTorrentsAndUnregisteredTagHeuristic(t *testing.T) {
	t.Parallel()
	torrents := []qbit.Torrent{
		{Category: "sonarr", Tags: ""},
		{Category: "sonarr", Tags: "issue"},
		{Category: "sonarr", Tags: "Tracker_Error"},
		{Category: "sonarr", Tags: "foo,unregistered,bar"},
		{Category: "sonarr", Tags: "harmless"},
		{Category: "movies", Tags: "issue"}, // filtered
	}
	w, u := countTorrents(torrents, "sonarr")
	if w != 5 {
		t.Errorf("watched: want 5, got %d", w)
	}
	if u != 3 {
		t.Errorf("unregistered: want 3, got %d", u)
	}

	// Empty category bypasses the filter (qBT did the server-side filter).
	w2, u2 := countTorrents(torrents, "")
	if w2 != 6 {
		t.Errorf("watched (no filter): want 6, got %d", w2)
	}
	if u2 != 4 {
		t.Errorf("unregistered (no filter): want 4, got %d", u2)
	}
}

func TestWatchdogRollupHandler_ProbeRecoveryAfterStaleUnreachable(t *testing.T) {
	t.Parallel()
	settings := stubSettings{}
	settings["homelab"] = regrab.Settings{
		InstanceID: 1, InstanceName: "homelab", Enabled: true,
		URL: "http://qbit.local", PollInterval: 30 * time.Minute,
	}
	snaps := stubSnapshots{}
	snaps["homelab"] = regrab.RuntimeState{
		LastPollAt: fixedNow.Add(-2 * time.Minute), LastPollResult: regrab.PollResultQbitError,
		QbitReachable: false, Watched: 0,
	}
	probe := newStubProbe(true, nil)
	r := setupProbeHandler(t, snaps, settings, probe)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/rollups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if probe.callsFor("homelab") != 1 {
		t.Errorf("expected 1 probe call (stale unreachable snapshot), got %d", probe.callsFor("homelab"))
	}
	var got dto.WatchdogRollup
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if !got.QbitReachable {
		t.Errorf("expected probe to override stale unreachable snapshot, got %+v", got)
	}
}
