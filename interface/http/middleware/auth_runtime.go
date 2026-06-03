package middleware

import (
	"net"
	"sync/atomic"
	"time"
)

// AuthRuntime carries the auth-side fields that must reload in-process
// without a restart. Every field is read on the hot request path:
//
//   - SessionTTL     → AuthHandler.Login (cookie max-age + token exp)
//   - TrustedProxies → gin engine SetTrustedProxies on rebuild
//   - SecureCookie   → AuthHandler.Login / Logout cookie Secure flag
//   - Mode           → RequireAuth dispatcher (forms|basic|none)
//   - LocalBypass    → 036c bypass middleware
//   - LocalNetworks  → 036c pre-parsed CIDR allow-list (subscriber
//     parses once, hot path does pure compare)
//   - SessionEpoch   → cookie verifier rejects payloads with ep < this
//
// Callers MUST Load() the atomic per request — never cache the pointer
// across requests.
type AuthRuntime struct {
	SessionTTL     time.Duration
	TrustedProxies []string
	SecureCookie   bool
	Mode           string
	LocalBypass    bool
	LocalNetworks  []*net.IPNet
	SessionEpoch   int64
	OIDC           OIDCRuntime
}

// OIDCRuntime mirrors runtime.OIDCSnapshot at the middleware layer. The
// start handler reads Issuer + ClientID + RedirectURL + Scopes from here
// to build the authorization-endpoint URL; the callback handler reads
// UsernameClaim + AllowedGroups to enforce the ACL.
type OIDCRuntime struct {
	Issuer        string
	ClientID      string
	RedirectURL   string
	Scopes        []string
	UsernameClaim string
	AllowedGroups []string
}

// AuthRuntimePointer is the atomic published by cmd/server to:
//   - AuthHandler, which reads SessionTTL + SecureCookie on every request.
//   - AuthMiddlewareSubscriber, which Stores a fresh value per snapshot.
//   - AuthConfigHandler, which reads Mode + LocalBypass.
//   - RequireAuthWithRuntime dispatcher.
//
// Default = boot-time values; never nil after cmd/server init.
type AuthRuntimePointer = atomic.Pointer[AuthRuntime]
