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
}
