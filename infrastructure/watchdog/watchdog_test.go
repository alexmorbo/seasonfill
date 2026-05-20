package watchdog

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/domain/instance"
	"github.com/alexmorbo/seasonfill/internal/config"
)

type fakeReg struct {
	mu       sync.Mutex
	snapshot []instance.Snapshot
}

func (f *fakeReg) Snapshot() []instance.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]instance.Snapshot, len(f.snapshot))
	copy(out, f.snapshot)
	return out
}

type fakeChecker struct {
	calls int64
	names []string
	mu    sync.Mutex
}

func (f *fakeChecker) RecheckByName(_ context.Context, name string) {
	atomic.AddInt64(&f.calls, 1)
	f.mu.Lock()
	f.names = append(f.names, name)
	f.mu.Unlock()
}

func TestWatchdog_SkipsAvailable(t *testing.T) {
	t.Parallel()
	reg := &fakeReg{snapshot: []instance.Snapshot{
		{Name: "a", Health: instance.HealthAvailable},
	}}
	ch := &fakeChecker{}
	w := New(reg, ch, slog.New(slog.NewJSONHandler(io.Discard, nil)), map[string]config.HealthCheckConfig{
		"a": {RecheckIntervalAuth: 50 * time.Millisecond, RecheckIntervalNetwork: 50 * time.Millisecond},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	w.Run(ctx)
	assert.EqualValues(t, 0, atomic.LoadInt64(&ch.calls))
}

func TestWatchdog_RechecksUnavailable(t *testing.T) {
	t.Parallel()
	reg := &fakeReg{snapshot: []instance.Snapshot{
		{Name: "a", Health: instance.HealthUnavailableNetwork},
	}}
	ch := &fakeChecker{}
	w := New(reg, ch, slog.New(slog.NewJSONHandler(io.Discard, nil)), map[string]config.HealthCheckConfig{
		"a": {RecheckIntervalAuth: 50 * time.Millisecond, RecheckIntervalNetwork: 50 * time.Millisecond},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	w.Run(ctx)
	got := atomic.LoadInt64(&ch.calls)
	assert.GreaterOrEqual(t, got, int64(2))
}

func TestWatchdog_HonorsStateInterval(t *testing.T) {
	t.Parallel()
	reg := &fakeReg{snapshot: []instance.Snapshot{
		{Name: "auth", Health: instance.HealthUnavailableAuth},
		{Name: "net", Health: instance.HealthUnavailableNetwork},
	}}
	ch := &fakeChecker{}
	w := New(reg, ch, slog.New(slog.NewJSONHandler(io.Discard, nil)), map[string]config.HealthCheckConfig{
		"auth": {RecheckIntervalAuth: time.Second, RecheckIntervalNetwork: 50 * time.Millisecond},
		"net":  {RecheckIntervalAuth: time.Second, RecheckIntervalNetwork: 50 * time.Millisecond},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	w.Run(ctx)
	ch.mu.Lock()
	defer ch.mu.Unlock()
	netCalls := 0
	authCalls := 0
	for _, n := range ch.names {
		if n == "net" {
			netCalls++
		}
		if n == "auth" {
			authCalls++
		}
	}
	assert.Greater(t, netCalls, authCalls, "network should recheck more often than auth in this window")
}

func TestWatchdog_ShortestInterval(t *testing.T) {
	t.Parallel()
	w := New(nil, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)), map[string]config.HealthCheckConfig{
		"a": {RecheckIntervalAuth: 5 * time.Minute, RecheckIntervalNetwork: 1 * time.Minute},
		"b": {RecheckIntervalAuth: 5 * time.Minute, RecheckIntervalNetwork: 30 * time.Second},
	})
	assert.Equal(t, 30*time.Second, w.shortest())
}

func TestWatchdog_ShortestInterval_AllZeroFallsBackToMinute(t *testing.T) {
	t.Parallel()
	w := New(nil, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)), map[string]config.HealthCheckConfig{
		"a": {RecheckIntervalAuth: 0, RecheckIntervalNetwork: 0},
	})
	// shortest() returns 0; Run() falls back to time.Minute internally.
	assert.Equal(t, time.Duration(0), w.shortest())
}

func TestWatchdog_StopsOnContextCancel(t *testing.T) {
	t.Parallel()
	reg := &fakeReg{}
	ch := &fakeChecker{}
	w := New(reg, ch, slog.New(slog.NewJSONHandler(io.Discard, nil)), map[string]config.HealthCheckConfig{
		"a": {RecheckIntervalAuth: time.Hour, RecheckIntervalNetwork: time.Hour},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not stop after cancel")
	}
}

func TestWatchdog_UnconfiguredInstanceSkipped(t *testing.T) {
	t.Parallel()
	reg := &fakeReg{snapshot: []instance.Snapshot{
		{Name: "ghost", Health: instance.HealthUnavailableNetwork},
	}}
	ch := &fakeChecker{}
	// No config entry for "ghost".
	w := New(reg, ch, slog.New(slog.NewJSONHandler(io.Discard, nil)), map[string]config.HealthCheckConfig{
		"other": {RecheckIntervalNetwork: 50 * time.Millisecond},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	w.Run(ctx)
	assert.EqualValues(t, 0, atomic.LoadInt64(&ch.calls))
}
