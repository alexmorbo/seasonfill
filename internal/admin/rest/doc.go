// Package rest hosts the admin-context Gin handlers that translate
// HTTP requests into use-case calls against the admin bounded
// context. It is the interface-layer leaf of the admin vertical
// slice extracted by story 430 (A-1-4) out of the catch-all
// interface/http/handlers/ tree.
//
// Endpoints owned by this package:
//
//   - GET  /healthz                       (always 200, liveness probe)
//   - GET  /readyz                        (database probe; never gates on Sonarr)
//   - GET  /metrics                       (Prometheus text exposition)
//   - GET  /api/v1/auth/config            (public — auth-mode bootstrap)
//   - POST /api/v1/auth/login             (cookie session mint)
//   - GET  /api/v1/auth/session           (guarded; current session)
//   - DELETE /api/v1/auth/session         (logout)
//   - POST /api/v1/auth/password          (guarded; change password)
//   - GET  /api/v1/auth/oidc/start        (public — OIDC PKCE start)
//   - GET  /api/v1/auth/oidc/callback     (public — OIDC PKCE callback)
//   - POST /api/v1/auth/oidc/test         (guarded; admin)
//   - GET  /api/v1/settings/timezone      (guarded)
//   - PATCH /api/v1/settings/timezone     (guarded)
//
// Sub-package internal/admin/rest/healthcheck hosts the periodic
// Sonarr-instance probe Checker (used by /readyz and watchdog).
//
// Story 352 catalog media-pending hooks live in media_pending.go —
// they are imported by interface/http/handlers/instances.go and
// interface/http/handlers/audit.go (the catch-all catalog handlers
// still own the read-side that drives the eager poster_hash
// projection). The DTO converter ToGrabDTO + the unexported
// scan/decision/grab converters live alongside the audit handler
// here; the latter is still a handlers/ resident (depends on the
// pagination + error-response helpers that the broader catalog
// surface owns).
//
// Vertical-slice boundary: this leaf MUST NOT import other
// horizontal-CA layers except via the explicit kernel-shaped
// carve-outs listed in tests/lint_admin_imports_test.go. The
// dedicated regression guard TestAdminRestNoBackwardsImports pins
// the rule to this story.
package rest
