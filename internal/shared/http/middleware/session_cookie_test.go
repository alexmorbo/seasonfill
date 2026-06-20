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
	tok, err := SignSession(secret, "admin", exp, 0)
	require.NoError(t, err)
	p, err := VerifySession(secret, tok, time.Now(), 0)
	require.NoError(t, err)
	assert.Equal(t, "admin", p.Username)
	assert.Equal(t, exp.Unix(), p.Exp)
	assert.Equal(t, int64(0), p.Epoch)
}

func TestSession_RoundTripWithEpoch(t *testing.T) {
	t.Parallel()
	secret := []byte("apikey")
	tok, err := SignSession(secret, "admin", time.Now().Add(time.Hour), 42)
	require.NoError(t, err)
	p, err := VerifySession(secret, tok, time.Now(), 42)
	require.NoError(t, err)
	assert.Equal(t, int64(42), p.Epoch)
}

func TestSession_Expired(t *testing.T) {
	t.Parallel()
	tok, _ := SignSession([]byte("k"), "admin", time.Now().Add(-time.Minute), 0)
	_, err := VerifySession([]byte("k"), tok, time.Now(), 0)
	assert.True(t, errors.Is(err, ErrSessionExpired))
}

func TestSession_SignatureMismatch(t *testing.T) {
	t.Parallel()
	tok, _ := SignSession([]byte("a"), "admin", time.Now().Add(time.Hour), 0)
	_, err := VerifySession([]byte("b"), tok, time.Now(), 0)
	assert.True(t, errors.Is(err, ErrSessionSignature))
}

func TestSession_Malformed(t *testing.T) {
	t.Parallel()
	for _, c := range []string{"", "single", "too.many.parts.here", "!!!.???"} {
		_, err := VerifySession([]byte("k"), c, time.Now(), 0)
		assert.True(t, errors.Is(err, ErrSessionMalformed), "input=%q got=%v", c, err)
	}
}

func TestSession_EmptyUsername_Malformed(t *testing.T) {
	t.Parallel()
	tok, _ := SignSession([]byte("k"), "", time.Now().Add(time.Hour), 0)
	_, err := VerifySession([]byte("k"), tok, time.Now(), 0)
	assert.True(t, errors.Is(err, ErrSessionMalformed))
}

func TestSession_StaleEpoch_Rejected(t *testing.T) {
	t.Parallel()
	secret := []byte("k")
	tok, err := SignSession(secret, "admin", time.Now().Add(time.Hour), 5)
	require.NoError(t, err)
	_, err = VerifySession(secret, tok, time.Now(), 10)
	assert.True(t, errors.Is(err, ErrSessionEpoch))
}

func TestSession_DefaultEpochZero_StillValid(t *testing.T) {
	// Pre-036a cookies have no `ep` field → decodes as 0. Default
	// SessionEpoch in DB is 0 → must validate.
	t.Parallel()
	secret := []byte("k")
	tok, err := SignSession(secret, "admin", time.Now().Add(time.Hour), 0)
	require.NoError(t, err)
	p, err := VerifySession(secret, tok, time.Now(), 0)
	require.NoError(t, err)
	assert.Equal(t, "admin", p.Username)
}

func TestSession_FutureEpochAccepted(t *testing.T) {
	// Defensive: a cookie minted under a HIGHER epoch (e.g. during a
	// race between subscriber apply on one node and login on another)
	// must NOT be rejected. The invariant is `payload.Epoch >= snapshot`.
	t.Parallel()
	secret := []byte("k")
	tok, err := SignSession(secret, "admin", time.Now().Add(time.Hour), 999)
	require.NoError(t, err)
	_, err = VerifySession(secret, tok, time.Now(), 100)
	require.NoError(t, err)
}

// TestSessionSigningKey_DerivedDistinctFromMasterKey asserts that the HKDF-
// derived session HMAC key is domain-separated from the master key bytes.
func TestSessionSigningKey_DerivedDistinctFromMasterKey(t *testing.T) {
	t.Parallel()
	const masterKey = "test-master-key-for-session-regression"
	masterBytes := []byte(masterKey)

	sessionKey, err := crypto.DeriveSessionHMACKey(masterKey)
	require.NoError(t, err)

	assert.False(t, bytes.Equal(sessionKey, masterBytes),
		"derived session key must not equal raw master key bytes")

	exp := time.Now().Add(time.Hour)

	tokMaster, err := SignSession(masterBytes, "admin", exp, 0)
	require.NoError(t, err)
	_, errVerify := VerifySession(sessionKey, tokMaster, time.Now(), 0)
	assert.ErrorIs(t, errVerify, ErrSessionSignature,
		"master-key-signed cookie must not verify under derived session key")

	tokDerived, err := SignSession(sessionKey, "admin", exp, 0)
	require.NoError(t, err)
	_, errVerify2 := VerifySession(sessionKey, tokDerived, time.Now(), 0)
	assert.NoError(t, errVerify2,
		"derived-key-signed cookie must verify under derived session key")
}
