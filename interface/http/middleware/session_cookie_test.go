package middleware

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

func TestSession_RoundTrip(t *testing.T) {
	t.Parallel()
	secret := []byte("apikey")
	exp := time.Now().Add(12 * time.Hour)
	tok, err := SignSession(secret, "admin", exp)
	require.NoError(t, err)
	p, err := VerifySession(secret, tok, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "admin", p.Username)
	assert.Equal(t, exp.Unix(), p.Exp)
}

func TestSession_Expired(t *testing.T) {
	t.Parallel()
	tok, _ := SignSession([]byte("k"), "admin", time.Now().Add(-time.Minute))
	_, err := VerifySession([]byte("k"), tok, time.Now())
	assert.True(t, errors.Is(err, ErrSessionExpired))
}

func TestSession_SignatureMismatch(t *testing.T) {
	t.Parallel()
	tok, _ := SignSession([]byte("a"), "admin", time.Now().Add(time.Hour))
	_, err := VerifySession([]byte("b"), tok, time.Now())
	assert.True(t, errors.Is(err, ErrSessionSignature))
}

func TestSession_Malformed(t *testing.T) {
	t.Parallel()
	for _, c := range []string{"", "single", "too.many.parts.here", "!!!.???"} {
		_, err := VerifySession([]byte("k"), c, time.Now())
		assert.True(t, errors.Is(err, ErrSessionMalformed), "input=%q got=%v", c, err)
	}
}

func TestSession_EmptyUsername_Malformed(t *testing.T) {
	t.Parallel()
	tok, _ := SignSession([]byte("k"), "", time.Now().Add(time.Hour))
	_, err := VerifySession([]byte("k"), tok, time.Now())
	assert.True(t, errors.Is(err, ErrSessionMalformed))
}

// TestSessionSigningKey_DerivedDistinctFromMasterKey asserts that the HKDF-
// derived session HMAC key is domain-separated from the master key bytes.
// A cookie signed with the raw master key must NOT verify under the derived
// key, and vice versa — confirming the two roles use different key material.
func TestSessionSigningKey_DerivedDistinctFromMasterKey(t *testing.T) {
	t.Parallel()
	const masterKey = "test-master-key-for-session-regression"
	masterBytes := []byte(masterKey)

	sessionKey, err := crypto.DeriveSessionHMACKey(masterKey)
	require.NoError(t, err)

	// Derived key bytes must differ from the raw master key bytes.
	assert.False(t, bytes.Equal(sessionKey, masterBytes),
		"derived session key must not equal raw master key bytes")

	exp := time.Now().Add(time.Hour)

	// Cookie signed with master key must NOT verify with the derived key.
	tokMaster, err := SignSession(masterBytes, "admin", exp)
	require.NoError(t, err)
	_, errVerify := VerifySession(sessionKey, tokMaster, time.Now())
	assert.ErrorIs(t, errVerify, ErrSessionSignature,
		"master-key-signed cookie must not verify under derived session key")

	// Cookie signed with the derived key MUST verify with the derived key.
	tokDerived, err := SignSession(sessionKey, "admin", exp)
	require.NoError(t, err)
	_, errVerify2 := VerifySession(sessionKey, tokDerived, time.Now())
	assert.NoError(t, errVerify2,
		"derived-key-signed cookie must verify under derived session key")
}
