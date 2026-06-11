package cooldown

import (
	"fmt"
	"strings"
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

func TestClampReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		in        string
		wantLen   int
		wantTrunc bool
	}{
		{
			name:    "empty unchanged",
			in:      "",
			wantLen: 0,
		},
		{
			name:    "short literal unchanged",
			in:      "guid_after_failed_grab",
			wantLen: len("guid_after_failed_grab"),
		},
		{
			name:    "exactly at cap unchanged",
			in:      strings.Repeat("a", ReasonMaxBytes),
			wantLen: ReasonMaxBytes,
		},
		{
			name:      "one byte over the cap is truncated",
			in:        strings.Repeat("a", ReasonMaxBytes+1),
			wantLen:   ReasonMaxBytes + len("…(truncated 1 bytes)"),
			wantTrunc: true,
		},
		{
			name:      "realistic 4KiB sonarr body is truncated",
			in:        strings.Repeat("x", 4096),
			wantLen:   ReasonMaxBytes + len(fmt.Sprintf("…(truncated %d bytes)", 4096-ReasonMaxBytes)),
			wantTrunc: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ClampReason(tc.in)
			assert.Equal(t, tc.wantLen, len(got), "len mismatch for %q", tc.name)
			if tc.wantTrunc {
				assert.Contains(t, got, "…(truncated", "truncated output must carry the marker")
			} else {
				assert.Equal(t, tc.in, got, "below-cap input must pass through verbatim")
			}
		})
	}
}
