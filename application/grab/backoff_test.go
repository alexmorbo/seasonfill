package grab

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBackoffFor_DefaultProgression(t *testing.T) {
	t.Parallel()
	// init=0 falls back to 1s/5s/30s.
	assert.Equal(t, time.Second, backoffFor(1, 0, 30*time.Second))
	assert.Equal(t, 5*time.Second, backoffFor(2, 0, 30*time.Second))
	assert.Equal(t, 30*time.Second, backoffFor(3, 0, 30*time.Second))
	assert.Equal(t, 30*time.Second, backoffFor(10, 0, 30*time.Second))
}

func TestBackoffFor_HonorsInitialBackoff(t *testing.T) {
	t.Parallel()
	// init=2s -> 2s, 10s, 60s (capped by max).
	assert.Equal(t, 2*time.Second, backoffFor(1, 2*time.Second, 60*time.Second))
	assert.Equal(t, 10*time.Second, backoffFor(2, 2*time.Second, 60*time.Second))
	assert.Equal(t, 60*time.Second, backoffFor(3, 2*time.Second, 60*time.Second))
}

func TestBackoffFor_CapsAtMax(t *testing.T) {
	t.Parallel()
	// init=10s, max=15s -> 10s, 15s (capped), 15s (capped).
	assert.Equal(t, 10*time.Second, backoffFor(1, 10*time.Second, 15*time.Second))
	assert.Equal(t, 15*time.Second, backoffFor(2, 10*time.Second, 15*time.Second))
	assert.Equal(t, 15*time.Second, backoffFor(3, 10*time.Second, 15*time.Second))
}

func TestBackoffFor_ZeroMaxFallsBackTo30s(t *testing.T) {
	t.Parallel()
	assert.Equal(t, time.Second, backoffFor(1, time.Second, 0))
	assert.Equal(t, 5*time.Second, backoffFor(2, time.Second, 0))
	assert.Equal(t, 30*time.Second, backoffFor(3, time.Second, 0))
}
