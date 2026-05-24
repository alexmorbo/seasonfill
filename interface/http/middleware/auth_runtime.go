package middleware

import (
	"sync/atomic"
	"time"
)

// AuthRuntime carries the auth-side fields that must reload
// in-process without a restart. The fields are read on every
// request (SessionTTL → Login.exp; TrustedProxies → engine
// configuration), so the consumer must Load() the atomic per
// request, not cache the value.
type AuthRuntime struct {
	SessionTTL     time.Duration
	TrustedProxies []string
}

// AuthRuntimePointer is the atomic exposed by cmd/server to:
//
//   - The AuthHandler, which reads SessionTTL on Login.
//   - The authMiddleware reload subscriber, which Stores a fresh
//     value on every snapshot.
//
// Default = boot-time values; never nil after cmd/server init.
type AuthRuntimePointer = atomic.Pointer[AuthRuntime]
