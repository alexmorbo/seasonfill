package wiring

import (
	"context"
	"fmt"
	"log/slog"

	authapp "github.com/alexmorbo/seasonfill/internal/admin/app"
	infraoidc "github.com/alexmorbo/seasonfill/internal/admin/infrastructure/oidc"
	adminpersistence "github.com/alexmorbo/seasonfill/internal/admin/persistence"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// admin.go owns the wiring for the admin bounded context: the auth
// stack (Story 333 — admin user repo, OIDC provider cache, login UC,
// IP limiters, password bootstrap seed). Audit / health / metrics
// wiring belongs here once those subsystems acquire their own Build*
// wirers (currently they live inside the HTTP server construction).

// AuthBundle holds the auth-domain collaborators wired by BuildAuth.
// All five handles are passed-through to httpserver.NewServer (AdminRepo,
// LoginLimiter, WebhookLimiter, OIDCUC) and to the reload OIDC provider
// subscriber (OIDCCache) constructed in server.go's Run flow.
//
// AdminRepo  — admin_users CRUD, also the AdminUserRepository port the
//
//	OIDC login use case verifies against on group-ACL match.
//
// OIDCCache  — shared provider/discovery cache; the reload subscriber
//
//	invalidates it on issuer change.
//
// OIDCUC     — Authorization Code + PKCE use case, stateless beyond the
//
//	provider cache.
//
// LoginLimiter / WebhookLimiter — IP-keyed token bucket limiters with
//
//	the standard LoginLimit() / WebhookLimit() rates.
type AuthBundle struct {
	AdminRepo      *adminpersistence.AdminUserRepository
	OIDCCache      *infraoidc.ProviderCache
	OIDCUC         *authapp.OIDCLoginUseCase
	LoginLimiter   *authapp.IPLimiter
	WebhookLimiter *authapp.IPLimiter
}

// BuildAuth constructs the admin user repo, the OIDC provider cache,
// the OIDC login use case, the login + webhook IP limiters, and runs
// the admin password bootstrap (first-run seed; idempotent across
// restarts).
//
// The `bus` parameter is reserved — admin bootstrap does not currently
// publish runtime events. Keeping it in the signature matches the other
// wirers (BuildRuntimeConfig) so future auth events have a take-up path.
//
// The ctx parameter is reserved for future use. The current body uses a
// background context for the Bootstrap call to mirror the pre-refactor
// behaviour in Server.New: the seed must complete even if the parent
// ctx already carries a deadline applied by an outer test harness.
// Plumbing the parent ctx here would change the cancel semantics. Same
// pattern as BuildPersistence / BuildRuntimeConfig.
func BuildAuth(
	ctx context.Context,
	persistence *PersistenceBundle,
	bootCfg *config.Bootstrap,
	bus *runtime.Bus,
	log *slog.Logger,
) (*AuthBundle, error) {
	_ = ctx
	_ = bus
	bgCtx := context.Background()

	adminRepo := adminpersistence.NewAdminUserRepository(persistence.DB)
	oidcCache := infraoidc.NewProviderCache()
	oidcUC := authapp.NewOIDCLoginUseCase(oidcCache, adminRepo)

	// F-4b-8: bootstrap admin seeder emits auth-domain records
	// (admin-user creation, password-reset bootstrap).
	authLog := sharedports.DomainLogger(log, "auth")
	if err := authapp.Bootstrap(bgCtx, adminRepo, authapp.BootstrapConfig{
		WebUser:         bootCfg.Auth.WebUser,
		WebPassword:     bootCfg.Auth.WebPassword,
		WebPasswordHash: bootCfg.Auth.WebPasswordHash,
	}, authLog); err != nil {
		return nil, fmt.Errorf("auth bootstrap: %w", err)
	}

	loginLimiter := authapp.NewIPLimiter(authapp.LoginLimit(), 5)
	webhookLimiter := authapp.NewIPLimiter(authapp.WebhookLimit(), 60)

	return &AuthBundle{
		AdminRepo:      adminRepo,
		OIDCCache:      oidcCache,
		OIDCUC:         oidcUC,
		LoginLimiter:   loginLimiter,
		WebhookLimiter: webhookLimiter,
	}, nil
}
