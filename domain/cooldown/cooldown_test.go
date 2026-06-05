package cooldown

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSeriesKey(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "main:122:2", SeriesKey("main", 122, 2))
	assert.Equal(t, ":0:0", SeriesKey("", 0, 0))
}

func TestGUIDKey(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "abc", GUIDKey("abc"))
	assert.Equal(t, "", GUIDKey(""))
}

func TestIsActive(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		expiry time.Time
		want   bool
	}{
		{"future expiry is active", now.Add(time.Hour), true},
		{"past expiry is inactive", now.Add(-time.Hour), false},
		{"equal expiry is inactive", now, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := Cooldown{ExpiresAt: tt.expiry}
			assert.Equal(t, tt.want, c.IsActive(now))
		})
	}
}

func TestScope_Constants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, Scope("series"), ScopeSeries)
	assert.Equal(t, Scope("guid"), ScopeGUID)
	assert.Equal(t, Scope("regrab_retry"), ScopeRegrabRetry)
}

// TestSeriesKey_ReusableForRegrabRetry pins the key-shape contract
// documented in parent story 039 D-T7: the Watchdog regrab loop uses
// SeriesKey verbatim with the new ScopeRegrabRetry. Adding a separate
// key helper would diverge from this contract.
func TestSeriesKey_ReusableForRegrabRetry(t *testing.T) {
	t.Parallel()
	key := SeriesKey("alpha", 122, 2)
	assert.Equal(t, "alpha:122:2", key)
	// Same call site, different scope — both rows are independent.
	a := Cooldown{Scope: ScopeSeries, Key: key}
	b := Cooldown{Scope: ScopeRegrabRetry, Key: key}
	assert.NotEqual(t, a.Scope, b.Scope)
	assert.Equal(t, a.Key, b.Key)
}
