package qbit

import "testing"

// TestStateGroup_AllRawStates is the canonical 22-state coverage
// test. Each case is one verbatim qBit state string with the
// expected bucket. Adding a 23rd state in a future qBit release =
// add the case; the default-arm safety net keeps the production
// binary from panicking in the meantime (it returns Unknown).
func TestStateGroup_AllRawStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want StateGroup
	}{
		// downloading bucket (5)
		{"downloading", StateGroupDownloading},
		{"forcedDL", StateGroupDownloading},
		{"metaDL", StateGroupDownloading},
		{"forcedMetaDL", StateGroupDownloading},
		{"allocating", StateGroupDownloading},
		// seeding bucket (2)
		{"uploading", StateGroupSeeding},
		{"forcedUP", StateGroupSeeding},
		// stalled bucket (2)
		{"stalledDL", StateGroupStalled},
		{"stalledUP", StateGroupStalled},
		// queued bucket (2)
		{"queuedDL", StateGroupQueued},
		{"queuedUP", StateGroupQueued},
		// paused bucket (4) — qBit 4.x and 5.x spellings
		{"pausedDL", StateGroupPaused},
		{"pausedUP", StateGroupPaused},
		{"stoppedDL", StateGroupPaused},
		{"stoppedUP", StateGroupPaused},
		// checking bucket (4)
		{"checkingDL", StateGroupChecking},
		{"checkingUP", StateGroupChecking},
		{"checkingResumeData", StateGroupChecking},
		{"moving", StateGroupChecking},
		// error bucket (2)
		{"error", StateGroupError},
		{"missingFiles", StateGroupError},
		// unknown bucket (1 verbatim)
		{"unknown", StateGroupUnknown},
		// fallback cases
		{"", StateGroupUnknown},
		{"futureWeirdState", StateGroupUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			if got := stateGroup(tc.raw); got != tc.want {
				t.Fatalf("stateGroup(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestStateGroup_TrimsWhitespace guards the defensive trim in the
// mapper — qBit shouldn't emit whitespace, but a future
// custom-WebUI-proxy might. Cheap insurance.
func TestStateGroup_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	if got := stateGroup("  downloading "); got != StateGroupDownloading {
		t.Fatalf("trim: got %q want downloading", got)
	}
}

// TestStateGroup_Exported asserts the public-facing wrapper agrees
// with the internal mapper. Belt-and-braces — story 220 will start
// calling StateGroupFor.
func TestStateGroup_Exported(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"downloading", "stoppedUP", "missingFiles", "garbage"} {
		if StateGroupFor(raw) != stateGroup(raw) {
			t.Fatalf("StateGroupFor and stateGroup disagree on %q", raw)
		}
	}
}
