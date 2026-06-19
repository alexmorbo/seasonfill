package enrichment

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKind_IsValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		k    Kind
		want bool
	}{
		{"continuing", KindSeriesContinuing, true},
		{"ended", KindSeriesEnded, true},
		{"season_active", KindSeasonActive, true},
		{"season_closed", KindSeasonClosed, true},
		{"person", KindPerson, true},
		{"omdb", KindOMDb, true},
		{"unknown empty", KindUnknown, false},
		{"unknown garbage", Kind("xyz"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.k.IsValid())
		})
	}
}
