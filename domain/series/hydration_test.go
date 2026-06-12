package series

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCanTransition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		from Hydration
		to   Hydration
		want bool
	}{
		{"stub -> stub", HydrationStub, HydrationStub, true},
		{"stub -> full", HydrationStub, HydrationFull, true},
		{"full -> stub REJECTED", HydrationFull, HydrationStub, false},
		{"full -> full", HydrationFull, HydrationFull, true},
		{"empty from defaults to stub", "", HydrationFull, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, CanTransition(tc.from, tc.to))
		})
	}
}
