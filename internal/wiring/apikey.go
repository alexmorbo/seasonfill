package wiring

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

var ErrAPIKeyMismatch = errors.New("SEASONFILL_API_KEY mismatch: cannot decrypt stored secrets")

// ResolveAPIKey implements the bootstrap API-key flow:
//
// - envKey != "" && hasRow && probe != nil → validate via Open(ciphertext)
// - envKey != "" && (!hasRow || probe == nil) → encrypt-self, persist
// - envKey == "" && hasRow && probe != nil → error (can't decrypt without input)
// - envKey == "" && (!hasRow || probe == nil) → auto-gen 32-byte hex, persist, log banner
//
// Returns the master key (plaintext) used for AES-GCM.
func ResolveAPIKey(ctx context.Context, envKey string, repo ports.RuntimeConfigRepository, log *slog.Logger) (string, error) {
	row, err := repo.Get(ctx)
	hasRow := err == nil
	hasCiphertext := hasRow && len(row.APIKeyCiphertext) > 0

	switch {
	case envKey != "" && hasRow && hasCiphertext:
		// Validate the provided key can decrypt the stored probe.
		cipher, err := crypto.New(envKey)
		if err != nil {
			return "", fmt.Errorf("derive cipher: %w", err)
		}
		_, err = cipher.Open(row.APIKeyCiphertext)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrAPIKeyMismatch, err)
		}
		return envKey, nil

	case envKey != "" && (!hasRow || !hasCiphertext):
		// Derive cipher and persist an encrypt-self probe.
		cipher, err := crypto.New(envKey)
		if err != nil {
			return "", fmt.Errorf("derive cipher: %w", err)
		}
		probe, err := cipher.Seal([]byte(envKey))
		if err != nil {
			return "", fmt.Errorf("seal probe: %w", err)
		}
		if err := repo.SaveAPIKey(ctx, probe, false); err != nil {
			return "", fmt.Errorf("save api key: %w", err)
		}
		return envKey, nil

	case envKey == "" && hasRow && hasCiphertext:
		// DB holds encrypted secrets but no key was provided.
		return "", ErrAPIKeyMismatch

	case envKey == "":
		// Auto-generate a new key.
		key, err := generateHexKey(32)
		if err != nil {
			return "", fmt.Errorf("generate api key: %w", err)
		}
		cipher, err := crypto.New(key)
		if err != nil {
			return "", fmt.Errorf("derive cipher: %w", err)
		}
		probe, err := cipher.Seal([]byte(key))
		if err != nil {
			return "", fmt.Errorf("seal probe: %w", err)
		}
		if err := repo.SaveAPIKey(ctx, probe, true); err != nil {
			return "", fmt.Errorf("save api key: %w", err)
		}
		// Key MUST NOT enter the slog pipeline — log aggregators (Loki,
		// VictoriaLogs) would index it. Stdout is the one channel an
		// operator can capture out-of-band.
		_, _ = fmt.Fprintln(os.Stdout, "SEASONFILL_API_KEY="+key)
		_, _ = fmt.Fprintln(os.Stdout, "SEASONFILL_API_KEY_HELP: capture the line above and set it in your environment before the next restart")
		log.Info("FIRST-RUN: auto-generated SEASONFILL_API_KEY printed to stdout (not logged)",
			slog.Bool("auto_generated", true))
		return key, nil

	default:
		return "", errors.New("unreachable")
	}
}

// generateHexKey generates a random n-byte string as lowercase hex.
func generateHexKey(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random key: %w", err)
	}
	return hex.EncodeToString(b), nil
}
