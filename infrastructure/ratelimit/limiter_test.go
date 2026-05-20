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
