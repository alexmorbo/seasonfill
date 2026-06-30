package values_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

func TestNewSeriesStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid Returning Series", "Returning Series", false},
		{"valid Ended", "Ended", false},
		{"valid Canceled", "Canceled", false},
		{"valid In Production", "In Production", false},
		{"valid Pilot", "Pilot", false},
		{"valid Planned", "Planned", false},
		{"reject case-mismatch", "ended", true},
		{"reject Cancelled (British)", "Cancelled", true},
		{"reject unknown", "Dormant", true},
		{"reject empty", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := values.NewSeriesStatus(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, values.ErrSeriesStatusInvalid))
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestSeriesStatus_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	src, err := values.NewSeriesStatus("Returning Series")
	require.NoError(t, err)
	data, err := json.Marshal(src)
	require.NoError(t, err)
	require.Equal(t, `"Returning Series"`, string(data))
	var got values.SeriesStatus
	require.NoError(t, json.Unmarshal(data, &got))
	require.True(t, src.Equal(got))
}
