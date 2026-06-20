package evaluate

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// errorDetailMaxRunes — soft cap on the persisted error_detail size.
// Widened from 256 → 4096 by story 092 (F-P2-4) so error-category
// decisions carry the full upstream Sonarr body (or an explicit
// "truncated N runes" suffix on overflow). Paired with the migration
// that widens decisions.error_detail from varchar(300) to text. Bound
// is in runes, not bytes, so multibyte glyphs do not truncate mid-
// codepoint (Q-014-1).
const errorDetailMaxRunes = 4096

// truncateErrorDetail normalises an error.Error() string for storage
// + UI display:
//   - trim leading/trailing whitespace
//   - flatten newlines (\r, \n) to spaces; collapse runs of whitespace
//   - cap at errorDetailMaxRunes; append "…(truncated N runes)" suffix on overflow
//
// The newline flattening stays — the decision drawer's
// error_detail block sits below a series of single-line meta rows and
// newlines would push the layout out of alignment. Operators copying
// the field into a grep pipeline want a flat string anyway. The grab
// drawer's error_message has a different layout (top-of-drawer <pre>
// block) and preserves newlines via errtext.Clamp — these two paths
// are intentionally distinct.
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
	// errorDetailMaxRunes-th rune. Count the dropped runes so the
	// suffix tells the operator exactly how much was elided.
	var b strings.Builder
	b.Grow(len(s))
	kept := 0
	for _, r := range s {
		if kept == errorDetailMaxRunes {
			break
		}
		b.WriteRune(r)
		kept++
	}
	total := utf8.RuneCountInString(s)
	dropped := total - kept
	fmt.Fprintf(&b, "…(truncated %d runes)", dropped)
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
