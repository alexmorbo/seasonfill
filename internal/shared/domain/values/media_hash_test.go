package values_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

func TestNewMediaHash(t *testing.T) {
	t.Parallel()
	valid := strings.Repeat("a", 64)
	upper := strings.Repeat("A", 64)
	short := strings.Repeat("a", 63)
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid 64 lowercase hex", valid, false},
		{"reject uppercase", upper, true},
		{"reject 63 chars", short, true},
		{"reject 65 chars", valid + "a", true},
		{"reject non-hex", strings.Repeat("z", 64), true},
		{"reject empty", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := values.NewMediaHash(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, values.ErrMediaHashInvalid))
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestMediaHash_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	hash := strings.Repeat("a", 64)
	src, err := values.NewMediaHash(hash)
	require.NoError(t, err)
	data, err := json.Marshal(src)
	require.NoError(t, err)
	var got values.MediaHash
	require.NoError(t, json.Unmarshal(data, &got))
	require.True(t, src.Equal(got))
}
