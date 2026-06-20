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
	// Kept = cap, plus the suffix.
	assert.True(t, strings.HasPrefix(got, strings.Repeat("a", errorDetailMaxRunes)))
	assert.Contains(t, got, "(truncated 1 runes)")
}

func TestTruncateErrorDetail_FarOverCap_Multibyte(t *testing.T) {
	t.Parallel()
	// 5000 multibyte runes — truncation must not split a codepoint
	// (Q-014-1). Cap is 4096, suffix records the 904-rune drop.
	in := strings.Repeat("ы", 5000)
	got := truncateErrorDetail(in)
	assert.True(t, utf8.ValidString(got), "result must remain valid UTF-8")
	assert.True(t, strings.HasPrefix(got, strings.Repeat("ы", errorDetailMaxRunes)))
	assert.Contains(t, got, "(truncated 904 runes)")
}

// TestTruncateErrorDetail_RealisticSonarrBody asserts a ~3.5 KiB
// Sonarr stack trace stays under the cap unchanged — the operator
// sees the full upstream context in the drawer (F-P2-4).
func TestTruncateErrorDetail_RealisticSonarrBody(t *testing.T) {
	t.Parallel()
	body := "sonarr /api/v3/release returned status=500 body=" +
		strings.Repeat("NzbDrone.Core.Download.Clients.DownloadClientException: Download client failed to add torrent\\n   at NzbDrone.Core.Download.Clients.QBittorrent.QBittorrentProxyV2.AddTorrentFromFile(...). ", 20)
	if len(body) > errorDetailMaxRunes {
		t.Fatalf("fixture too large: %d bytes; pick a shorter repetition", len(body))
	}
	got := truncateErrorDetail(body)
	// Multi-line flattening still applies — newlines collapsed, but
	// the body is under the cap so no suffix.
	assert.NotContains(t, got, "(truncated")
}
