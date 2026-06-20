package scan

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
)

// TestCacheEntryFromPayload_AiredFallback — story 380. Sonarr's
// /api/v3/series LIST endpoint omits airedEpisodeCount from the
// series-level statistics block. cacheEntryFromPayload must fall back
// to the legacy EpisodeCount field so the LibraryStrip denominator
// renders against the LIST endpoint output.
func TestCacheEntryFromPayload_AiredFallback(t *testing.T) {
	t.Parallel()
	p := sonarr.SeriesPayload{
		ID:        140,
		Title:     "Rick and Morty",
		TitleSlug: "rick-and-morty",
		Monitored: true,
		Statistics: series.Statistics{
			EpisodeCount:     71,
			EpisodeFileCount: 71,
			// Aired intentionally zero — mirrors LIST-endpoint shape.
		},
	}
	entry := cacheEntryFromPayload("homelab", p)
	assert.Equal(t, 71, entry.AiredEpisodeCount, "fallback should pick up EpisodeCount when Aired is zero")
}

// TestCacheEntryFromPayload_AiredPrefersExplicit — defensive: when
// Sonarr does emit Aired (per-season blocks, or future series-level
// fixes), it wins over the EpisodeCount fallback.
func TestCacheEntryFromPayload_AiredPrefersExplicit(t *testing.T) {
	t.Parallel()
	p := sonarr.SeriesPayload{
		ID: 140, Title: "X", TitleSlug: "x", Monitored: true,
		Statistics: series.Statistics{
			EpisodeCount: 40,
			Aired:        38,
		},
	}
	entry := cacheEntryFromPayload("homelab", p)
	assert.Equal(t, 38, entry.AiredEpisodeCount, "explicit Aired wins over EpisodeCount fallback")
}

// TestAiredOrEpisodeCount_TableDriven — direct helper coverage.
func TestAiredOrEpisodeCount_TableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   series.Statistics
		want int
	}{
		{"prefers Aired", series.Statistics{Aired: 10, EpisodeCount: 12}, 10},
		{"falls back to EpisodeCount when Aired zero", series.Statistics{Aired: 0, EpisodeCount: 38}, 38},
		{"both zero -> zero", series.Statistics{}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, airedOrEpisodeCount(tc.in))
		})
	}
}
