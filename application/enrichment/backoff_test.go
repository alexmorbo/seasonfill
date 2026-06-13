package enrichment

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNextRetry_FirstFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	attempts, next := NextRetry(0, now)
	assert.Equal(t, 1, attempts)
	assert.Equal(t, now.Add(2*time.Hour), next, "attempts=1 ⇒ 1h<<1 = 2h")
}

func TestNextRetry_IncrementsAttempts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	attempts, next := NextRetry(5, now)
	assert.Equal(t, 6, attempts)
	assert.Equal(t, now.Add(24*time.Hour), next, "attempts≥5 ⇒ clamped to 24h")
}
