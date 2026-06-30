package values_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

func mustLang(t *testing.T, s string) values.LanguageTag {
	t.Helper()
	lt, err := values.NewLanguageTag(s)
	require.NoError(t, err)
	return lt
}

func TestNewTitle(t *testing.T) {
	t.Parallel()
	ru := mustLang(t, "ru-RU")
	zero := values.LanguageTag{}
	tests := []struct {
		name    string
		value   string
		lang    values.LanguageTag
		wantErr bool
	}{
		{"valid", "Star City", ru, false},
		{"trim whitespace", "  Star City  ", ru, false},
		{"reject empty value", "", ru, true},
		{"reject whitespace-only", "   ", ru, true},
		{"reject zero lang", "Star City", zero, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := values.NewTitle(tc.value, tc.lang)
			if tc.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, values.ErrTitleEmpty))
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestTitle_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	ru := mustLang(t, "ru-RU")
	src, err := values.NewTitle("Звёздный городок", ru)
	require.NoError(t, err)
	data, err := json.Marshal(src)
	require.NoError(t, err)
	var got values.Title
	require.NoError(t, json.Unmarshal(data, &got))
	require.True(t, src.Equal(got))
	require.Equal(t, "Звёздный городок", got.Value())
	require.Equal(t, "ru-RU", got.Lang().Value())
}

func TestTitle_ZeroMarshalsNull(t *testing.T) {
	t.Parallel()
	var zero values.Title
	require.True(t, zero.IsZero())
	data, err := json.Marshal(zero)
	require.NoError(t, err)
	require.Equal(t, "null", string(data))
}

func TestTitle_NullUnmarshalsToZero(t *testing.T) {
	t.Parallel()
	var got values.Title
	require.NoError(t, json.Unmarshal([]byte("null"), &got))
	require.True(t, got.IsZero())
}

func TestTitle_Equal(t *testing.T) {
	t.Parallel()
	ru := mustLang(t, "ru-RU")
	en := mustLang(t, "en-US")
	a, _ := values.NewTitle("X", ru)
	b, _ := values.NewTitle("X", ru)
	c, _ := values.NewTitle("X", en)
	d, _ := values.NewTitle("Y", ru)
	require.True(t, a.Equal(b))
	require.False(t, a.Equal(c))
	require.False(t, a.Equal(d))
}
