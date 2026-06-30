package freshener_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
)

func TestSeasonSection_Roundtrip(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, 1, 8, 99, 1000} {
		s := freshener.SeasonSection(n)
		got, ok := freshener.IsSeasonSection(s)
		assert.True(t, ok, "n=%d", n)
		assert.Equal(t, n, got, "n=%d → %q", n, string(s))
	}
}

func TestIsSeasonSection_FixedSectionsRejected(t *testing.T) {
	t.Parallel()
	for _, s := range freshener.FixedSections {
		_, ok := freshener.IsSeasonSection(s)
		assert.False(t, ok, "%q must not parse as season:N", string(s))
	}
}

func TestIsSeasonSection_GarbageRejected(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "season", "season:", "season:abc", "season:1.5", "skeleton"} {
		_, ok := freshener.IsSeasonSection(freshener.Section(raw))
		assert.False(t, ok, "garbage %q parsed as season", raw)
	}
}
