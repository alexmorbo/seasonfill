package values_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

func TestNewContentRating(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"valid TV-MA", "TV-MA", "TV-MA", false},
		{"valid TV-14", "TV-14", "TV-14", false},
		{"valid PG-13", "PG-13", "PG-13", false},
		{"valid NR", "NR", "NR", false},
		{"normalize lowercase", "tv-ma", "TV-MA", false},
		{"trim whitespace", "  R  ", "R", false},
		{"reject unknown", "XX", "", true},
		{"reject empty", "", "", true},
		{"reject TV-Z bogus", "TV-Z", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := values.NewContentRating(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, values.ErrContentRatingInvalid))
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got.Value())
		})
	}
}

func TestContentRating_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	src, err := values.NewContentRating("TV-MA")
	require.NoError(t, err)
	data, err := json.Marshal(src)
	require.NoError(t, err)
	require.Equal(t, `"TV-MA"`, string(data))
	var got values.ContentRating
	require.NoError(t, json.Unmarshal(data, &got))
	require.True(t, src.Equal(got))
}
