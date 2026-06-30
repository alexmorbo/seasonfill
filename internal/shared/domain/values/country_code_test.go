package values_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

func TestNewCountryCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"valid US", "US", "US", false},
		{"normalize us", "us", "US", false},
		{"trim whitespace", "  RU  ", "RU", false},
		{"reject 3-letter", "USA", "", true},
		{"reject digit", "U1", "", true},
		{"reject empty", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := values.NewCountryCode(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, values.ErrCountryCodeInvalid))
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got.Value())
		})
	}
}

func TestCountryCode_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	src, err := values.NewCountryCode("US")
	require.NoError(t, err)
	data, err := json.Marshal(src)
	require.NoError(t, err)
	require.Equal(t, `"US"`, string(data))
	var got values.CountryCode
	require.NoError(t, json.Unmarshal(data, &got))
	require.True(t, src.Equal(got))
}
