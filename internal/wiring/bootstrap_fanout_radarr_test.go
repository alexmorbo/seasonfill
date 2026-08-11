package wiring

import (
	"context"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	watchdog "github.com/alexmorbo/seasonfill/internal/watchdog/infrastructure"
)

// fanoutFakeChecker is a no-op reload.HealthChecker for the fanout test.
type fanoutFakeChecker struct{}

func (fanoutFakeChecker) ReplaceClients([]ports.ArrHealthProbe, []string) {}
func (fanoutFakeChecker) Preflight(context.Context)                       {}

func newFanoutForTest(radarrSyncUC *scan.RadarrSyncUseCase, radarrHolder *adapters.RadarrInstanceMapHolder) func(runtime.Snapshot, map[string]ports.SonarrClient) {
	scanUC := &scan.UseCase{}
	sonarrHolder := adapters.NewInstanceMapHolder(nil)
	wd := watchdog.New(nil, nil, nil, nil)
	return BuildOnAppliedFanout(
		context.Background(),
		scanUC,
		radarrSyncUC,
		radarrHolder,
		sonarrHolder,
		fanoutFakeChecker{},
		wd,
		nil, // sweeper (guarded)
		nil, // regrabLoop (guarded)
		nil, // torrentsyncLoop (guarded)
		nil, // qbitLoader (guarded)
		nil, // logger — unused on the paths exercised here
	)
}

// TestFanout_RadarrInstance_LandsInHolder proves the REST-registry gap is closed:
// a type='radarr' instance in the published snapshot is registered into the
// radarr holder (Load returns it with a live Client).
func TestFanout_RadarrInstance_LandsInHolder(t *testing.T) {
	radarrHolder := adapters.NewRadarrInstanceMapHolder(nil)
	syncUC := scan.NewRadarrSyncUseCase(nil, scan.RadarrSyncDeps{})
	fanout := newFanoutForTest(syncUC, radarrHolder)

	snap := runtime.Snapshot{
		Instances: []runtime.InstanceSnapshot{
			{Name: "movies", Type: "radarr", URL: "http://radarr:7878", APIKey: "k", Timeout: 5 * time.Second},
		},
	}
	fanout(snap, nil)

	got := radarrHolder.Load()
	inst, ok := got["movies"]
	if !ok {
		t.Fatalf("radarr holder missing 'movies' after fanout; got keys=%v", keysOf(got))
	}
	if inst.Client == nil {
		t.Fatalf("radarr holder 'movies' has nil Client")
	}
	if inst.Config.Name != "movies" || inst.Config.Type != "radarr" {
		t.Fatalf("radarr holder config mismatch: %+v", inst.Config)
	}
}

// TestFanout_PureSonarr_LeavesRadarrHolderEmpty proves the sonarr path is
// unaffected: a snapshot without radarr rows leaves the radarr holder empty.
func TestFanout_PureSonarr_LeavesRadarrHolderEmpty(t *testing.T) {
	radarrHolder := adapters.NewRadarrInstanceMapHolder(nil)
	syncUC := scan.NewRadarrSyncUseCase(nil, scan.RadarrSyncDeps{})
	fanout := newFanoutForTest(syncUC, radarrHolder)

	// No instances → radarr partition empty. (Sonarr rows require a client in
	// the clients map; the radarr registration path under test is independent.)
	fanout(runtime.Snapshot{Instances: nil}, nil)

	if got := radarrHolder.Load(); len(got) != 0 {
		t.Fatalf("radarr holder expected empty for pure-sonarr snapshot, got %v", keysOf(got))
	}
}

// TestFanout_NilRadarrHolder_NoPanic proves the guard tolerates a nil radarr
// holder (minimal wirings) without panicking.
func TestFanout_NilRadarrHolder_NoPanic(t *testing.T) {
	syncUC := scan.NewRadarrSyncUseCase(nil, scan.RadarrSyncDeps{})
	fanout := newFanoutForTest(syncUC, nil)
	snap := runtime.Snapshot{
		Instances: []runtime.InstanceSnapshot{
			{Name: "movies", Type: "radarr", URL: "http://radarr:7878", APIKey: "k", Timeout: 5 * time.Second},
		},
	}
	fanout(snap, nil) // must not panic
}

func keysOf(m map[string]scan.RadarrInstance) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
