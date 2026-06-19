package series

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSeason_Missing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		episodes []Episode
		want     []int
	}{
		{
			name: "monitored without file is missing",
			episodes: []Episode{
				{Number: 1, Monitored: true, HasFile: true},
				{Number: 2, Monitored: true, HasFile: false},
				{Number: 3, Monitored: true, HasFile: false},
			},
			want: []int{2, 3},
		},
		{
			name: "unmonitored without file is NOT missing",
			episodes: []Episode{
				{Number: 1, Monitored: false, HasFile: false},
				{Number: 2, Monitored: true, HasFile: false},
			},
			want: []int{2},
		},
		{
			name: "all have file means none missing",
			episodes: []Episode{
				{Number: 1, Monitored: true, HasFile: true},
				{Number: 2, Monitored: true, HasFile: true},
			},
			want: []int{},
		},
		{
			name:     "no episodes",
			episodes: nil,
			want:     []int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := Season{Episodes: tt.episodes}
			got := s.Missing()
			assert.Len(t, got, len(tt.want))
			for i, num := range tt.want {
				assert.Equal(t, num, got[i].Number)
			}
		})
	}
}

func TestSeason_Have(t *testing.T) {
	t.Parallel()

	s := Season{Episodes: []Episode{
		{Number: 1, Monitored: true, HasFile: true},
		{Number: 2, Monitored: true, HasFile: false},
		{Number: 3, Monitored: false, HasFile: true},
		{Number: 4, Monitored: true, HasFile: true},
	}}

	have := s.Have()
	assert.Len(t, have, 3)
	nums := []int{have[0].Number, have[1].Number, have[2].Number}
	assert.ElementsMatch(t, []int{1, 3, 4}, nums)
}

func TestSeason_MissingNumbers(t *testing.T) {
	t.Parallel()

	s := Season{Episodes: []Episode{
		{Number: 1, Monitored: true, HasFile: true},
		{Number: 2, Monitored: true, HasFile: false},
		{Number: 3, Monitored: true, HasFile: false},
	}}

	assert.Equal(t, []int{2, 3}, s.MissingNumbers())
}

func TestSeason_HaveNumbers(t *testing.T) {
	t.Parallel()

	s := Season{Episodes: []Episode{
		{Number: 1, Monitored: true, HasFile: true},
		{Number: 2, Monitored: true, HasFile: false},
		{Number: 3, Monitored: true, HasFile: true},
	}}

	assert.Equal(t, []int{1, 3}, s.HaveNumbers())
}

func TestSeason_AllEmpty(t *testing.T) {
	t.Parallel()
	s := Season{}
	assert.Empty(t, s.Missing())
	assert.Empty(t, s.Have())
	assert.Empty(t, s.MissingNumbers())
	assert.Empty(t, s.HaveNumbers())
}
