package middleware

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// SessionCookieName is the HMAC-signed session cookie. Exported so
// AuthHandler (this story) and the 009a1 middleware refactor share
// one named constant.
const SessionCookieName = "seasonfill_session"

// cookieTTL is the absolute lifetime of a signed cookie. Matches
// the Set-Cookie Max-Age set by Login.
const cookieTTL = 30 * 24 * time.Hour

var (
	// ErrCookieMalformed: wrong part count, undecodable base64, or
	// non-numeric issued_at. Callers fall back to header.
	ErrCookieMalformed = errors.New("cookie malformed")

	// ErrCookieSignature: HMAC mismatch. Callers fall back to header.
	ErrCookieSignature = errors.New("cookie signature mismatch")

	// ErrCookieExpired: issued_at + cookieTTL in the past. Callers
	// fall back to header so a stale cookie doesn't block a valid
	// X-Api-Key request.
	ErrCookieExpired = errors.New("cookie expired")
)

// SignCookie produces an opaque token bound to `secret`. Format:
// base64url(issued_unix).base64url(nonce16).base64url(hmac). The
// 16-byte random nonce makes consecutive calls at the same `now`
// produce distinct tokens.
func SignCookie(secret []byte, now time.Time) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	issued := strconv.FormatInt(now.Unix(), 10)
	issuedB64 := base64.RawURLEncoding.EncodeToString([]byte(issued))
	nonceB64 := base64.RawURLEncoding.EncodeToString(nonce)
	payload := issuedB64 + "." + nonceB64
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig, nil
}

// VerifyCookie checks `token` against `secret` and the TTL window.
// Returns one of ErrCookieMalformed / Signature / Expired; callers
// must NOT leak which to the client (single 401 response).
func VerifyCookie(secret []byte, token string, now time.Time) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ErrCookieMalformed
	}
	payload := parts[0] + "." + parts[1]
	expected := hmac.New(sha256.New, secret)
	_, _ = expected.Write([]byte(payload))
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return ErrCookieMalformed
	}
	if subtle.ConstantTimeCompare(sigBytes, expected.Sum(nil)) != 1 {
		return ErrCookieSignature
	}
	issuedBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ErrCookieMalformed
	}
	issued, err := strconv.ParseInt(string(issuedBytes), 10, 64)
	if err != nil {
		return ErrCookieMalformed
	}
	if now.Sub(time.Unix(issued, 0)) > cookieTTL {
		return ErrCookieExpired
	}
	return nil
}
