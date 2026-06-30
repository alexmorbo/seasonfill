package values_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

func TestNewNextEpisodeCanon(t *testing.T) {
	t.Parallel()
	ru := mustLang(t, "ru-RU")
	title, err := values.NewTitle("Pilot", ru)
	require.NoError(t, err)
	airDate := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		season  int
		episode int
		title   values.Title
		air     time.Time
		days    int
		wantErr bool
	}{
		{"valid", 1, 1, title, airDate, 1, false},
		{"reject zero season", 0, 1, title, airDate, 1, true},
		{"reject zero episode", 1, 0, title, airDate, 1, true},
		{"reject zero title", 1, 1, values.Title{}, airDate, 1, true},
		{"reject zero air date", 1, 1, title, time.Time{}, 1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := values.NewNextEpisodeCanon(tc.season, tc.episode, tc.title, tc.air, tc.days)
			if tc.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, values.ErrNextEpisodeInvalid))
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestNextEpisodeCanon_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	ru := mustLang(t, "ru-RU")
	title, _ := values.NewTitle("Pilot", ru)
	airDate := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	src, err := values.NewNextEpisodeCanon(1, 1, title, airDate, 7)
	require.NoError(t, err)
	data, err := json.Marshal(src)
	require.NoError(t, err)
	var got values.NextEpisodeCanon
	require.NoError(t, json.Unmarshal(data, &got))
	require.True(t, src.Equal(got))
}

func TestNextEpisodeCanon_ZeroMarshalsNull(t *testing.T) {
	t.Parallel()
	var zero values.NextEpisodeCanon
	require.True(t, zero.IsZero())
	data, err := json.Marshal(zero)
	require.NoError(t, err)
	require.Equal(t, "null", string(data))
}
