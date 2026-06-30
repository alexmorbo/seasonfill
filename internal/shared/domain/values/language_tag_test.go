package values_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

func TestNewLanguageTag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"valid ru-RU", "ru-RU", "ru-RU", false},
		{"valid en-US", "en-US", "en-US", false},
		{"normalize ru-ru", "ru-ru", "ru-RU", false},
		{"normalize EN-us", "EN-us", "en-US", false},
		{"trim whitespace", "  en-US  ", "en-US", false},
		{"reject ruRU no hyphen", "ruRU", "", true},
		{"reject ru only", "ru", "", true},
		{"reject ru-R only", "ru-R", "", true},
		{"reject empty", "", "", true},
		{"reject digits", "r1-RU", "", true},
		{"reject script subtag", "zh-Hant-CN", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := values.NewLanguageTag(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, values.ErrLanguageTagInvalid))
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got.Value())
		})
	}
}

func TestLanguageTag_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	src, err := values.NewLanguageTag("ru-RU")
	require.NoError(t, err)
	data, err := json.Marshal(src)
	require.NoError(t, err)
	require.Equal(t, `"ru-RU"`, string(data))
	var got values.LanguageTag
	require.NoError(t, json.Unmarshal(data, &got))
	require.True(t, src.Equal(got))
}

func TestLanguageTag_ZeroMarshalsNull(t *testing.T) {
	t.Parallel()
	var zero values.LanguageTag
	require.True(t, zero.IsZero())
	data, err := json.Marshal(zero)
	require.NoError(t, err)
	require.Equal(t, "null", string(data))
}

func TestLanguageTag_NullUnmarshalsToZero(t *testing.T) {
	t.Parallel()
	var got values.LanguageTag
	require.NoError(t, json.Unmarshal([]byte("null"), &got))
	require.True(t, got.IsZero())
}

func TestLanguageTag_PointerFieldRoundTrip(t *testing.T) {
	t.Parallel()
	type wrap struct {
		Lang *values.LanguageTag `json:"lang,omitempty"`
	}
	in := wrap{}
	data, err := json.Marshal(in)
	require.NoError(t, err)
	require.Equal(t, `{}`, string(data))
}

func TestLanguageTag_Equal(t *testing.T) {
	t.Parallel()
	a, _ := values.NewLanguageTag("ru-RU")
	b, _ := values.NewLanguageTag("ru-RU")
	c, _ := values.NewLanguageTag("en-US")
	require.True(t, a.Equal(b))
	require.False(t, a.Equal(c))
}
