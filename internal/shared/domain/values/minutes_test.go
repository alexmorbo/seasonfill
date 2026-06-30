package values_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

func TestNewMinutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   int
		wantErr bool
	}{
		{"valid 22", 22, false},
		{"valid 1 boundary", 1, false},
		{"reject zero", 0, true},
		{"reject negative", -10, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := values.NewMinutes(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, values.ErrMinutesInvalid))
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.input, got.Value())
		})
	}
}

func TestMinutes_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	src, err := values.NewMinutes(22)
	require.NoError(t, err)
	data, err := json.Marshal(src)
	require.NoError(t, err)
	require.Equal(t, "22", string(data))
	var got values.Minutes
	require.NoError(t, json.Unmarshal(data, &got))
	require.True(t, src.Equal(got))
}

func TestMinutes_ZeroMarshalsNull(t *testing.T) {
	t.Parallel()
	var zero values.Minutes
	require.True(t, zero.IsZero())
	data, err := json.Marshal(zero)
	require.NoError(t, err)
	require.Equal(t, "null", string(data))
}

func TestMinutes_NullUnmarshalsToZero(t *testing.T) {
	t.Parallel()
	var got values.Minutes
	require.NoError(t, json.Unmarshal([]byte("null"), &got))
	require.True(t, got.IsZero())
}

func TestMinutes_UnmarshalRejectsInvalid(t *testing.T) {
	t.Parallel()
	var got values.Minutes
	require.Error(t, json.Unmarshal([]byte("0"), &got))
	require.Error(t, json.Unmarshal([]byte("-5"), &got))
}
