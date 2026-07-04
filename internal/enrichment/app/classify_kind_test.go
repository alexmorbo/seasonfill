package enrichment

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
)

// TestClassifyKind locks the TTL-bucket classification across BOTH status
// vocabularies: TMDB canonical case AND Sonarr coarse lowercase. The
// Sonarr "continuing" value must map to KindSeriesContinuing so
// library series (whose status is Sonarr-sourced until a TMDB Handle
// pass rewrites it) get the 24h continuing TTL, not the 30d ended TTL.
func TestClassifyKind(t *testing.T) {
	t.Parallel()
	statusPtr := func(s string) *string { return &s }
	cases := []struct {
		name string
		in   series.Canon
		want enrichment.Kind
	}{
		{"tmdb returning", series.Canon{Status: statusPtr("Returning Series")}, enrichment.KindSeriesContinuing},
		{"tmdb in production", series.Canon{Status: statusPtr("In Production")}, enrichment.KindSeriesContinuing},
		{"sonarr continuing", series.Canon{Status: statusPtr("continuing")}, enrichment.KindSeriesContinuing},
		{"sonarr ended", series.Canon{Status: statusPtr("ended")}, enrichment.KindSeriesEnded},
		{"sonarr deleted", series.Canon{Status: statusPtr("deleted")}, enrichment.KindSeriesEnded},
		{"tmdb ended", series.Canon{Status: statusPtr("Ended")}, enrichment.KindSeriesEnded},
		{"nil status", series.Canon{Status: nil}, enrichment.KindSeriesEnded},
		{"in production flag overrides ended status", series.Canon{Status: statusPtr("ended"), InProduction: true}, enrichment.KindSeriesContinuing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, classifyKind(tc.in))
		})
	}
}
