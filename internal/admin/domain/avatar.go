// Package admin avatar helpers.
//
// Story 485 (N-7a). ComputeAvatarHash mirrors the Gravatar canonical hash
// — md5 over the lowercase-trimmed email. The util is a deliberate
// duplicate of `crypto/md5` rather than a thin alias so the response
// builder can stay framework-free and call sites read as
// `admin.ComputeAvatarHash(...)`.

package admin

import (
	"crypto/md5" //nolint:gosec // Gravatar protocol mandates md5; not security-sensitive.
	"encoding/hex"
	"strings"
)

// ComputeAvatarHash returns the MD5 hash of the lowercase, trimmed email
// suitable for use as a Gravatar identifier
// (https://docs.gravatar.com/api/avatars/hash/). Returns an empty string
// when email is empty or whitespace-only.
//
// Whitespace handling: leading + trailing ASCII whitespace stripped via
// strings.TrimSpace; embedded whitespace is preserved (a defensive copy
// of Gravatar's documented algorithm — embedded whitespace is never legal
// in an RFC 5321 mailbox, so this only matters for malformed input).
func ComputeAvatarHash(email string) string {
	normalised := strings.TrimSpace(strings.ToLower(email))
	if normalised == "" {
		return ""
	}
	sum := md5.Sum([]byte(normalised)) //nolint:gosec
	return hex.EncodeToString(sum[:])
}
