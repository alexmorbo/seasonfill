package wiring

import (
	"log/slog"

	appextsvc "github.com/alexmorbo/seasonfill/application/externalservices"
	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	infraextsvc "github.com/alexmorbo/seasonfill/infrastructure/externalservices"
	handlers "github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// ExtSvcBundle groups the external-services runtime-config components
// constructed at boot. Returned by BuildExtSvc. Threaded into:
//
//   - httpserver.NewServer (Handler) — the HTTP wirer remains in
//     server.go for now.
//   - server.go calls Sub.Start(rootCtx, nil) directly because the
//     subscriber owner needs the cancellation-bearing rootCtx, which
//     the wirer does not (and should not) own.
//
// Field-level invariants:
//
//   - Sub is the runtime-config subscriber. Built FIRST with a nil use
//     case so it can be injected as the use case's Publisher; the use
//     case is then constructed and back-wired via Sub.SetUseCase before
//     callers see the bundle.
//
//   - UC owns the masked-DTO contract for the HTTP layer. Plaintext
//     keys never leave the subscriber/use case pair — the Handler
//     emits the masked DTO only.
//
//   - Handler is the HTTP adapter wrapping UC.
type ExtSvcBundle struct {
	Sub     *adapters.ExternalServicesSubscriber
	UC      *appextsvc.UseCase
	Handler *handlers.ExternalServicesHandler
}

// BuildExtSvc wires the Story 202 (S-2) external-services runtime-config
// stack. Construction order mirrors the pre-339 inline body verbatim:
//
//  1. Repository (cipher-wrapped settings repo backed by persistence.DB).
//  2. Subscriber (built with nil UC).
//  3. UseCase (subscriber injected as Publisher; bootCfg.ExternalServices
//     supplies the env lookup; production tester).
//  4. SetUseCase back-wires the subscriber.
//  5. Handler wraps the UC.
//
// Start(rootCtx, nil) is NOT called here — server.go owns rootCtx and
// fires the prime after Build returns, matching the original temporal
// position (before the wireEnrichment block).
//
// No error path — every step is in-memory construction. The signature
// returns error for symmetry with the other Build* wirers.
func BuildExtSvc(
	persistence *PersistenceBundle,
	bootCfg *config.Bootstrap,
	bus *runtime.Bus,
	log *slog.Logger,
) (*ExtSvcBundle, error) {
	// F-4b-8: subscriber records describe configuration loading at boot
	// (cache prime + on-operator-change apply); admin tag covers the
	// operator-facing UC (TMDB/OMDb credential rotation). PRD §6.5.
	bootLog := sharedports.DomainLogger(log, "boot")
	adminLog := sharedports.DomainLogger(log, "admin")
	extRepo := infraextsvc.NewRepository(persistence.DB, persistence.Cipher)
	sub := adapters.NewExternalServicesSubscriber(bus, bootLog)
	uc := appextsvc.NewUseCase(extRepo, bootCfg.ExternalServices.Lookup(),
		appextsvc.NewRealTester(), sub, adminLog)
	sub.SetUseCase(uc)
	handler := handlers.NewExternalServicesHandler(uc, log)

	return &ExtSvcBundle{
		Sub:     sub,
		UC:      uc,
		Handler: handler,
	}, nil
}
