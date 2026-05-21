package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_ZeroZeroIsNil(t *testing.T) {
	t.Parallel()
	assert.Nil(t, New(0, 0))
}

func TestNew_NonZeroNotNil(t *testing.T) {
	t.Parallel()
	assert.NotNil(t, New(5, 10))
	assert.NotNil(t, New(0.1, 1))
}

func TestNew_NegativeRPSDefaults(t *testing.T) {
	t.Parallel()
	l := New(-1, 5)
	require.NotNil(t, l)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	assert.NoError(t, l.Wait(ctx))
}

func TestNew_BurstZeroDefaultsToOne(t *testing.T) {
	t.Parallel()
	l := New(10, 0)
	require.NotNil(t, l)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	assert.NoError(t, l.Wait(ctx))
}

func TestWait_NilLimiterReturnsImmediately(t *testing.T) {
	t.Parallel()
	require.NoError(t, Wait(nil, context.Background()))
}

func TestWait_RespectsBurst(t *testing.T) {
	t.Parallel()
	l := New(1000, 5)
	require.NotNil(t, l)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := 0; i < 5; i++ {
		assert.NoError(t, l.Wait(ctx))
	}
}

func TestNewFromRPM_ZeroIsNil(t *testing.T) {
	t.Parallel()
	assert.Nil(t, NewFromRPM(0, 0))
}

func TestNewFromRPM_NegativeIsNil(t *testing.T) {
	t.Parallel()
	assert.Nil(t, NewFromRPM(-5, -1))
}

func TestNewFromRPM_30RPM(t *testing.T) {
	t.Parallel()
	l := NewFromRPM(30, 5)
	require.NotNil(t, l)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	assert.NoError(t, l.Wait(ctx))
}

func TestWait_ContextCancel(t *testing.T) {
	t.Parallel()
	l := New(0.001, 1)
	require.NotNil(t, l)
	// Burn the first token.
	ctx0, cancel0 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel0()
	_ = l.Wait(ctx0)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := l.Wait(ctx)
	assert.Error(t, err)
}

func TestObserver_NotCalledOnInstantAcquire(t *testing.T) {
	t.Parallel()
	var calls int
	l := NewWithOptions(1000, 10, WithObserver("per_instance", func(string) { calls++ }))
	require.NotNil(t, l)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := 0; i < 5; i++ {
		require.NoError(t, l.Wait(ctx))
	}
	assert.Equal(t, 0, calls, "observer must not fire on instant-acquire fast path")
}

func TestObserver_CalledOnceWhenBlocked(t *testing.T) {
	t.Parallel()
	var (
		calls  int
		scopes []string
	)
	// 5 rps, burst 1 → second call must wait ~200 ms.
	l := NewWithOptions(5, 1, WithObserver("per_instance", func(s string) {
		calls++
		scopes = append(scopes, s)
	}))
	require.NotNil(t, l)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.NoError(t, l.Wait(ctx))
	require.Equal(t, 0, calls, "first call (burst) should be instant")

	start := time.Now()
	require.NoError(t, l.Wait(ctx))
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 150*time.Millisecond, "second call should have blocked")
	require.Equal(t, 1, calls, "observer should fire exactly once for one blocked call")
	assert.Equal(t, []string{"per_instance"}, scopes)
}

func TestObserver_FiresEvenWhenCtxCancelsDuringSleep(t *testing.T) {
	t.Parallel()
	var calls int
	// 0.5 rps, burst 1 → second call wait ~2 s, but ctx fires at 50 ms.
	l := NewWithOptions(0.5, 1, WithObserver("global", func(string) { calls++ }))
	require.NotNil(t, l)

	// Burn the burst.
	bctx, bcancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer bcancel()
	require.NoError(t, l.Wait(bctx))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := l.Wait(ctx)
	assert.Error(t, err, "ctx cancel during sleep should return ctx error")
	// The observer fires before the sleep begins — ctx cancel during the
	// sleep does NOT unwind it. This intentionally captures the "we tried
	// to throttle" signal even if the caller's request was abandoned.
	assert.Equal(t, 1, calls, "observer should still fire when sleep is cancelled by ctx")
}

func TestObserver_NilOptionIsNoOp(t *testing.T) {
	t.Parallel()
	// Both nil observer and absent option must be safe.
	l1 := NewWithOptions(1000, 10, WithObserver("per_instance", nil))
	require.NotNil(t, l1)
	l2 := NewWithOptions(1000, 10)
	require.NotNil(t, l2)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, l1.Wait(ctx))
	require.NoError(t, l2.Wait(ctx))
}

func TestNewFromRPMWithOptions_AttachesObserver(t *testing.T) {
	t.Parallel()
	var calls int
	// 60 rpm = 1 rps, burst 1 → second call waits ~1 s, but we just check
	// the observer fires.
	l := NewFromRPMWithOptions(60, 1, WithObserver("global", func(string) { calls++ }))
	require.NotNil(t, l)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, l.Wait(ctx))
	require.NoError(t, l.Wait(ctx))
	assert.Equal(t, 1, calls)
}
