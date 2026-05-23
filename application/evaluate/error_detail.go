package evaluate

import (
	"strings"
	"unicode/utf8"
)

// errorDetailMaxRunes — soft cap on the persisted error_detail size.
// 256 fits the common upstream-error shape (HTTP status + short URL +
// short message). Beyond that, the structured log keeps the original;
// the persisted preview is enough to identify the failure class. Bound
// is in runes, not bytes, so multibyte glyphs do not truncate mid-
// codepoint (Q-014-1). Buffer above this in models.go is `size:300`.
const errorDetailMaxRunes = 256

// truncateErrorDetail normalises an error.Error() string for storage
// + UI display:
//   - trim leading/trailing whitespace
//   - flatten newlines (\r, \n) to spaces; collapse runs of whitespace
//   - cap at errorDetailMaxRunes; append "..." when truncated
//
// Returns "" for empty input — the zero-value is the wire signal that
// no error detail is attached (Q-014-3).
func truncateErrorDetail(s string) string {
	if s == "" {
		return ""
	}
	// Flatten multiline errors so the UI stays single-line (Q-014-5).
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s)
	s = collapseWhitespace(s)
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if utf8.RuneCountInString(s) <= errorDetailMaxRunes {
		return s
	}
	// Walk the string by rune to find the byte-offset of the
	// (errorDetailMaxRunes-3)th rune so "..." brings total to the cap.
	const ellipsis = "..."
	keepRunes := errorDetailMaxRunes - len(ellipsis)
	var b strings.Builder
	b.Grow(len(s))
	count := 0
	for _, r := range s {
		if count == keepRunes {
			break
		}
		b.WriteRune(r)
		count++
	}
	b.WriteString(ellipsis)
	return b.String()
}

// collapseWhitespace replaces runs of ' ' with a single space. Cheaper
// than regexp for this hot path (called once per error decision).
func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if r == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
		} else {
			prevSpace = false
		}
		b.WriteRune(r)
	}
	return b.String()
}
