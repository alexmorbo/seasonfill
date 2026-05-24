package middleware

import (
	"sync/atomic"
	"time"
)

// AuthRuntime carries the auth-side fields that must reload in-process
// without a restart. Every field is read on the hot request path:
//
//   - SessionTTL     → AuthHandler.Login (cookie max-age + token exp)
//   - TrustedProxies → gin engine SetTrustedProxies on rebuild
//   - SecureCookie   → AuthHandler.Login / Logout cookie Secure flag
//
// Callers MUST Load() the atomic per request — never cache the pointer
// across requests.
type AuthRuntime struct {
	SessionTTL     time.Duration
	TrustedProxies []string
	SecureCookie   bool
}

// AuthRuntimePointer is the atomic published by cmd/server to:
//   - AuthHandler, which reads SessionTTL + SecureCookie on every request.
//   - AuthMiddlewareSubscriber, which Stores a fresh value per snapshot.
//
// Default = boot-time values; never nil after cmd/server init.
type AuthRuntimePointer = atomic.Pointer[AuthRuntime]
