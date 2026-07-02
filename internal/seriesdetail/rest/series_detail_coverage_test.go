package rest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
)

// TestMapSeasons_DownloadingCount — a Sonarr queue record with status
// "downloading" whose season_number matches a SeasonDetail must bump that
// season's downloading_count. Queued / completed records must NOT count.
// (mapHero / mapLibrary branch-coverage tests were removed at the B1b
// cutover along with their handlers; mapSeasons survives.)
func TestMapSeasons_DownloadingCount(t *testing.T) {
	t.Parallel()
	d := &seriesdetail.Detail{
		Seasons: []seriesdetail.SeasonDetail{
			{Canon: series.CanonSeason{SeasonNumber: 1}},
			{Canon: series.CanonSeason{SeasonNumber: 5}},
		},
		QueueRecords: []seriesdetail.QueueRecordDetail{
			{SeasonNumber: 5, EpisodeNumber: 3, Status: "downloading"},
			{SeasonNumber: 5, EpisodeNumber: 4, Status: "downloading"},
			{SeasonNumber: 5, EpisodeNumber: 5, Status: "queued"}, // not downloading
			{SeasonNumber: 1, EpisodeNumber: 1, Status: "downloading"},
		},
	}
	out := mapSeasons(d)
	require.Len(t, out, 2)
	// out[0] is season 1.
	assert.Equal(t, 1, out[0].SeasonNumber)
	assert.Equal(t, 1, out[0].DownloadingCount)
	// out[1] is season 5.
	assert.Equal(t, 5, out[1].SeasonNumber)
	assert.Equal(t, 2, out[1].DownloadingCount, "queued record must not count")
}
