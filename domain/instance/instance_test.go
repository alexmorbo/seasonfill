package instance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHealth_Constants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, Health("Available"), HealthAvailable)
	assert.Equal(t, Health("UnavailableAuth"), HealthUnavailableAuth)
	assert.Equal(t, Health("UnavailableNetwork"), HealthUnavailableNetwork)
	assert.Equal(t, Health("UnavailableUnknown"), HealthUnavailableUnknown)
}

func TestHealth_IsAvailable(t *testing.T) {
	t.Parallel()
	assert.True(t, HealthAvailable.IsAvailable())
	assert.False(t, HealthUnavailableAuth.IsAvailable())
	assert.False(t, HealthUnavailableNetwork.IsAvailable())
	assert.False(t, HealthUnavailableUnknown.IsAvailable())
}

func TestHealth_IsUnavailable(t *testing.T) {
	t.Parallel()
	assert.False(t, HealthAvailable.IsUnavailable())
	assert.True(t, HealthUnavailableAuth.IsUnavailable())
	assert.True(t, HealthUnavailableNetwork.IsUnavailable())
	assert.True(t, HealthUnavailableUnknown.IsUnavailable())
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
