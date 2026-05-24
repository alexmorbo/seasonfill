package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

const (
	hkdfSalt = "seasonfill-runtime-config-v1"
	hkdfInfo = "aes-gcm-key"
	keyLen   = 32 // AES-256
	nonceLen = 12
	tagLen   = 16
)

var (
	ErrEmptyMasterKey     = errors.New("master key is empty")
	ErrCiphertextTooShort = errors.New("ciphertext too short")
)

type Cipher struct{ aead cipher.AEAD }

func New(masterKey string) (*Cipher, error) {
	if masterKey == "" {
		return nil, ErrEmptyMasterKey
	}
	r := hkdf.New(sha256.New, []byte(masterKey), []byte(hkdfSalt), []byte(hkdfInfo))
	key := make([]byte, keyLen)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("hkdf read: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes new: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm new: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	ct := c.aead.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, len(nonce)+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

func (c *Cipher) Open(blob []byte) ([]byte, error) {
	if len(blob) < nonceLen+tagLen {
		return nil, ErrCiphertextTooShort
	}
	nonce, ct := blob[:nonceLen], blob[nonceLen:]
	pt, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("aead open: %w", err)
	}
	return pt, nil
}
