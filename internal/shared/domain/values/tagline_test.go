package values_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

func TestNewTagline(t *testing.T) {
	t.Parallel()
	ru := mustLang(t, "ru-RU")
	zero := values.LanguageTag{}
	tests := []struct {
		name    string
		value   string
		lang    values.LanguageTag
		wantErr bool
	}{
		{"valid", "A new dawn rises", ru, false},
		{"trim whitespace", "  A new dawn  ", ru, false},
		{"reject empty value", "", ru, true},
		{"reject whitespace-only", "   ", ru, true},
		{"reject zero lang", "A new dawn", zero, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := values.NewTagline(tc.value, tc.lang)
			if tc.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, values.ErrTaglineEmpty))
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestTagline_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	ru := mustLang(t, "ru-RU")
	src, err := values.NewTagline("Слоган", ru)
	require.NoError(t, err)
	data, err := json.Marshal(src)
	require.NoError(t, err)
	var got values.Tagline
	require.NoError(t, json.Unmarshal(data, &got))
	require.True(t, src.Equal(got))
	require.Equal(t, "Слоган", got.Value())
	require.Equal(t, "ru-RU", got.Lang().Value())
}

func TestTagline_ZeroMarshalsNull(t *testing.T) {
	t.Parallel()
	var zero values.Tagline
	require.True(t, zero.IsZero())
	data, err := json.Marshal(zero)
	require.NoError(t, err)
	require.Equal(t, "null", string(data))
}

func TestTagline_NullUnmarshalsToZero(t *testing.T) {
	t.Parallel()
	var got values.Tagline
	require.NoError(t, json.Unmarshal([]byte("null"), &got))
	require.True(t, got.IsZero())
}

func TestTagline_Equal(t *testing.T) {
	t.Parallel()
	ru := mustLang(t, "ru-RU")
	en := mustLang(t, "en-US")
	a, _ := values.NewTagline("X", ru)
	b, _ := values.NewTagline("X", ru)
	c, _ := values.NewTagline("X", en)
	d, _ := values.NewTagline("Y", ru)
	require.True(t, a.Equal(b))
	require.False(t, a.Equal(c))
	require.False(t, a.Equal(d))
}
