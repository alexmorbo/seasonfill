package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testSecret = []byte("test-secret-please-do-not-reuse")

func TestSignCookie_RoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	tok, err := SignCookie(testSecret, now)
	require.NoError(t, err)
	require.NotEmpty(t, tok)
	assert.Equal(t, 3, len(strings.Split(tok, ".")),
		"expected base64url(issued).base64url(nonce).base64url(hmac)")

	require.NoError(t, VerifyCookie(testSecret, tok, now))
}

func TestVerifyCookie_TamperedSignature(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	tok, err := SignCookie(testSecret, now)
	require.NoError(t, err)

	parts := strings.Split(tok, ".")
	require.Len(t, parts, 3)

	// Decode → XOR-flip one real byte → re-encode. Flipping the
	// trailing base64 char alone is unsafe (padding-bit shuffle can
	// leave the decoded signature identical — see Known revisions).
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	sigBytes[0] ^= 0xFF
	parts[2] = base64.RawURLEncoding.EncodeToString(sigBytes)
	tampered := strings.Join(parts, ".")

	err = VerifyCookie(testSecret, tampered, now)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCookieSignature), "got %v, want ErrCookieSignature", err)
}

func TestVerifyCookie_WrongSecret(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	tok, err := SignCookie(testSecret, now)
	require.NoError(t, err)

	err = VerifyCookie([]byte("other-secret"), tok, now)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCookieSignature))
}

func TestVerifyCookie_MangledBase64(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	// Two valid-looking parts plus a third that is NOT valid base64url.
	bad := "MTcyMTYwMDAwMA.YWFhYWFhYWFhYWFhYWFhYQ.!!!not-base64!!!"
	err := VerifyCookie(testSecret, bad, now)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCookieMalformed))
}

func TestVerifyCookie_BadIssuedAtBase64(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	// Issued segment outside the base64url alphabet, but with a
	// valid HMAC so we reach the issued-decode branch.
	badIssued := "!!!"
	validNonce := base64.RawURLEncoding.EncodeToString([]byte("aaaaaaaaaaaaaaaa"))
	payload := badIssued + "." + validNonce
	mac := hmac.New(sha256.New, testSecret)
	_, _ = mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	token := payload + "." + sig

	err := VerifyCookie(testSecret, token, now)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCookieMalformed), "got %v, want ErrCookieMalformed", err)
}

func TestVerifyCookie_BadIssuedAtNotInt(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	// Base64-valid but non-numeric — guards strconv.ParseInt.
	badIssued := base64.RawURLEncoding.EncodeToString([]byte("not-an-int"))
	validNonce := base64.RawURLEncoding.EncodeToString([]byte("aaaaaaaaaaaaaaaa"))
	payload := badIssued + "." + validNonce
	mac := hmac.New(sha256.New, testSecret)
	_, _ = mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	token := payload + "." + sig

	err := VerifyCookie(testSecret, token, now)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCookieMalformed), "got %v, want ErrCookieMalformed", err)
}

func TestVerifyCookie_Expired(t *testing.T) {
	t.Parallel()
	issued := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tok, err := SignCookie(testSecret, issued)
	require.NoError(t, err)
	later := issued.Add(31 * 24 * time.Hour)
	err = VerifyCookie(testSecret, tok, later)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCookieExpired))
}

func TestVerifyCookie_TooFewParts(t *testing.T) {
	t.Parallel()
	cases := []string{"", "one", "one.two"}
	for _, in := range cases {
		err := VerifyCookie(testSecret, in, time.Now())
		require.Error(t, err, in)
		assert.True(t, errors.Is(err, ErrCookieMalformed), in)
	}
}

func TestVerifyCookie_TooManyParts(t *testing.T) {
	t.Parallel()
	err := VerifyCookie(testSecret, "a.b.c.d", time.Now())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCookieMalformed))
}

func TestSignCookie_RandomNonceVariance(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	tok1, err := SignCookie(testSecret, now)
	require.NoError(t, err)
	tok2, err := SignCookie(testSecret, now)
	require.NoError(t, err)
	assert.NotEqual(t, tok1, tok2, "random nonce must vary")
}

func TestVerifyCookie_TTLBoundary(t *testing.T) {
	t.Parallel()
	issued := time.Unix(1700000000, 0)

	tests := []struct {
		name    string
		delta   time.Duration
		wantErr error
	}{
		{"just before TTL", cookieTTL - time.Nanosecond, nil},
		{"exactly at TTL", cookieTTL, nil},
		{"just past TTL", cookieTTL + time.Nanosecond, ErrCookieExpired},
		{"future-dated (negative delta)", -time.Hour, nil},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			token, err := SignCookie(testSecret, issued)
			require.NoError(t, err)
			err = VerifyCookie(testSecret, token, issued.Add(tc.delta))
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tc.wantErr),
					"got %v, want %v", err, tc.wantErr)
			}
		})
	}
}
