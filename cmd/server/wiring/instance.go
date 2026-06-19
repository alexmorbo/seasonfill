package wiring

import (
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/alexmorbo/seasonfill/application/instance"
	handlers "github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// InstanceBundle groups the instance-domain components constructed at boot.
// Returned by BuildInstance. Threaded into httpserver.NewServer (CRUDHandler,
// ProbeHandler) — the HTTP wirer remains in server.go for now.
//
// Field-level invariants:
//
//   - UC owns the WithWebhookReconciler + WithWebhookStatusCache chained
//     setters from the webhook bundle (story 335). The adapter is the
//     pre-baked WebhookBundle.ReconcilerAdapter — same pointer identity
//     as everywhere else.
//
//   - CRUDHandler wraps UC for the /api/v1/instances CRUD routes.
//
//   - ProbeHandler is the stateless POST /api/v1/instances/test handler;
//     it holds its own *http.Client (tuned for probe: 5s dial + TLS +
//     response-header timeouts, 64 KiB response-header cap, redirects
//     short-circuited with ErrUseLastResponse so probe assertions can
//     inspect the original status / Location).
//
//   - ProbeClient is exposed on the bundle for symmetry / tests; the
//     handler owns the only production reference.
type InstanceBundle struct {
	UC           *instance.UseCase
	CRUDHandler  *handlers.InstanceCRUDHandler
	ProbeHandler *handlers.InstanceProbeHandler
	ProbeClient  *http.Client
}

// BuildInstance wires the instance.UseCase + CRUD handler + Probe handler
// + probe HTTP client.
//
// Construction order mirrors the pre-336 inline body in server.go verbatim:
//
//  1. instance.New(instanceRepo, runtimeRepo, cipher, bus, log) chained
//     through WithWebhookReconciler(webhook.ReconcilerAdapter) +
//     WithWebhookStatusCache(webhook.StatusCache).
//  2. handlers.NewInstanceCRUDHandler(uc, log).
//  3. *http.Client tuned for probe (5s dial + TLS + response-header
//     timeouts, 64 KiB response-header cap, short-circuited redirects).
//  4. handlers.NewInstanceProbeHandler(probeClient, log).
//
// No error path — every step is in-memory construction. The signature
// returns error for symmetry with the other Build* wirers.
func BuildInstance(
	persistence *PersistenceBundle,
	webhook *WebhookBundle,
	bus *runtime.Bus,
	log *slog.Logger,
) (*InstanceBundle, error) {
	// F-4b-8: instance CRUD UC is the operator-facing admin surface for
	// Sonarr instance management — operator-driven mutations belong to
	// the "admin" slot.
	adminLog := sharedports.DomainLogger(log, "admin")
	uc := instance.New(
		persistence.InstanceRepo,
		persistence.RuntimeRepo,
		persistence.Cipher,
		bus,
		adminLog,
	).
		WithWebhookReconciler(webhook.ReconcilerAdapter).
		WithWebhookStatusCache(webhook.StatusCache)

	crudHandler := handlers.NewInstanceCRUDHandler(uc, log)

	probeClient := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext:            (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			TLSHandshakeTimeout:    5 * time.Second,
			ResponseHeaderTimeout:  5 * time.Second,
			MaxResponseHeaderBytes: 64 << 10,
		},
	}
	probeHandler := handlers.NewInstanceProbeHandler(probeClient, log)

	return &InstanceBundle{
		UC:           uc,
		CRUDHandler:  crudHandler,
		ProbeHandler: probeHandler,
		ProbeClient:  probeClient,
	}, nil
}
