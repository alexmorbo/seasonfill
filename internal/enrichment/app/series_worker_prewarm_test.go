package enrichment

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
)

// TestComposePrewarmAssets_EpisodeStills locks the story 322 invariant:
// every non-empty episode StillAsset produces a "still_w300" prewarm
// request. Misses (nil / empty) are skipped.
func TestComposePrewarmAssets_EpisodeStills(t *testing.T) {
	t.Parallel()
	canon := series.Canon{ID: 42, Title: "Breaking Bad"}
	// mappedPayload's Episodes field carries the canon episodes (see
	// series_worker.go for the populated field name; adapt if needed).
	m := mappedPayload{
		Episodes: []series.CanonEpisode{
			{ID: 10, SeasonNumber: 1, EpisodeNumber: 1, StillAsset: new("/ep1.jpg")},
			{ID: 11, SeasonNumber: 1, EpisodeNumber: 2}, // no still — skipped
			{ID: 20, SeasonNumber: 2, EpisodeNumber: 1, StillAsset: new("/ep3.jpg")},
		},
	}
	reqs := composePrewarmAssets(canon, m, nil)
	var stills []MediaPrewarmRequest
	for _, r := range reqs {
		if r.Kind == "still_w300" {
			stills = append(stills, r)
		}
	}
	require.Len(t, stills, 2, "every non-empty StillAsset must yield one still_w300 request")
	require.Equal(t, "https://image.tmdb.org/t/p/w300/ep1.jpg", stills[0].UpstreamURL)
	require.Equal(t, "https://image.tmdb.org/t/p/w300/ep3.jpg", stills[1].UpstreamURL)
}
