package evaluate

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestTruncateErrorDetail(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"only-whitespace", "   \t\n  ", ""},
		{"short-passthrough", "sonarr: 503 service unavailable",
			"sonarr: 503 service unavailable"},
		{"trim-leading-trailing", "   sonarr: boom\n",
			"sonarr: boom"},
		{"multiline-flattened", "sonarr: 502\nbad gateway\nretry later",
			"sonarr: 502 bad gateway retry later"},
		{"collapse-runs-of-whitespace", "sonarr:    503    service",
			"sonarr: 503 service"},
		{"multibyte-safe", "ошибка: сервер недоступен (503)",
			"ошибка: сервер недоступен (503)"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, truncateErrorDetail(tc.in))
		})
	}
}

func TestTruncateErrorDetail_ExactlyAtCap(t *testing.T) {
	t.Parallel()
	// Build a 256-rune ASCII string — must pass through untouched.
	in := strings.Repeat("a", errorDetailMaxRunes)
	got := truncateErrorDetail(in)
	assert.Equal(t, in, got)
	assert.Equal(t, errorDetailMaxRunes, utf8.RuneCountInString(got))
}

func TestTruncateErrorDetail_OneOverCap(t *testing.T) {
	t.Parallel()
	in := strings.Repeat("a", errorDetailMaxRunes+1)
	got := truncateErrorDetail(in)
	assert.Equal(t, errorDetailMaxRunes, utf8.RuneCountInString(got),
		"total rune count after truncation must equal cap")
	assert.True(t, strings.HasSuffix(got, "..."))
}

func TestTruncateErrorDetail_FarOverCap_Multibyte(t *testing.T) {
	t.Parallel()
	// 1000 multibyte runes — truncation must not split a codepoint.
	in := strings.Repeat("ы", 1000)
	got := truncateErrorDetail(in)
	assert.True(t, utf8.ValidString(got), "result must remain valid UTF-8")
	assert.Equal(t, errorDetailMaxRunes, utf8.RuneCountInString(got))
	assert.True(t, strings.HasSuffix(got, "..."))
}
