package series

import "testing"

func TestSeasonStats_Missing(t *testing.T) {
	tests := []struct {
		name string
		in   SeasonStats
		want int
	}{
		{"zero", SeasonStats{}, 0},
		{"happy_partial", SeasonStats{Total: 10, Aired: 8, Existing: 3}, 5},
		{"complete", SeasonStats{Total: 10, Aired: 10, Existing: 10}, 0},
		{"clamp_negative_inconsistent_snapshot", SeasonStats{Aired: 5, Existing: 8}, 0},
		{"future_only_unaired", SeasonStats{Total: 10, Aired: 0, Existing: 0}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.Missing(); got != tt.want {
				t.Fatalf("Missing() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSeasonStats_IsComplete(t *testing.T) {
	tests := []struct {
		name string
		in   SeasonStats
		want bool
	}{
		{"complete", SeasonStats{Aired: 10, Existing: 10}, true},
		{"partial", SeasonStats{Aired: 10, Existing: 7}, false},
		{"unaired_only", SeasonStats{Total: 10, Aired: 0, Existing: 0}, true},
		{"clamp_negative_treated_complete", SeasonStats{Aired: 5, Existing: 8}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.IsComplete(); got != tt.want {
				t.Fatalf("IsComplete() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSeasonStats_HasNoLocal(t *testing.T) {
	tests := []struct {
		name string
		in   SeasonStats
		want bool
	}{
		{"sonarr_handles_classic", SeasonStats{Aired: 8, Existing: 0}, true},
		{"unaired_only_not_handled", SeasonStats{Aired: 0, Existing: 0}, false},
		{"partial_pack_not_handled", SeasonStats{Aired: 8, Existing: 3}, false},
		{"complete_not_handled", SeasonStats{Aired: 10, Existing: 10}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.HasNoLocal(); got != tt.want {
				t.Fatalf("HasNoLocal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSeasonStatsFromStatistics(t *testing.T) {
	tests := []struct {
		name string
		st   Statistics
		want SeasonStats
	}{
		{
			name: "modern_full_block",
			st:   Statistics{Total: 10, Aired: 8, EpisodeFileCount: 3, EpisodeCount: 10},
			want: SeasonStats{Total: 10, Aired: 8, Existing: 3},
		},
		{
			name: "legacy_only_episode_count",
			st:   Statistics{EpisodeCount: 10, EpisodeFileCount: 7},
			want: SeasonStats{Total: 10, Aired: 10, Existing: 7},
		},
		{
			name: "empty",
			st:   Statistics{},
			want: SeasonStats{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SeasonStatsFromStatistics(tt.st)
			if got != tt.want {
				t.Fatalf("SeasonStatsFromStatistics() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
