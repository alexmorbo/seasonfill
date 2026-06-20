// Package externalservices owns the shared HTTP client primitive
// (HttpClientFor) that every outbound integration in the codebase
// builds on, plus the runtime-config Settings UseCase that tracks
// per-service keys (TMDB / OMDb / Trakt) and quota state.
//
// Two surfaces:
//
//  1. http_client.go — HttpClientFor(settings) returns an *http.Client
//     with proxy / transport pool / timeout overrides baked in. TMDB,
//     OMDb, and the orphan-resolution path all hit this entry point;
//     the per-service proxy isolation keeps a flaky provider from
//     poisoning the shared transport pool.
//
//  2. settings.go — runtime Settings UseCase that owns the
//     external_services table CRUD + quota / key rotation state.
//     The cmd/server adapters (external_services_subscriber.go +
//     extsvc_client_holders.go) subscribe to the reload bus and
//     swap in fresh TMDB/OMDb clients when a key flips.
//
// Story 435 A-1-9 moved this package from infrastructure/externalservices/
// into the shared kernel so the enrichment + mediaproxy + seriesdetail
// contexts can each consume it without taking an infrastructure
// dependency. The kernel boundary is enforced by
// tests/lint_shared_clients_imports_test.go.
package externalservices
