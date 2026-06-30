package values_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

func TestNewTrailerKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid 11 chars", "dQw4w9WgXcQ", false},
		{"valid with underscore", "abc_def_123", false},
		{"valid with hyphen", "abc-def-123", false},
		{"reject 10 chars", "dQw4w9WgXc", true},
		{"reject 12 chars", "dQw4w9WgXcQa", true},
		{"reject space", "dQw4w9WgXc ", true},
		{"reject empty", "", true},
		{"reject special char", "dQw4w9Wg!cQ", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := values.NewTrailerKey(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, values.ErrTrailerKeyInvalid))
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestTrailerKey_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	src, err := values.NewTrailerKey("dQw4w9WgXcQ")
	require.NoError(t, err)
	data, err := json.Marshal(src)
	require.NoError(t, err)
	require.Equal(t, `"dQw4w9WgXcQ"`, string(data))
	var got values.TrailerKey
	require.NoError(t, json.Unmarshal(data, &got))
	require.True(t, src.Equal(got))
}
