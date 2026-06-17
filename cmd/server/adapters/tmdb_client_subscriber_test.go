package adapters

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infraextsvc "github.com/alexmorbo/seasonfill/infrastructure/externalservices"
	"github.com/alexmorbo/seasonfill/infrastructure/tmdb"
)

// newStubTMDBClient builds a real *tmdb.Client (rate limiter + token
// bucket + a fresh http.Client) using a fixture bearer token so tests
// can exercise the Close() / Swap() lifecycle without standing up an
// HTTP server.
func newStubTMDBClient(t *testing.T, token string) *tmdb.Client {
	t.Helper()
	c, err := tmdb.New(tmdb.Config{
		Token:      token,
		HTTPClient: &http.Client{},
		Language:   tmdb.DefaultLanguage,
	})
	require.NoError(t, err)
	return c
}

// TestTMDBClientSubscriber_FirstApplyRebuilds confirms a first call
// with enabled+key swaps the holder + bumps the rebuild counter.
func TestTMDBClientSubscriber_FirstApplyRebuilds(t *testing.T) {
	t.Parallel()
	holder := NewTMDBClientHolder()
	logBuf := &bytes.Buffer{}
	sub := NewTMDBClientSubscriber(holder, TMDBClientFactoryConfig{}, newTestLogger(logBuf)).
		WithCloseDelay(0).
		WithFactoryFn(func(s infraextsvc.Settings) (*tmdb.Client, error) {
			return newStubTMDBClient(t, s.APIKey), nil
		})

	sub.Apply(context.Background(), infraextsvc.Settings{
		Service:     infraextsvc.ServiceTMDB,
		Enabled:     true,
		APIKey:      "token_abcd",
		APIKeyLast4: "abcd",
	})

	require.NotNil(t, sub.Current())
	assert.Equal(t, 1, sub.RebuildCount())
	assert.Contains(t, logBuf.String(), "external_service.client.rebuilt")
	assert.Contains(t, logBuf.String(), `"last4":"abcd"`)
	// Holder's Close happens lazily; let the goroutine drain.
	sub.Wait()
	// First apply had no previous client, so no Close fired.
	sub.Current().Close()
}

// TestTMDBClientSubscriber_SamePayloadNoRebuild covers idempotency.
func TestTMDBClientSubscriber_SamePayloadNoRebuild(t *testing.T) {
	t.Parallel()
	holder := NewTMDBClientHolder()
	sub := NewTMDBClientSubscriber(holder, TMDBClientFactoryConfig{}, newTestLogger(nil)).
		WithCloseDelay(0).
		WithFactoryFn(func(s infraextsvc.Settings) (*tmdb.Client, error) {
			return newStubTMDBClient(t, s.APIKey), nil
		})

	s := infraextsvc.Settings{
		Service:     infraextsvc.ServiceTMDB,
		Enabled:     true,
		APIKey:      "token_abcd",
		APIKeyLast4: "abcd",
	}
	sub.Apply(context.Background(), s)
	first := sub.Current()
	require.NotNil(t, first)

	sub.Apply(context.Background(), s)
	assert.Same(t, first, sub.Current(), "same payload must not swap")
	assert.Equal(t, 1, sub.RebuildCount())

	sub.Wait()
	first.Close()
}

// TestTMDBClientSubscriber_KeyRotationSwapsAndClosesPrevious is the
// happy-path Story 352 scenario: operator rotates the bearer token,
// the subscriber swaps, the previous client gets Close()d after the
// drain window.
func TestTMDBClientSubscriber_KeyRotationSwapsAndClosesPrevious(t *testing.T) {
	t.Parallel()
	holder := NewTMDBClientHolder()

	var closed atomic.Int32
	sub := NewTMDBClientSubscriber(holder, TMDBClientFactoryConfig{}, newTestLogger(nil)).
		WithCloseDelay(0).
		WithCloseFn(func(c *tmdb.Client) {
			closed.Add(1)
			c.Close()
		}).
		WithFactoryFn(func(s infraextsvc.Settings) (*tmdb.Client, error) {
			return newStubTMDBClient(t, s.APIKey), nil
		})

	first := infraextsvc.Settings{
		Service:     infraextsvc.ServiceTMDB,
		Enabled:     true,
		APIKey:      "token_old1",
		APIKeyLast4: "old1",
	}
	sub.Apply(context.Background(), first)
	firstClient := sub.Current()
	require.NotNil(t, firstClient)

	rotated := first
	rotated.APIKey = "token_new1"
	rotated.APIKeyLast4 = "new1"
	sub.Apply(context.Background(), rotated)
	secondClient := sub.Current()

	assert.NotSame(t, firstClient, secondClient, "rotation must swap")
	assert.Equal(t, 2, sub.RebuildCount())

	sub.Wait()
	assert.Equal(t, int32(1), closed.Load(),
		"previous client must be Close()d after swap")

	secondClient.Close()
}

