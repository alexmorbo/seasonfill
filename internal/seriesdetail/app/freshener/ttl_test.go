package freshener

// Internal-test package (white-box) — ttlVerdict is unexported.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTTLVerdict(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { ts := now.Add(-d); return &ts }
	statusPtr := func(s string) *string { return &s }

	policy := TTLPolicy{Floor: 24 * time.Hour, Ceiling: 7 * 24 * time.Hour, StatusAware: true}

	tests := []struct {
		name   string
		synced *time.Time
		status *string
		policy TTLPolicy
		stale  bool
		reason string
	}{
		{"nil synced → never", nil, nil, policy, true, "never"},
		{"age=0 → fresh", ago(0), nil, policy, false, "fresh"},
		{"age=1h ended → fresh", ago(time.Hour), statusPtr("Ended"), policy, false, "fresh"},
		{"age=2d ended → fresh (below ceiling)", ago(48 * time.Hour), statusPtr("Ended"), policy, false, "fresh"},
		{"age=2d returning → status (above floor)", ago(48 * time.Hour), statusPtr("Returning Series"), policy, true, "status"},
		{"age=2d in production → status", ago(48 * time.Hour), statusPtr("In Production"), policy, true, "status"},
		{"age=8d ended → expired (above ceiling)", ago(8 * 24 * time.Hour), statusPtr("Ended"), policy, true, "expired"},
		{"age=8d returning → expired (ceiling beats status)", ago(8 * 24 * time.Hour), statusPtr("Returning Series"), policy, true, "expired"},
		{"status-unaware policy ignores status",
			ago(48 * time.Hour), statusPtr("Returning Series"),
			TTLPolicy{Floor: 24 * time.Hour, Ceiling: 7 * 24 * time.Hour}, // no StatusAware
			false, "fresh"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stale, reason := ttlVerdict(tc.synced, tc.status, tc.policy, now)
			assert.Equal(t, tc.stale, stale)
			assert.Equal(t, tc.reason, reason)
		})
	}
}

func TestIsReturning(t *testing.T) {
	t.Parallel()
	statusPtr := func(s string) *string { return &s }
	assert.False(t, isReturning(nil))
	assert.False(t, isReturning(statusPtr("Ended")))
	assert.False(t, isReturning(statusPtr("Canceled")))
	assert.True(t, isReturning(statusPtr("Returning Series")))
	assert.True(t, isReturning(statusPtr("In Production")))
}
