package values_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

func TestNewVoteCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   int
		wantErr bool
	}{
		{"valid 1000", 1000, false},
		{"valid 0", 0, false},
		{"reject -1", -1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := values.NewVoteCount(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, values.ErrVoteCountInvalid))
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestVoteCount_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	src, err := values.NewVoteCount(1234)
	require.NoError(t, err)
	data, err := json.Marshal(src)
	require.NoError(t, err)
	require.Equal(t, "1234", string(data))
	var got values.VoteCount
	require.NoError(t, json.Unmarshal(data, &got))
	require.True(t, src.Equal(got))
}

func TestVoteCount_ZeroEmitsZero(t *testing.T) {
	t.Parallel()
	var zero values.VoteCount
	data, err := json.Marshal(zero)
	require.NoError(t, err)
	// IMPORTANT: votes=0 is meaningful data (Rating may legitimately
	// report 0 votes), so VoteCount does NOT collapse zero to null.
	// SkeletonDTO uses *VoteCount when "unknown" must be distinguishable.
	require.Equal(t, "0", string(data))
}
