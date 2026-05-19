package series

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSeries_MonitoredSeasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		seasons []Season
		want    int
	}{
		{
			name:    "all monitored",
			seasons: []Season{{Number: 1, Monitored: true}, {Number: 2, Monitored: true}},
			want:    2,
		},
		{
			name:    "mixed",
			seasons: []Season{{Number: 1, Monitored: true}, {Number: 2, Monitored: false}},
			want:    1,
		},
		{
			name:    "none monitored",
			seasons: []Season{{Number: 1, Monitored: false}, {Number: 2, Monitored: false}},
			want:    0,
		},
		{
			name:    "specials excluded when not monitored",
			seasons: []Season{{Number: 0, Monitored: false}, {Number: 1, Monitored: true}},
			want:    1,
		},
		{
			name:    "empty",
			seasons: nil,
			want:    0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := Series{Seasons: tt.seasons}
			got := s.MonitoredSeasons()
			assert.Len(t, got, tt.want)
			for _, season := range got {
				assert.True(t, season.Monitored)
			}
		})
	}
}

func TestSeriesType_Constants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, SeriesType("standard"), SeriesTypeStandard)
	assert.Equal(t, SeriesType("anime"), SeriesTypeAnime)
	assert.Equal(t, SeriesType("daily"), SeriesTypeDaily)
}
