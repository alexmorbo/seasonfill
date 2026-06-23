// Package rest hosts the catalog-context Gin handlers that translate
// HTTP requests into use-case calls against the catalog bounded
// context. It is the interface-layer leaf of the catalog vertical
// slice extracted by story 444 (A-1-18) out of the catch-all
// interface/http/handlers/ tree.
//
// Endpoints owned by this package:
//
//   - POST /api/v1/scan                                    (manual scan trigger)
//   - POST /api/v1/scans/:id/cancel                        (cancel running scan)
//   - GET  /api/v1/instances                               (instance health snapshot)
//   - POST /api/v1/instances                               (create instance)
//   - GET  /api/v1/instances/:name                         (instance detail)
//   - PATCH /api/v1/instances/:name                        (update instance)
//   - DELETE /api/v1/instances/:name                       (delete instance)
//   - POST /api/v1/instances/probe                         (probe Sonarr config)
//   - GET  /api/v1/counters                                (aggregate counters)
//   - GET  /api/v1/runtime-config                          (runtime config get)
//   - PATCH /api/v1/runtime-config                         (runtime config update)
//   - POST /api/v1/webhook/sonarr/:instance_name           (inbound Sonarr webhook)
//   - POST /api/v1/instances/:name/webhook/install         (webhook install)
//   - GET  /api/v1/instances/:name/webhook/status          (webhook status)
//   - GET  /api/v1/webhooks/status                         (webhook aggregate)
//
// Shared types exported for cross-context reuse:
//
//   - InstanceRegistry — reload-aware Sonarr instance snapshot accessor.
//     Consumed by internal/grab/rest, internal/watchdog/rest,
//     cmd/server/adapters (webhook checker + reconciler + regrab adapter),
//     and the catch-all interface/http/handlers (qbit_discover) that
//     have not yet been pulled into their own vertical slice.
//   - InstanceLister — minimal `ListNames(ctx) []string` shape consumed
//     by WebhooksAggregateHandler (this package) and
//     internal/watchdog/rest WatchdogRollupHandler.
//   - WebhookProcessor — the Process(ctx, evt) port the webhook handler
//     dispatches against; satisfied by internal/catalog/app/webhook.UseCase.
//
// Local helpers (helpers.go + media_hash.go) duplicate the small
// write-error / poster-hash helpers from interface/http/handlers so the
// catalog rest layer does not have to import the catch-all package
// (avoiding the cycle that would arise from handlers → catalogrest for
// InstanceRegistry and catalogrest → handlers for the helpers).
//
// Vertical-slice boundary: this leaf MUST NOT import other horizontal-CA
// layers except via the explicit kernel-shaped carve-outs listed in
// tests/lint_catalog_imports_test.go (the catalog regression guard
// installed by story 441 A-1-15 and extended for each follow-up vertical
// move). Story 444 extends that guard to cover the new rest leaf.
package rest
