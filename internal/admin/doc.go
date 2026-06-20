// Package admin is the bounded context that owns the seasonfill
// single-user admin surface: password bootstrap, forms login + session
// cookies, OIDC login (PKCE + group ACL), IP-keyed rate limiting on the
// login + webhook paths, and the audit-log stream.
//
// Layout (PRD §3.2 vertical slice, established in story 427 A-1-1 for
// mediaproxy, replicated here in story 428 A-1-2):
//
//	internal/admin/
//	  domain/         — AdminUser entity (single-user record)
//	  app/            — Bootstrap (first-run password seed),
//	                    OIDCLoginUseCase + OIDCTestUseCase,
//	                    IPLimiter (login + webhook rate limit)
//	  infrastructure/
//	    oidc/         — go-oidc/v3 ProviderCache (issuer-keyed)
//	    ratelimit/    — Wait helper for upstream-bound rate-limited
//	                    callers (TMDB CDN etc — used by mediaproxy
//	                    downloader; co-located with admin until story
//	                    443 / catalog extraction sorts cross-context
//	                    placement).
//
// Import direction (PRD §3.3 — enforced by the depcheck test):
//
//	app             → domain, infrastructure
//	infrastructure  → domain
//	domain          → (std lib + internal/shared/domain only)
//
// Cross-context boundary: the rest layer (handlers + middleware) still
// lives under interface/http/ during story 428. Story 430 will move it
// into internal/admin/rest/ alongside the bounded context. Until then,
// interface/http/handlers/{auth,oidc,audit}*.go and
// interface/http/middleware/auth*.go are the rest-layer consumers and
// import internal/admin/app + internal/admin/domain directly.
//
// Persistence: the production AdminUserRepository implementation still
// lives at infrastructure/database/repositories/admin_user_repository.go
// during story 428. Story 429 will move it into
// internal/admin/infrastructure/repositories/ and complete the vertical
// slice. The interface itself stays in application/ports for the same
// reason — every persistence repo's port still lives in application/ports
// until the catch-all is dismantled in a later sweep.
//
// Story origin:
//   - 036 — auth modes + dispatch order
//   - 037 — OIDC PKCE + group ACL
//   - 346 — global rate limiter (CDN-bound; co-located here)
//   - 427 — vertical-slice extraction protocol (mediaproxy)
//   - 428 — this layout
package admin
