package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

type stubSettings map[string]regrab.Settings

func (s stubSettings) Lookup(_ context.Context, name string) (regrab.Settings, error) {
	v, ok := s[name]
	if !ok {
		return regrab.Settings{}, ports.ErrNotFound
	}
	return v, nil
}

type stubSnapshots map[string]regrab.RuntimeState

func (s stubSnapshots) Snapshot(name string) (regrab.RuntimeState, bool) {
	v, ok := s[name]
	return v, ok
}
func (s stubSnapshots) SnapshotAll() map[string]regrab.RuntimeState {
	out := make(map[string]regrab.RuntimeState, len(s))
	for k, v := range s {
		out[k] = v
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

func (s *stubGrabs) CountReplaysSince(_ context.Context, name string, since time.Time) (int, error) {
	for delta, v := range s.replaysSince[name] {
		want := fixedNow.Add(-delta)
		if absDuration(since.Sub(want)) < time.Minute {
			return v, nil
		}
	}
	return 0, nil
}
func (s *stubGrabs) CountReplaysAll(_ context.Context, name string) (int, error) {
	return s.replaysAll[name], nil
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

type stubBlacklistCount map[uint]int

func (s stubBlacklistCount) CountByInstance(_ context.Context, id uint) (int, error) {
	return s[id], nil
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
	blist[1] = 3

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
	if got.Watched != 12 || got.Unregistered != 24 || got.Regrabs24h != 1 || got.Regrabs7d != 5 || got.BlacklistSize != 3 {
		t.Errorf("counters: %+v", got)
	}
	if got.LastPollAt == nil || got.LastPollResult == nil || *got.LastPollResult != regrab.PollResultOK || got.NextPollAt == nil {
		t.Errorf("poll bookkeeping: %+v", got)
	}
}

func TestWatchdogRollupHandler_OneUnknownInstance(t *testing.T) {
	r, _, _, _, _ := setupHandler(t)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/ghost/watchdog/rollups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status: %d", w.Code)
	}
}

func TestWatchdogRollupHandler_OneNoSettings(t *testing.T) {
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
	names := []string{got.Items[0].InstanceName, got.Items[1].InstanceName}
	if !sort.StringsAreSorted(names) {
		t.Errorf("names not sorted: %v", names)
	}
}

func TestWatchdogRollupHandler_AggregateLatencyUnder100ms(t *testing.T) {
	gin.SetMode(gin.TestMode)
	names := make([]string, 10)
	lookup := stubLookup{}
	settings := stubSettings{}
	snaps := stubSnapshots{}
	for i := 0; i < 10; i++ {
		name := "inst" + string(rune('a'+i))
		names[i] = name
		lookup[name] = uint(i + 1)
		settings[name] = regrab.Settings{InstanceID: uint(i + 1), InstanceName: name, Enabled: true, PollInterval: time.Minute}
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
