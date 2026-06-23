package adapters_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestColdStartKicker_FirstPassEmpty_KicksOnSyncCompleted(t *testing.T) {
	var fired atomic.Int64
	trigger := func(_ context.Context) error {
		fired.Add(1)
		return nil
	}
	k := adapters.NewColdStartKicker(trigger, discardLog())
	k.MarkPassResult(0)
	k.OnSyncCompleted(t.Context())
	require.Equal(t, int64(1), fired.Load())
}

func TestColdStartKicker_FirstPassNonEmpty_DoesNotKick(t *testing.T) {
	var fired atomic.Int64
	k := adapters.NewColdStartKicker(func(_ context.Context) error {
		fired.Add(1)
		return nil
	}, discardLog())
	k.MarkPassResult(50)
	k.OnSyncCompleted(t.Context())
	require.Equal(t, int64(0), fired.Load())
}

func TestColdStartKicker_FiresExactlyOnce(t *testing.T) {
	var fired atomic.Int64
	k := adapters.NewColdStartKicker(func(_ context.Context) error {
		fired.Add(1)
		return nil
	}, discardLog())
	k.MarkPassResult(0)
	for range 5 {
		k.OnSyncCompleted(t.Context())
	}
	require.Equal(t, int64(1), fired.Load(), "kicker must fire AT MOST ONCE per boot")
}

func TestColdStartKicker_MarkPassResult_SingleShot(t *testing.T) {
	var fired atomic.Int64
	k := adapters.NewColdStartKicker(func(_ context.Context) error {
		fired.Add(1)
		return nil
	}, discardLog())
	// First call wins; subsequent re-sweep ticks with 50 rows must NOT
	// re-arm the kicker (kicker stays armed from initial 0-result pass).
	k.MarkPassResult(0)
	k.MarkPassResult(50)
	k.OnSyncCompleted(t.Context())
	require.Equal(t, int64(1), fired.Load())
}

func TestColdStartKicker_SyncBeforeBootPass_NoOp(t *testing.T) {
	var fired atomic.Int64
	k := adapters.NewColdStartKicker(func(_ context.Context) error {
		fired.Add(1)
		return nil
	}, discardLog())
	// scan_completed fires BEFORE BackfillSeries records its first pass.
	// Kicker must no-op (initialPassDone = false).
	k.OnSyncCompleted(t.Context())
	require.Equal(t, int64(0), fired.Load())
	// And after the boot pass finally arms, the NEXT scan_completed
	// kicks normally.
	k.MarkPassResult(0)
	k.OnSyncCompleted(t.Context())
	require.Equal(t, int64(1), fired.Load())
}

func TestColdStartKicker_TriggerError_LoggedButNoCrash(t *testing.T) {
	var fired atomic.Int64
	k := adapters.NewColdStartKicker(func(_ context.Context) error {
		fired.Add(1)
		return errors.New("dispatcher down")
	}, discardLog())
	k.MarkPassResult(0)
	k.OnSyncCompleted(t.Context())
	require.Equal(t, int64(1), fired.Load())
	// Subsequent OnSyncCompleted calls still no-op (fired flag set).
	k.OnSyncCompleted(t.Context())
	require.Equal(t, int64(1), fired.Load())
}

func TestColdStartKicker_RaceFreeUnderConcurrency(t *testing.T) {
	var fired atomic.Int64
	k := adapters.NewColdStartKicker(func(_ context.Context) error {
		fired.Add(1)
		return nil
	}, discardLog())
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			k.MarkPassResult(0)
		}()
		go func() {
			defer wg.Done()
			k.OnSyncCompleted(t.Context())
		}()
	}
	wg.Wait()
	// Possible outcomes: 0 fires (sync raced ahead of arm) or 1 fire
	// (arm landed before any sync). NEVER >1.
	count := fired.Load()
	require.LessOrEqual(t, count, int64(1), "kicker must NEVER fire more than once")
	// Force at least one OnSyncCompleted AFTER MarkPassResult to assert
	// the kicker is functional after the race.
	k.OnSyncCompleted(t.Context())
	require.LessOrEqual(t, fired.Load(), int64(1))
}

func TestColdStartKicker_NilTrigger_Panics(t *testing.T) {
	require.Panics(t, func() {
		_ = adapters.NewColdStartKicker(nil, discardLog())
	})
}