// TestTMDBClientSubscriber_DisabledClearsHolder covers the operator-
// initiated disable: holder returns nil, future GetTV calls fail with
// ErrTMDBClientNotReady.
func TestTMDBClientSubscriber_DisabledClearsHolder(t *testing.T) {
	t.Parallel()
	holder := NewTMDBClientHolder()
	var closed atomic.Int32
	sub := NewTMDBClientSubscriber(holder, TMDBClientFactoryConfig{}, newTestLogger(nil)).
		WithCloseDelay(0).
		WithCloseFn(func(c *tmdb.Client) {
			closed.Add(1)
			c.Close()
		}).
		WithFactoryFn(func(s infraextsvc.Settings) (*tmdb.Client, error) {
			return newStubTMDBClient(t, s.APIKey), nil
		})

	enabled := infraextsvc.Settings{
		Service: infraextsvc.ServiceTMDB,
		Enabled: true,
		APIKey:  "token_abcd",
	}
	sub.Apply(context.Background(), enabled)
	require.NotNil(t, sub.Current())

	disabled := enabled
	disabled.Enabled = false
	sub.Apply(context.Background(), disabled)

	assert.Nil(t, sub.Current(), "disable must clear holder")
	sub.Wait()
	assert.Equal(t, int32(1), closed.Load())

	_, err := holder.GetTV(context.Background(), 42, "en-US")
	require.ErrorIs(t, err, ErrTMDBClientNotReady)
}

// TestTMDBClientSubscriber_NilHolderShortCircuits exercises the boot-
// disabled path: the subscriber sees holder=nil and logs a single warn
// without panicking.
func TestTMDBClientSubscriber_NilHolderShortCircuits(t *testing.T) {
	t.Parallel()
	logBuf := &bytes.Buffer{}
	sub := NewTMDBClientSubscriber(nil, TMDBClientFactoryConfig{}, newTestLogger(logBuf))

	sub.Apply(context.Background(), infraextsvc.Settings{
		Service: infraextsvc.ServiceTMDB,
		Enabled: true,
		APIKey:  "token",
	})

	assert.Contains(t, logBuf.String(), "external_service.client.boot_disabled")
}

// TestTMDBClientSubscriber_FactoryErrorLeavesPrevious covers the
// failure mode.
func TestTMDBClientSubscriber_FactoryErrorLeavesPrevious(t *testing.T) {
	t.Parallel()
	holder := NewTMDBClientHolder()
	logBuf := &bytes.Buffer{}
	factoryErr := errors.New("simulated factory failure")
	var callCount atomic.Int32
	sub := NewTMDBClientSubscriber(holder, TMDBClientFactoryConfig{}, newTestLogger(logBuf)).
		WithCloseDelay(0).
		WithFactoryFn(func(s infraextsvc.Settings) (*tmdb.Client, error) {
			if callCount.Add(1) == 1 {
				return newStubTMDBClient(t, s.APIKey), nil
			}
			return nil, factoryErr
		})

	first := infraextsvc.Settings{
		Service:     infraextsvc.ServiceTMDB,
		Enabled:     true,
		APIKey:      "token_old",
		APIKeyLast4: "_old",
	}
	sub.Apply(context.Background(), first)
	previous := sub.Current()
	require.NotNil(t, previous)

	rotated := first
	rotated.APIKey = "token_new"
	rotated.APIKeyLast4 = "_new"
	sub.Apply(context.Background(), rotated)

	assert.Same(t, previous, sub.Current(), "factory failure must NOT swap")
	assert.True(t, strings.Contains(logBuf.String(), "rebuild_failed"))

	previous.Close()
}

// TestTMDBClientSubscriber_ConcurrentApply stresses the race detector.
func TestTMDBClientSubscriber_ConcurrentApply(t *testing.T) {
	t.Parallel()
	holder := NewTMDBClientHolder()
	sub := NewTMDBClientSubscriber(holder, TMDBClientFactoryConfig{}, newTestLogger(nil)).
		WithCloseDelay(0).
		WithCloseFn(func(c *tmdb.Client) { c.Close() }).
		WithFactoryFn(func(s infraextsvc.Settings) (*tmdb.Client, error) {
			return newStubTMDBClient(t, s.APIKey), nil
		})

	s := infraextsvc.Settings{
		Service: infraextsvc.ServiceTMDB,
		Enabled: true,
		APIKey:  "token_abcd",
	}
	sub.Apply(context.Background(), s)
	require.NotNil(t, sub.Current())

	var wg sync.WaitGroup
	const N = 8
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub.Apply(context.Background(), s)
		}()
	}
	wg.Wait()
	sub.Wait()
	require.NotNil(t, sub.Current())
	sub.Current().Close()
}

// TestTMDBClientSubscriber_CloseDelayHonoured verifies the drain delay
// is respected.
func TestTMDBClientSubscriber_CloseDelayHonoured(t *testing.T) {
	t.Parallel()
	holder := NewTMDBClientHolder()
	var closedAt atomic.Int64
	start := time.Now()
	sub := NewTMDBClientSubscriber(holder, TMDBClientFactoryConfig{}, newTestLogger(nil)).
		WithCloseDelay(50 * time.Millisecond).
		WithCloseFn(func(c *tmdb.Client) {
			closedAt.Store(time.Since(start).Milliseconds())
			c.Close()
		}).
		WithFactoryFn(func(s infraextsvc.Settings) (*tmdb.Client, error) {
			return newStubTMDBClient(t, s.APIKey), nil
		})

	first := infraextsvc.Settings{Service: infraextsvc.ServiceTMDB, Enabled: true, APIKey: "k1"}
	sub.Apply(context.Background(), first)
	require.NotNil(t, sub.Current())

	second := first
	second.APIKey = "k2"
	sub.Apply(context.Background(), second)

	sub.Wait()
	assert.GreaterOrEqual(t, closedAt.Load(), int64(40),
		"close should fire after ~50ms delay")
	sub.Current().Close()
}
