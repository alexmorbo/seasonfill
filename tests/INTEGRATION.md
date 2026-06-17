# Integration test tiers

Seasonfill splits Go tests into three tiers via build tags. This document maps each file to its tier and explains how to add new tests.

## Tier table

| Tier | Build tag | CI job | Local target | Trigger | Timeout |
|------|-----------|--------|---------------|---------|---------|
| Unit | (none) | `unit` | `make test-race` | Every push | 5m |
| Integration | `integration` | `integration` | `make test-integration` | PR + push to `main` / `v*` tag | 15m |
| End-to-end | `integration_e2e` (implies `integration`) | `nightly-deep` | `make test-integration-e2e` | Cron `0 3 * * *` UTC + `workflow_dispatch` | 30m |

## Files by tier

### `integration` (CI: integration job)

- `tests/integration/scan_integration_test.go` — boots in-process scan use-case against SQLite + fake Sonarr fixture.
- `tests/integration/real_grab_integration_test.go` — exercises grab pipeline + GUID cooldown against fake Sonarr HTTP.
- `infrastructure/mediastore/s3_integration_test.go` — round-trips an object against a real S3 endpoint. Skips when `MEDIASTORE_S3_TEST_ENDPOINT` is unset.

### `integration_e2e` (CI: nightly-deep job)

- `tests/integration/regrab_e2e_test.go` — full regrab lifecycle: grab → cooldown → second grab attempt blocks.
- `tests/integration/oidc_callback_e2e_test.go` — full OIDC callback flow including group ACL gating. Currently stub-only (`t.Skip`).

## Conventions for new tests

### Choose the tier

- **Unit**: pure function, single repo, ≤1s wall, no goroutines that outlive the test, no external network. Default. No tag.
- **Integration**: boots multiple subsystems (use-case + repo + fake transport), ≤5s wall, deterministic, no external service. Tag with `//go:build integration`.
- **E2E**: end-to-end flow across the entire stack (HTTP handler → use-case → repo → outbound), or full lifecycle. Often >5s wall. Tag with `//go:build integration_e2e`.

### Required header for tagged tests

```go
//go:build integration

package mypkg
```

The blank line between the tag directive and the `package` clause is mandatory (Go build constraint syntax). Do NOT include the legacy `// +build integration` form — Go 1.18+ accepts the new form exclusively.

For E2E:

```go
//go:build integration_e2e

package mypkg
```

`integration_e2e` does NOT imply `integration` at the parser level — but our nightly job invokes `go test -tags "integration integration_e2e"` so both compile together.

### Local repro

```bash
# Unit tier (fast, every-commit):
make test-race

# Integration tier (CI integration job):
make test-integration

# E2E tier (CI nightly):
make test-integration-e2e

# Everything (matches CI nightly-deep):
make test-all
```

For S3 integration locally:

```bash
export MEDIASTORE_S3_TEST_ENDPOINT=http://localhost:9000
export MEDIASTORE_S3_TEST_KEY=...
export MEDIASTORE_S3_TEST_SECRET=...
make test-integration
```

Without those env vars, `TestS3Store_RoundTrip` skips silently.

### Verifying tier membership

```bash
go test -short -race -run TestMyNewTest ./path/to/pkg/      # unit
go test -tags integration -race -run TestMyNewTest ./...     # integration
go test -tags "integration integration_e2e" -race -run TestMyNewTest ./...  # e2e
```

A test in the wrong tier reports `no tests to run`.
