package crypto

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_EmptyMasterKey(t *testing.T) {
	t.Parallel()
	_, err := New("")
	require.Error(t, err)
	assert.Equal(t, ErrEmptyMasterKey, err)
}

func TestSealOpen_RoundTrip(t *testing.T) {
	t.Parallel()
	masterKey := "test-master-key-for-aes-gcm"
	c, err := New(masterKey)
	require.NoError(t, err)

	plaintext := []byte("hello world")
	ciphertext, err := c.Seal(plaintext)
	require.NoError(t, err)

	decrypted, err := c.Open(ciphertext)
	require.NoError(t, err)

	assert.Equal(t, plaintext, decrypted)
}

func TestSealOpen_DifferentKey(t *testing.T) {
	t.Parallel()
	c1, err := New("key1")
	require.NoError(t, err)

	c2, err := New("key2")
	require.NoError(t, err)

	plaintext := []byte("secret")
	ciphertext, err := c1.Seal(plaintext)
	require.NoError(t, err)

	_, err = c2.Open(ciphertext)
	require.Error(t, err)
}

func TestSealOpen_TamperDetect(t *testing.T) {
	t.Parallel()
	c, err := New("key")
	require.NoError(t, err)

	plaintext := []byte("data")
	ciphertext, err := c.Seal(plaintext)
	require.NoError(t, err)

	ciphertext[len(ciphertext)-1] ^= 1

	_, err = c.Open(ciphertext)
	require.Error(t, err)
}

func TestOpen_CiphertextTooShort(t *testing.T) {
	t.Parallel()
	c, err := New("key")
	require.NoError(t, err)

	_, err = c.Open([]byte("short"))
	require.Error(t, err)
	assert.Equal(t, ErrCiphertextTooShort, err)
}

func TestSeal_NonceUniqueness(t *testing.T) {
	t.Parallel()
	c, err := New("key")
	require.NoError(t, err)

	plaintext := []byte("same plaintext")

	ct1, err := c.Seal(plaintext)
	require.NoError(t, err)

	ct2, err := c.Seal(plaintext)
	require.NoError(t, err)

	assert.NotEqual(t, ct1, ct2, "ciphertexts should differ due to random nonce")

	dec1, err := c.Open(ct1)
	require.NoError(t, err)
	assert.Equal(t, plaintext, dec1)

	dec2, err := c.Open(ct2)
	require.NoError(t, err)
	assert.Equal(t, plaintext, dec2)
}

func TestSeal_Empty(t *testing.T) {
	t.Parallel()
	c, err := New("key")
	require.NoError(t, err)

	ciphertext, err := c.Seal([]byte{})
	require.NoError(t, err)

	decrypted, err := c.Open(ciphertext)
	require.NoError(t, err)

	assert.Empty(t, decrypted)
}

func TestSeal_Large(t *testing.T) {
	t.Parallel()
	c, err := New("key")
	require.NoError(t, err)

	plaintext := bytes.Repeat([]byte("x"), 10000)
	ciphertext, err := c.Seal(plaintext)
	require.NoError(t, err)

	decrypted, err := c.Open(ciphertext)
	require.NoError(t, err)

	assert.Equal(t, plaintext, decrypted)
}
