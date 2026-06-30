package values_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

func TestNewYear(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   int
		wantErr bool
	}{
		{"valid 2026", 2026, false},
		{"valid 1900 boundary", 1900, false},
		{"valid 2100 boundary", 2100, false},
		{"reject 1899", 1899, true},
		{"reject 2101", 2101, true},
		{"reject zero", 0, true},
		{"reject negative", -100, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := values.NewYear(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, values.ErrYearInvalid))
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.input, got.Value())
		})
	}
}

func TestYear_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	src, err := values.NewYear(2026)
	require.NoError(t, err)
	data, err := json.Marshal(src)
	require.NoError(t, err)
	require.Equal(t, "2026", string(data))
	var got values.Year
	require.NoError(t, json.Unmarshal(data, &got))
	require.True(t, src.Equal(got))
}

func TestYear_ZeroMarshalsNull(t *testing.T) {
	t.Parallel()
	var zero values.Year
	require.True(t, zero.IsZero())
	data, err := json.Marshal(zero)
	require.NoError(t, err)
	require.Equal(t, "null", string(data))
}

func TestYear_NullUnmarshalsToZero(t *testing.T) {
	t.Parallel()
	var got values.Year
	require.NoError(t, json.Unmarshal([]byte("null"), &got))
	require.True(t, got.IsZero())
}

func TestYear_UnmarshalRejectsInvalid(t *testing.T) {
	t.Parallel()
	var got values.Year
	require.Error(t, json.Unmarshal([]byte("1500"), &got))
}

func TestYear_PointerFieldOmitempty(t *testing.T) {
	t.Parallel()
	type wrap struct {
		End *values.Year `json:"year_end,omitempty"`
	}
	in := wrap{}
	data, err := json.Marshal(in)
	require.NoError(t, err)
	require.Equal(t, `{}`, string(data))
}
