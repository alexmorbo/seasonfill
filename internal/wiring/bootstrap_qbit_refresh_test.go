package wiring

import (
	"context"
	"testing"

	"github.com/alexmorbo/seasonfill/internal/watchdog/app/regrab"
)

// qbitRefreshFakeLoader records the ctx it was handed so the
// cancellation-detach behaviour can be asserted.
type qbitRefreshFakeLoader struct {
	m      map[string]regrab.Settings
	calls  int
	ctxErr error
	sawCtx bool
}

func (f *qbitRefreshFakeLoader) Load(ctx context.Context) map[string]regrab.Settings {
	f.calls++
	f.sawCtx = true
	f.ctxErr = ctx.Err()
	return f.m
}

type qbitRefreshFakeSwapper struct {
	got        map[string]regrab.Settings
	calls      int
	panicOnRun bool
}

func (f *qbitRefreshFakeSwapper) SwapSettings(m map[string]regrab.Settings) {
	f.calls++
	f.got = m
	if f.panicOnRun {
		panic("swap exploded")
	}
}

func fixtureSettings() map[string]regrab.Settings {
	return map[string]regrab.Settings{
		"movies": {InstanceName: "movies", Enabled: true, PollInterval: 30},
	}
}

// The headline wiring test: the refresher loads once and hands the SAME
// map to both loops — which is what actually spawns the goroutines for a
// newly-enabled (radarr or sonarr) instance.
func TestBuildQbitLoopRefresher_SwapsBothLoopsWithLoadedSettings(t *testing.T) {
	loader := &qbitRefreshFakeLoader{m: fixtureSettings()}
	rg := &qbitRefreshFakeSwapper{}
	ts := &qbitRefreshFakeSwapper{}

	BuildQbitLoopRefresher(rg, ts, loader, nil)(context.Background())

	if loader.calls != 1 {
		t.Fatalf("loader calls = %d, want 1 (one Load per refresh, shared by both loops)", loader.calls)
	}
	if rg.calls != 1 || ts.calls != 1 {
		t.Fatalf("swapper calls: regrab=%d torrentsync=%d, want 1/1", rg.calls, ts.calls)
	}
	if _, ok := rg.got["movies"]; !ok {
		t.Fatalf("regrab loop did not receive the loaded settings: %v", rg.got)
	}
	if _, ok := ts.got["movies"]; !ok {
		t.Fatalf("torrentsync loop did not receive the loaded settings: %v", ts.got)
	}
	if !rg.got["movies"].Enabled {
		t.Fatalf("regrab loop received enabled=false; the loop would never spawn")
	}
}

// A client that disconnected mid-PUT must NOT cause an empty settings map
// (which would stop every running loop). The refresher detaches
// cancellation, so the loader sees a live context.
func TestBuildQbitLoopRefresher_DetachesRequestCancellation(t *testing.T) {
	loader := &qbitRefreshFakeLoader{m: fixtureSettings()}
	rg := &qbitRefreshFakeSwapper{}
	ts := &qbitRefreshFakeSwapper{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulate the request context already being done

	BuildQbitLoopRefresher(rg, ts, loader, nil)(ctx)

	if !loader.sawCtx {
		t.Fatal("loader was never called on a cancelled request context")
	}
	if loader.ctxErr != nil {
		t.Fatalf("loader saw a cancelled context (%v); WithoutCancel must detach it", loader.ctxErr)
	}
	if rg.calls != 1 || ts.calls != 1 {
		t.Fatalf("swapper calls: regrab=%d torrentsync=%d, want 1/1", rg.calls, ts.calls)
	}
}

// A panicking loop must not escape into the HTTP handler — the settings
// row is already committed at that point.
func TestBuildQbitLoopRefresher_RecoversPanic(t *testing.T) {
	loader := &qbitRefreshFakeLoader{m: fixtureSettings()}
	rg := &qbitRefreshFakeSwapper{panicOnRun: true}
	ts := &qbitRefreshFakeSwapper{}

	BuildQbitLoopRefresher(rg, ts, loader, nil)(context.Background()) // must not panic

	if rg.calls != 1 {
		t.Fatalf("regrab swapper calls = %d, want 1", rg.calls)
	}
	// The panic unwinds before torrentsync is reached — documented, not a
	// regression: production SwapSettings does not panic, and the recover
	// exists only so a settings write can never 500.
	if ts.calls != 0 {
		t.Fatalf("torrentsync swapper calls = %d, want 0 (panic unwinds first)", ts.calls)
	}
}

func TestBuildQbitLoopRefresher_NilLoader_IsNoOp(t *testing.T) {
	rg := &qbitRefreshFakeSwapper{}
	ts := &qbitRefreshFakeSwapper{}

	BuildQbitLoopRefresher(rg, ts, nil, nil)(context.Background())

	if rg.calls != 0 || ts.calls != 0 {
		t.Fatalf("swapper calls: regrab=%d torrentsync=%d, want 0/0", rg.calls, ts.calls)
	}
}

// The shared body's guards — same ones the OnApplied fanout relies on.
func TestRefreshQbitLoops_Guards(t *testing.T) {
	loader := &qbitRefreshFakeLoader{m: fixtureSettings()}
	if n := refreshQbitLoops(context.Background(), nil, nil, loader); n != 0 {
		t.Fatalf("both loops nil: n = %d, want 0", n)
	}
	if loader.calls != 0 {
		t.Fatalf("both loops nil: loader must not be called, calls = %d", loader.calls)
	}

	rg := &qbitRefreshFakeSwapper{}
	if n := refreshQbitLoops(context.Background(), rg, nil, nil); n != 0 {
		t.Fatalf("nil loader: n = %d, want 0", n)
	}
	if rg.calls != 0 {
		t.Fatalf("nil loader: swapper must not be called, calls = %d", rg.calls)
	}

	// Only the regrab loop wired — still refreshes it.
	loader2 := &qbitRefreshFakeLoader{m: fixtureSettings()}
	rg2 := &qbitRefreshFakeSwapper{}
	if n := refreshQbitLoops(context.Background(), rg2, nil, loader2); n != 1 {
		t.Fatalf("regrab-only: n = %d, want 1", n)
	}
	if rg2.calls != 1 {
		t.Fatalf("regrab-only: swapper calls = %d, want 1", rg2.calls)
	}
}
