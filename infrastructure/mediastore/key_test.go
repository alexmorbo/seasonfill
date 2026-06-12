package mediastore

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKey_Deterministic(t *testing.T) {
	const url = "https://image.tmdb.org/t/p/w342/abc.jpg"
	k1 := Key(url, "jpg")
	k2 := Key(url, "jpg")
	assert.Equal(t, k1, k2)
}

func TestKey_Shape(t *testing.T) {
	tests := []struct {
		name      string
		sourceURL string
		ext       string
		wantExt   string
	}{
		{name: "lowercase ext", sourceURL: "https://example.com/a.jpg", ext: "JPG", wantExt: "jpg"},
		{name: "strip leading dot", sourceURL: "https://example.com/a.png", ext: ".png", wantExt: "png"},
		{name: "trim whitespace", sourceURL: "https://example.com/a.webp", ext: "  webp ", wantExt: "webp"},
		{name: "empty ext omits dot", sourceURL: "https://example.com/a", ext: "", wantExt: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := Key(tt.sourceURL, tt.ext)
			require.True(t, strings.HasPrefix(k, "media/v1/"), "key %q must carry layout prefix", k)
			parts := strings.Split(k, "/")
			require.Len(t, parts, 4, "key %q must be media/v1/{shard}/{hash[.ext]}", k)
			require.Len(t, parts[2], 2, "shard segment must be 2 chars: %q", parts[2])
			name := parts[3]
			if tt.wantExt == "" {
				assert.NotContains(t, name, ".", "name must omit extension")
				assert.Len(t, name, 64, "name must be sha256 hex")
			} else {
				dot := strings.LastIndex(name, ".")
				require.Greater(t, dot, 0, "name %q must carry extension", name)
				assert.Equal(t, tt.wantExt, name[dot+1:])
				assert.Len(t, name[:dot], 64, "hash segment must be sha256 hex")
			}
		})
	}
}

func TestKey_DifferentURLsDifferentKeys(t *testing.T) {
	a := Key("https://image.tmdb.org/t/p/w342/abc.jpg", "jpg")
	b := Key("https://image.tmdb.org/t/p/original/abc.jpg", "jpg")
	assert.NotEqual(t, a, b, "size variants must hash to different keys")
}

func TestHashFromKey(t *testing.T) {
	const url = "https://image.tmdb.org/t/p/w342/abc.jpg"
	k := Key(url, "jpg")
	hash := HashFromKey(k)
	require.Len(t, hash, 64)
	assert.Empty(t, HashFromKey("garbage"))
	assert.Empty(t, HashFromKey("media/v1/aa/short.jpg"))
}
