package enrichment

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCanTransition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		from HydrationLevel
		to   HydrationLevel
		want bool
	}{
		{"stub -> stub", LevelStub, LevelStub, true},
		{"stub -> full", LevelStub, LevelFull, true},
		{"full -> stub REJECTED", LevelFull, LevelStub, false},
		{"full -> full", LevelFull, LevelFull, true},
		{"empty from normalised to stub", "", LevelFull, true},
		{"empty to normalised to stub (from stub)", LevelStub, "", true},
		{"empty to normalised to stub (from full = REJECTED)", LevelFull, "", false},
		{"unknown from rejected", "weird", LevelFull, false},
		{"unknown to rejected", LevelStub, "weird", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, CanTransition(tc.from, tc.to))
		})
	}
}
