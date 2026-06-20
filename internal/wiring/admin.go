package wiring

// admin.go owns the wiring for the admin bounded context: the auth
// stack (Story 333 — admin user repo, OIDC provider cache, login UC,
// IP limiters, password bootstrap seed). Audit / health / metrics
// wiring belongs here once those subsystems acquire their own Build*
// wirers (currently they live inside the HTTP server construction).
