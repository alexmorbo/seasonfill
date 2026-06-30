package values_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

func TestNewLangCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"valid ru", "ru", "ru", false},
		{"normalize RU", "RU", "ru", false},
		{"trim whitespace", "  en  ", "en", false},
		{"reject 3-letter", "rus", "", true},
		{"reject 1-letter", "r", "", true},
		{"reject digit", "r1", "", true},
		{"reject empty", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := values.NewLangCode(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, values.ErrLangCodeInvalid))
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got.Value())
		})
	}
}

func TestLangCode_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	src, err := values.NewLangCode("en")
	require.NoError(t, err)
	data, err := json.Marshal(src)
	require.NoError(t, err)
	require.Equal(t, `"en"`, string(data))
	var got values.LangCode
	require.NoError(t, json.Unmarshal(data, &got))
	require.True(t, src.Equal(got))
}
