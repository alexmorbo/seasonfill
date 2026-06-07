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

func TestStatistics_AiredMissing(t *testing.T) {
	tests := []struct {
		name string
		s    Statistics
		want int
	}{
		{"empty", Statistics{}, 0},
		{"legacy_only_episode_count", Statistics{EpisodeCount: 10, EpisodeFileCount: 7}, 3},
		{"aired_preferred_over_legacy", Statistics{EpisodeCount: 10, EpisodeFileCount: 7, Aired: 8}, 1},
		{"clamp_negative", Statistics{EpisodeCount: 5, EpisodeFileCount: 8}, 0},
		{"clamp_negative_with_aired", Statistics{Aired: 3, EpisodeFileCount: 5}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.AiredMissing(); got != tt.want {
				t.Fatalf("AiredMissing() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestStatistics_Existing(t *testing.T) {
	s := Statistics{EpisodeFileCount: 4}
	if got := s.Existing(); got != 4 {
		t.Fatalf("Existing() = %d, want 4", got)
	}
}
