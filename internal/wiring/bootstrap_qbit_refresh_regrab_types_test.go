package wiring

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/cmd/server/loops"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/app/regrab"
)

// regrabTypesFakeRunner is a no-op RegrabRunner: this test never lets a
// spawned loop do real work, it only cares about which loops got spawned.
type regrabTypesFakeRunner struct{}

func (regrabTypesFakeRunner) RunInstance(context.Context, domain.InstanceName) (regrab.RunResult, error) {
	return regrab.RunResult{}, nil
}

// regrabTypesFakeMetrics records the ADR-0025 F2 gauge so the test can see
// which instances the regrab loop refused, without reaching into the loops
// package's unexported state.
type regrabTypesFakeMetrics struct {
	mu         sync.Mutex
	unresolved map[string]int
}

func (m *regrabTypesFakeMetrics) SetQbitUnreachableStreak(domain.InstanceName, int) {}
func (m *regrabTypesFakeMetrics) SetRegrabCandidates(domain.InstanceName, int)      {}
func (m *regrabTypesFakeMetrics) SetRegrabUnresolvedInstance(n domain.InstanceName, v int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unresolved == nil {
		m.unresolved = make(map[string]int)
	}
	m.unresolved[string(n)] = v
}

func (m *regrabTypesFakeMetrics) get(name string) (int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.unresolved[name]
	return v, ok
}

// ★ ADR-0025 F2 headline wiring proof: the SHARED projection reaches
// torrentsync UNTOUCHED — the radarr row is still in the map handed to it —
// while regrab filters that same row out INSIDE itself.
//
// This is the guard against the tempting-but-wrong fix of filtering radarr
// out of qbitLoader / refreshQbitLoops: torrentsync runs against radarr on
// production every ~30s without errors, and that row must keep working.
func TestRefreshQbitLoops_TorrentsyncKeepsRadarrWhileRegrabSkipsIt(t *testing.T) {
	radarrHolder := adapters.NewRadarrInstanceMapHolder(map[string]scan.RadarrInstance{
		"radarr": {Config: runtime.InstanceSnapshot{Name: "radarr", Type: "radarr"}},
	})
	sonarrHolder := adapters.NewInstanceMapHolder(map[string]scan.Instance{
		"homelab": {Config: runtime.InstanceSnapshot{Name: "homelab", Type: "sonarr"}},
	})

	metrics := &regrabTypesFakeMetrics{}
	var bgWG sync.WaitGroup
	regrabLoop := loops.NewRegrabLoop(regrabTypesFakeRunner{}, metrics, &bgWG, slog.Default()).
		WithInstanceTypes(adapters.NewRegrabInstanceTypes(sonarrHolder, radarrHolder))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	regrabLoop.Start(ctx)

	ts := &qbitRefreshFakeSwapper{}
	loader := &qbitRefreshFakeLoader{m: map[string]regrab.Settings{
		"homelab": {InstanceName: "homelab", Enabled: true, PollInterval: time.Hour},
		"radarr":  {InstanceName: "radarr", Enabled: true, PollInterval: time.Hour},
	}}

	if n := refreshQbitLoops(ctx, regrabLoop, ts, loader); n != 2 {
		t.Fatalf("projection size = %d, want 2 (the shared map must NOT be filtered)", n)
	}

	// torrentsync side — the radarr row survived the shared projection.
	if _, ok := ts.got["radarr"]; !ok {
		t.Fatalf("torrentsync lost the radarr row: %v", ts.got)
	}
	if !ts.got["radarr"].Enabled {
		t.Fatal("torrentsync received radarr with Enabled=false; its loop would never spawn")
	}
	if _, ok := ts.got["homelab"]; !ok {
		t.Fatalf("torrentsync lost the homelab row: %v", ts.got)
	}

	// regrab side — radarr was skipped INSIDE the loop, homelab was not.
	if v, ok := metrics.get("radarr"); !ok || v != 1 {
		t.Fatalf("regrab unresolved gauge for radarr = (%d, %v), want (1, true)", v, ok)
	}
	if _, ok := metrics.get("homelab"); ok {
		t.Fatal("regrab flagged the sonarr instance as unresolved")
	}

	cancel()
	bgWG.Wait()
}
