package icsfeed

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

func TestSignVerify_RoundTrip(t *testing.T) {
	t.Parallel()
	key, err := crypto.DeriveICSTokenKey("master-key")
	require.NoError(t, err)

	tok, err := SignToken(key, "followed", 7)
	require.NoError(t, err)

	p, err := VerifyToken(key, tok)
	require.NoError(t, err)
	assert.Equal(t, tokenPurpose, p.Purpose)
	assert.Equal(t, "followed", p.Scope)
	assert.Equal(t, int64(7), p.Epoch)
}

func TestVerify_TamperedSignature_Rejected(t *testing.T) {
	t.Parallel()
	key, _ := crypto.DeriveICSTokenKey("master-key")
	tok, _ := SignToken(key, "all", 0)
	// Flip the FIRST base64 char of the signature part. The last char of a
	// RawURL-encoded 32-byte HMAC carries 2 unused padding bits, so flipping
	// its low bit can decode to the identical signature; the first char is
	// always fully meaningful, guaranteeing the decoded signature changes.
	dot := strings.IndexByte(tok, '.')
	require.Positive(t, dot)
	b := []byte(tok)
	b[dot+1] ^= 0x01
	_, err := VerifyToken(key, string(b))
	assert.ErrorIs(t, err, ErrRevoked)
}

func TestVerify_WrongKey_Rejected(t *testing.T) {
	t.Parallel()
	k1, _ := crypto.DeriveICSTokenKey("key-1")
	k2, _ := crypto.DeriveICSTokenKey("key-2")
	tok, _ := SignToken(k1, "all", 0)
	_, err := VerifyToken(k2, tok)
	assert.ErrorIs(t, err, ErrRevoked)
}

func TestVerify_WrongPurpose_Rejected(t *testing.T) {
	t.Parallel()
	key, _ := crypto.DeriveICSTokenKey("master-key")
	// hand-craft a validly-signed token with purpose != "ics"
	body, _ := json.Marshal(Payload{Purpose: "session", Scope: "all", Epoch: 0})
	mac := hmacSum(key, body)
	tok := base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac)
	_, err := VerifyToken(key, tok)
	assert.ErrorIs(t, err, ErrRevoked)
}

func TestVerify_Malformed_Rejected(t *testing.T) {
	t.Parallel()
	key, _ := crypto.DeriveICSTokenKey("master-key")
	for _, bad := range []string{"", "onlyonepart", "a.b.c", "!!!.###"} {
		_, err := VerifyToken(key, bad)
		assert.ErrorIsf(t, err, ErrRevoked, "input %q", bad)
	}
}

// TestDomainSeparation_SessionCookieNotICS proves a session cookie can never
// validate as an ICS token and an ICS token can never validate as a session
// cookie — the two derive from DIFFERENT HKDF info labels.
func TestDomainSeparation_SessionCookieNotICS(t *testing.T) {
	t.Parallel()
	const master = "shared-master-key"
	icsKey, _ := crypto.DeriveICSTokenKey(master)
	sessKey, err := crypto.DeriveSessionHMACKey(master)
	require.NoError(t, err)

	// A real session cookie must NOT verify as an ICS token.
	cookie, err := middleware.SignSession(sessKey, "admin", time.Now().Add(time.Hour), 0)
	require.NoError(t, err)
	_, err = VerifyToken(icsKey, cookie)
	assert.ErrorIs(t, err, ErrRevoked, "session cookie must not verify as ICS token")

	// A real ICS token must NOT verify as a session cookie.
	icsTok, _ := SignToken(icsKey, "all", 0)
	_, err = middleware.VerifySession(sessKey, icsTok, time.Now(), 0)
	assert.Error(t, err, "ICS token must not verify as session cookie")
}

// hmacSum mirrors SignToken's MAC computation (HMAC-SHA256 over body).
func hmacSum(key, body []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(body)
	return mac.Sum(nil)
}
