package release

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRelease_HasRejection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		rejections []string
		query      string
		want       bool
	}{
		{
			name:       "present",
			rejections: []string{"Full season pack", "Too small"},
			query:      "Full season pack",
			want:       true,
		},
		{
			name:       "absent",
			rejections: []string{"Full season pack"},
			query:      "Not present",
			want:       false,
		},
		{
			name:       "case sensitive",
			rejections: []string{"Full season pack"},
			query:      "full season pack",
			want:       false,
		},
		{
			name:       "empty rejections",
			rejections: nil,
			query:      "anything",
			want:       false,
		},
		{
			name:       "empty query against non-empty list",
			rejections: []string{"x"},
			query:      "",
			want:       false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := Release{Rejections: tt.rejections}
			assert.Equal(t, tt.want, r.HasRejection(tt.query))
		})
	}
}

func TestRelease_Coverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mapped  []int
		missing []int
		want    int
	}{
		{
			name:    "full overlap",
			mapped:  []int{1, 2, 3},
			missing: []int{1, 2, 3},
			want:    3,
		},
		{
			name:    "partial overlap",
			mapped:  []int{1, 2, 3, 4},
			missing: []int{2, 3, 5},
			want:    2,
		},
		{
			name:    "no overlap",
			mapped:  []int{4, 5},
			missing: []int{1, 2, 3},
			want:    0,
		},
		{
			name:    "empty mapped returns 0",
			mapped:  nil,
			missing: []int{1, 2, 3},
			want:    0,
		},
		{
			name:    "empty missing returns 0",
			mapped:  []int{1, 2, 3},
			missing: nil,
			want:    0,
		},
		{
			name:    "both empty returns 0",
			mapped:  nil,
			missing: nil,
			want:    0,
		},
		{
			name:    "release covers more than missing — counts only overlap",
			mapped:  []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			missing: []int{3, 7},
			want:    2,
		},
		{
			name:    "duplicate in mapped does not double-count",
			mapped:  []int{1, 1, 2},
			missing: []int{1, 2},
			want:    3,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := Release{MappedEpisodeNumbers: tt.mapped}
			assert.Equal(t, tt.want, r.Coverage(tt.missing))
		})
	}
}
