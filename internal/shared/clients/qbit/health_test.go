package qbit

import "testing"

// TestHealthFor_AllStateGroups is the canonical coverage test for
// the state→health projection (ADR-0013 Q3′). Every one of the 8
// StateGroup buckets is asserted, PLUS the empty-string and a bogus
// value to lock the degrade-closed default (→ HealthOK).
func TestHealthFor_AllStateGroups(t *testing.T) {
	t.Parallel()
	cases := []struct {
		sg   StateGroup
		want Health
	}{
		// not-ok buckets
		{StateGroupError, HealthError},
		{StateGroupStalled, HealthStalled},
		// ok buckets
		{StateGroupDownloading, HealthOK},
		{StateGroupSeeding, HealthOK},
		{StateGroupQueued, HealthOK},
		{StateGroupPaused, HealthOK},
		{StateGroupChecking, HealthOK},
		{StateGroupUnknown, HealthOK},
		// degrade-closed defaults
		{StateGroup(""), HealthOK},
		{StateGroup("garbage"), HealthOK},
	}
	for _, tc := range cases {
		t.Run(string(tc.sg), func(t *testing.T) {
			t.Parallel()
			if got := HealthFor(tc.sg); got != tc.want {
				t.Fatalf("HealthFor(%q) = %q, want %q", tc.sg, got, tc.want)
			}
		})
	}
}
