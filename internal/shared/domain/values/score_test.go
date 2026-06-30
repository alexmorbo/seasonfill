package values_test

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

func TestNewScore(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   float64
		wantErr bool
	}{
		{"valid 7.5", 7.5, false},
		{"valid smallest positive", 0.1, false},
		{"valid 10 boundary", 10, false},
		{"reject 0 boundary", 0, true},
		{"reject negative", -0.1, true},
		{"reject 10.1", 10.1, true},
		{"reject NaN", math.NaN(), true},
		{"reject +Inf", math.Inf(1), true},
		{"reject -Inf", math.Inf(-1), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := values.NewScore(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, values.ErrScoreInvalid))
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestScore_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	src, err := values.NewScore(8.4)
	require.NoError(t, err)
	data, err := json.Marshal(src)
	require.NoError(t, err)
	var got values.Score
	require.NoError(t, json.Unmarshal(data, &got))
	require.True(t, src.Equal(got))
}

func TestScore_ZeroMarshalsNull(t *testing.T) {
	t.Parallel()
	var zero values.Score
	data, err := json.Marshal(zero)
	require.NoError(t, err)
	require.Equal(t, "null", string(data))
}
