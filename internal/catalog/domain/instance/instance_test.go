package instance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHealth_Constants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, Health("Bootstrapping"), HealthBootstrapping)
	assert.Equal(t, Health("Available"), HealthAvailable)
	assert.Equal(t, Health("SelfThrottled"), HealthSelfThrottled)
	assert.Equal(t, Health("UnavailableAuth"), HealthUnavailableAuth)
	assert.Equal(t, Health("UnavailableNetwork"), HealthUnavailableNetwork)
	assert.Equal(t, Health("UnavailableUnknown"), HealthUnavailableUnknown)
}

func TestHealth_IsAvailable(t *testing.T) {
	t.Parallel()
	assert.True(t, HealthAvailable.IsAvailable())
	// SelfThrottled is a transient self-imposed delay; the upstream
	// backend is reachable so scans are allowed to proceed.
	assert.True(t, HealthSelfThrottled.IsAvailable())
	// Story 488 (B-14): Bootstrapping must NOT be available — scans
	// are gated until the first preflight confirms reachability.
	assert.False(t, HealthBootstrapping.IsAvailable())
	assert.False(t, HealthUnavailableAuth.IsAvailable())
	assert.False(t, HealthUnavailableNetwork.IsAvailable())
	assert.False(t, HealthUnavailableUnknown.IsAvailable())
}

func TestHealth_IsUnavailable(t *testing.T) {
	t.Parallel()
	assert.False(t, HealthAvailable.IsUnavailable())
	assert.False(t, HealthSelfThrottled.IsUnavailable())
	// Story 488 (B-14): Bootstrapping is the inverse of IsAvailable.
	assert.True(t, HealthBootstrapping.IsUnavailable())
	assert.True(t, HealthUnavailableAuth.IsUnavailable())
	assert.True(t, HealthUnavailableNetwork.IsUnavailable())
	assert.True(t, HealthUnavailableUnknown.IsUnavailable())
}

// TestHealth_IsAvailable_BootstrappingFalse is a regression guard for
// Story 488 (B-14): the new Bootstrapping state gates scans by
// returning false from IsAvailable(); existing Available/SelfThrottled
// behaviour is preserved.
func TestHealth_IsAvailable_BootstrappingFalse(t *testing.T) {
	t.Parallel()
	assert.False(t, HealthBootstrapping.IsAvailable(),
		"HealthBootstrapping must NOT be IsAvailable() — scans must not start before preflight")
	assert.True(t, HealthAvailable.IsAvailable(),
		"HealthAvailable must be IsAvailable() — regression guard")
	assert.True(t, HealthSelfThrottled.IsAvailable(),
		"HealthSelfThrottled must be IsAvailable() — regression guard")
}

func TestSnapshot_Fields(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	s := Snapshot{
		Name:             "main",
		Health:           HealthAvailable,
		LastCheckAt:      now,
		LastError:        "",
		TransitionsCount: 1,
	}
	assert.Equal(t, "main", s.Name)
	assert.Equal(t, HealthAvailable, s.Health)
	assert.Equal(t, now, s.LastCheckAt)
	assert.Empty(t, s.LastError)
	assert.Equal(t, 1, s.TransitionsCount)
}
