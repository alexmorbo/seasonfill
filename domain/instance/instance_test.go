package instance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStatus_Constants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, Status("unknown"), StatusUnknown)
	assert.Equal(t, Status("available"), StatusAvailable)
	assert.Equal(t, Status("unavailable"), StatusUnavailable)
}

func TestHealth_StructFields(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	h := Health{
		Name:      "main",
		Status:    StatusAvailable,
		LastError: "",
		CheckedAt: now,
	}
	assert.Equal(t, "main", h.Name)
	assert.Equal(t, StatusAvailable, h.Status)
	assert.Empty(t, h.LastError)
	assert.Equal(t, now, h.CheckedAt)
}
