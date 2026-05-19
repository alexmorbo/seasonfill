package series

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEpisode_Aired(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		ep   Episode
		want bool
	}{
		{
			name: "aired yesterday",
			ep:   Episode{AirDateUTC: now.Add(-24 * time.Hour)},
			want: true,
		},
		{
			name: "airs in future",
			ep:   Episode{AirDateUTC: now.Add(24 * time.Hour)},
			want: false,
		},
		{
			name: "aired exactly now",
			ep:   Episode{AirDateUTC: now},
			want: true,
		},
		{
			name: "zero AirDateUTC returns false",
			ep:   Episode{AirDateUTC: time.Time{}},
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.ep.Aired(now))
		})
	}
}
