package middleware

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
