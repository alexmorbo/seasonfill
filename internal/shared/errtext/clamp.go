// Package errtext clamps persisted upstream error strings to a single
// project-wide cap (4 KiB) so a giant Sonarr/qBit stack trace cannot
// blow up DB rows or wire payloads. The drawer renders the full
// retained text in a <pre className="whitespace-pre-wrap break-all">
// block; the list row clamps to one line via CSS. Operators always see
// the full retained body OR the explicit "truncated N bytes" suffix.
//
// One shared helper, one constant, applied at every grab.error_message
// and decision.error_detail write — see story 092 (F-P2-4).
package errtext

import (
	"fmt"
)

// MaxBytes is the persistence cap for any operator-visible upstream
// error string. 4 KiB is large enough to hold a full Sonarr stack
// trace (the upstream HTTP body is itself read with
// io.LimitReader(_, 4096) on the network side — see
// infrastructure/sonarr/client.go) but small enough that even a row
// per scan-failure stays well under the typical Postgres/SQLite TOAST
// threshold. Byte-counted, not rune-counted, because the underlying
// bytes are what the column stores and what JSON encodes.
const MaxBytes = 4096

// Clamp returns s unchanged if len(s) <= MaxBytes; otherwise returns
// the first MaxBytes bytes of s with a "…(truncated N bytes)" suffix
// appended (N = the number of bytes dropped). The suffix is appended
// AFTER the cut — total length is therefore MaxBytes + len(suffix),
// which is fine for a `text` column but documented here so callers do
// not assume a hard byte budget.
//
// Multi-byte UTF-8 sequences may be cut mid-codepoint at the
// MaxBytes boundary. That is intentional: the only consumer is the
// drawer's <pre> block which renders the bytes verbatim, and
// browsers tolerate the resulting U+FFFD replacement glyphs.
// Snapping to a rune boundary would require an O(n) walk on every
// write and gain nothing — operators copy-paste the field into a
// grep pipeline, not into a string compare.
func Clamp(s string) string {
	if len(s) <= MaxBytes {
		return s
	}
	dropped := len(s) - MaxBytes
	return s[:MaxBytes] + fmt.Sprintf("…(truncated %d bytes)", dropped)
}
