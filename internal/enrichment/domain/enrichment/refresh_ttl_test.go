package enrichment

import (
	"testing"
	"time"
)

// TestRefreshTTL_For pins the per-tier durations, including the ADR-0015 Ф3 C1
// Followed tier (10d) and the renumber-safe Normal/Cold values. Changed has no
// TTL and falls through to Cold.
func TestRefreshTTL_For(t *testing.T) {
	t.Parallel()
	ttl := DefaultRefreshTTL()
	cases := []struct {
		name string
		tier RefreshTier
		want time.Duration
	}{
		{"hot", RefreshTierHot, 7 * 24 * time.Hour},
		{"followed", RefreshTierFollowed, 10 * 24 * time.Hour},
		{"normal", RefreshTierNormal, 14 * 24 * time.Hour},
		{"cold", RefreshTierCold, 30 * 24 * time.Hour},
		{"changed-falls-through-to-cold", RefreshTierChanged, 30 * 24 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ttl.For(tc.tier); got != tc.want {
				t.Fatalf("For(%v) = %v, want %v", tc.tier, got, tc.want)
			}
		})
	}
}

// TestRefreshTier_String pins the low-cardinality metric labels after the
// Followed insert + Normal/Cold renumber.
func TestRefreshTier_String(t *testing.T) {
	t.Parallel()
	cases := map[RefreshTier]string{
		RefreshTierChanged:  "changed",
		RefreshTierHot:      "hot",
		RefreshTierFollowed: "followed",
		RefreshTierNormal:   "normal",
		RefreshTierCold:     "cold",
		RefreshTier(99):     "unknown",
	}
	for tier, want := range cases {
		if got := tier.String(); got != want {
			t.Errorf("RefreshTier(%d).String() = %q, want %q", int(tier), got, want)
		}
	}
}

// TestRefreshTier_Ordering pins the priority ordering used by the tiered
// picker's ORDER BY tier ASC: Changed < Hot < Followed < Normal < Cold.
func TestRefreshTier_Ordering(t *testing.T) {
	t.Parallel()
	if RefreshTierChanged >= RefreshTierHot ||
		RefreshTierHot >= RefreshTierFollowed ||
		RefreshTierFollowed >= RefreshTierNormal ||
		RefreshTierNormal >= RefreshTierCold {
		t.Fatalf("tier ordering broken: changed=%d hot=%d followed=%d normal=%d cold=%d",
			RefreshTierChanged, RefreshTierHot, RefreshTierFollowed, RefreshTierNormal, RefreshTierCold)
	}
}

// TestDefaultRefreshTTL_Ordering asserts Hot < Followed < Normal < Cold on the
// production schedule.
func TestDefaultRefreshTTL_Ordering(t *testing.T) {
	t.Parallel()
	ttl := DefaultRefreshTTL()
	if ttl.Followed != 10*24*time.Hour {
		t.Fatalf("Followed TTL = %v, want 10d", ttl.Followed)
	}
	if ttl.Hot >= ttl.Followed || ttl.Followed >= ttl.Normal || ttl.Normal >= ttl.Cold {
		t.Fatalf("TTL ordering broken: hot=%v followed=%v normal=%v cold=%v",
			ttl.Hot, ttl.Followed, ttl.Normal, ttl.Cold)
	}
}
