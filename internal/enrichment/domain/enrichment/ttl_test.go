package enrichment

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTTL(t *testing.T) {
	t.Parallel()
	day := 24 * time.Hour
	cases := []struct {
		name   string
		source Source
		kind   Kind
		want   time.Duration
	}{
		{"tmdb series continuing", SourceTMDBSeries, KindSeriesContinuing, day},
		{"tmdb series ended", SourceTMDBSeries, KindSeriesEnded, 30 * day},
		{"tmdb season active", SourceTMDBSeason, KindSeasonActive, day},
		{"tmdb season closed", SourceTMDBSeason, KindSeasonClosed, 30 * day},
		{"tmdb person", SourceTMDBPerson, KindPerson, 30 * day},

		// OMDb base (KEPT for composer) + W18-5 age-tiers.
		{"omdb base", SourceOMDb, KindOMDb, day},
		{"omdb in_production", SourceOMDb, KindOMDbInProduction, 2 * day},
		{"omdb recent", SourceOMDb, KindOMDbRecent, 7 * day},
		{"omdb mid", SourceOMDb, KindOMDbMid, 30 * day},
		{"omdb old", SourceOMDb, KindOMDbOld, 90 * day},
		{"omdb ancient", SourceOMDb, KindOMDbAncient, 180 * day},

		// Mismatched / unknown pairs → 0.
		{"tmdb series with person kind", SourceTMDBSeries, KindPerson, 0},
		{"tmdb person with continuing kind", SourceTMDBPerson, KindSeriesContinuing, 0},
		{"omdb with season kind", SourceOMDb, KindSeasonActive, 0},
		{"tmdb series with omdb age kind", SourceTMDBSeries, KindOMDbRecent, 0},
		{"sonarr (live)", SourceSonarr, KindSeriesContinuing, 0},
		{"qbit (live)", SourceQbit, KindSeriesContinuing, 0},
		{"unknown kind", SourceTMDBSeries, KindUnknown, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, TTL(tc.source, tc.kind))
		})
	}
}
