package values_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

func mustScore(t *testing.T, v float64) values.Score {
	t.Helper()
	s, err := values.NewScore(v)
	require.NoError(t, err)
	return s
}

func mustVotes(t *testing.T, n int) values.VoteCount {
	t.Helper()
	v, err := values.NewVoteCount(n)
	require.NoError(t, err)
	return v
}

func TestNewRating(t *testing.T) {
	t.Parallel()
	_, err := values.NewRating(mustScore(t, 8.4), mustVotes(t, 1200))
	require.NoError(t, err)
	_, err = values.NewRating(values.Score{}, values.VoteCount{})
	require.Error(t, err)
}

func TestRating_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	src, err := values.NewRating(mustScore(t, 8.4), mustVotes(t, 1200))
	require.NoError(t, err)
	data, err := json.Marshal(src)
	require.NoError(t, err)
	var got values.Rating
	require.NoError(t, json.Unmarshal(data, &got))
	require.True(t, src.Equal(got))
}

func TestRating_ZeroMarshalsNull(t *testing.T) {
	t.Parallel()
	var zero values.Rating
	require.True(t, zero.IsZero())
	data, err := json.Marshal(zero)
	require.NoError(t, err)
	require.Equal(t, "null", string(data))
}
