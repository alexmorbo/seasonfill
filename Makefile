.PHONY: build test test-race test-coverage test-integration test-integration-sqlite test-integration-postgres test-integration-e2e test-all test-lint-rule lint vuln vuln-go vuln-web run clean tidy docker-build openapi openapi-check web-install web-dev web-build web-test web-lint web-image web-image-run help atlas-install migrations-diff migrations-lint migrations-diff-check migrations-apply-dev

BINARY := seasonfill
PKG    := github.com/alexmorbo/seasonfill

help:
	@echo "Targets: build test test-race test-coverage lint vuln run clean tidy docker-build"
	@echo "Atlas (dev-time schema tooling): atlas-install migrations-diff NAME=foo migrations-lint migrations-apply-dev"

build:
	CGO_ENABLED=0 go build -ldflags='-w -s' -trimpath -o bin/$(BINARY) ./cmd/server

test:
	go test ./... -short -cover

test-race:
	go test ./... -short -race -timeout 5m

test-coverage:
	PKGS=$$(go list ./... | grep -v '/cmd/'); \
	go test $$PKGS -short -race -coverprofile=coverage.out -covermode=atomic; \
	go tool cover -func=coverage.out | tail -1

# test-integration-sqlite runs the `integration` build tag suite with SQLite.
# Tests boot the full server in-process with SQLite; self-contained,
# ~3min total. Excluded from default `make test` / `make test-race` runs.
test-integration-sqlite:
	go test -tags integration -race -count=1 -timeout 15m ./...

# test-integration-postgres runs the `integration` build tag suite with Postgres.
# Story 424 (A-4-3) rolls out the dual-backend pattern across repository tests.
# Set SEASONFILL_TEST_POSTGRES_ENABLE to gate the Postgres backend in AllBackends(t).
# Requires Docker for testcontainers; ~6min total.
# -parallel=4 + GOMAXPROCS=4 caps concurrent test workers to prevent Postgres
# container OOM under high load on CI runners (2 CPU / 7GB RAM).
test-integration-postgres:
	SEASONFILL_TEST_POSTGRES_ENABLE=1 GOMAXPROCS=4 go test -tags integration -race -count=1 -timeout 20m -parallel=4 \
		./internal/shared/testhelpers \
		./infrastructure/database/repositories/...

# test-integration runs both sqlite and postgres integration suites in sequence.
test-integration: test-integration-sqlite test-integration-postgres

# test-integration-e2e runs the `integration_e2e` build tag suite (CI nightly-deep job).
# Long-running end-to-end flows (regrab full lifecycle, OIDC callback E2E).
# Always implies `integration` tag so middleware suites also load.
test-integration-e2e:
	go test -tags "integration integration_e2e" -race -count=1 -timeout 30m ./...

# test-all runs unit + integration + e2e in sequence. Mirrors nightly CI.
test-all: test-race test-integration test-integration-e2e

lint:
	golangci-lint run ./...

# test-lint-rule runs the typed-rules regression guards: the use-any
# rule (revive in .golangci.yml), the bare-id-int rule (AST scan in
# tests/lint_bare_id_int_test.go), and the modernize linter (Go 1.25+
# sugar, story 417 F-1 follow-up). All three are opt-in via the `lint`
# build tag and run in CI to catch regressions on type-discipline and
# code-modernization rules that golangci-lint and forbidigo cannot
# express in the main config. use-any/modernize need golangci-lint on
# PATH; bare-id-int is pure stdlib.
test-lint-rule:
	go test -tags lint -run 'TestUseAnyRejectsInterfaceLiteral|TestBareIDIntRegression|TestModernizeRejectsLegacyPatterns|TestNoGormInApplication|TestMediaProxyNoBackwardsImports|TestAdminNoBackwardsImports|TestAdminPersistenceNoBackwardsImports|TestAdminRestNoBackwardsImports|TestGrabNoBackwardsImports|TestGrabPersistenceNoBackwardsImports|TestGrabRestNoBackwardsImports|TestGrabAppEvaluateNoBackwardsImports|TestGrabAppDecisionNoBackwardsImports|TestGrabDomainDecisionNoBackwardsImports|TestWatchdogNoBackwardsImports|TestWatchdogDomainRegrabNoBackwardsImports|TestWatchdogDomainCooldownNoBackwardsImports|TestWatchdogPersistenceNoBackwardsImports|TestWatchdogInfrastructureNoBackwardsImports|TestWatchdogRestNoBackwardsImports|TestSharedClientsNoBackwardsImports|TestSharedReloadSchedulerNoBackwardsImports|TestSharedDBNoBackwardsImports|TestSharedHTTPNoBackwardsImports|TestEnrichmentNoBackwardsImports|TestCatalogNoBackwardsImports|TestSeriesDetailNoBackwardsImports|TestDiscoveryNoBackwardsImports|TestWiringNoBackwardsImports' ./tests/...

vuln: vuln-go vuln-web ## Run security vulnerability scanners (Go + web)

vuln-go: ## Scan Go code for known vulnerabilities (govulncheck, reachability mode)
	go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 ./...

vuln-web: ## Audit web dependencies for high+ severity vulnerabilities
	cd web && npm audit --audit-level=high

run:
	go run ./cmd/server -config config.yaml

clean:
	rm -rf bin/ coverage.out coverage.html

tidy:
	go mod tidy

docker-build:
	docker build -f deploy/docker/Dockerfile -t $(BINARY):latest .

# Regenerate docs/swagger.yaml + web/src/api/schema.ts.
# --v3.1 forces OpenAPI 3.1 output (swag's default is Swagger 2.0,
# which openapi-typescript v7 rejects).
openapi:
	go tool swag init -g internal/shared/http/edge/doc.go --overridesFile .swaggo -o docs --outputTypes yaml,json --v3.1
	cd web && npm install --no-audit --no-fund && npm run gen-types

# CI drift detector. Regenerates into temp dir and diffs against
# the committed artefacts. Also runs tsc on the generated TS.
openapi-check:
	@tmp=$$(mktemp -d); \
	go tool swag init -g internal/shared/http/edge/doc.go --overridesFile .swaggo -o $$tmp --outputTypes yaml,json --v3.1; \
	diff -u docs/swagger.yaml $$tmp/swagger.yaml || (echo "::error::docs/swagger.yaml is stale — run \`make openapi\`"; rm -rf $$tmp; exit 1); \
	rm -rf $$tmp
	cd web && npm install --no-audit --no-fund && npm run gen-types && \
	  git diff --exit-code src/api/schema.ts || \
	  (echo "::error::web/src/api/schema.ts is stale — run \`make openapi\`"; exit 1)

web-install:
	cd web && npm install --no-audit --no-fund

web-dev: web-install
	cd web && npm run dev

web-build: web-install
	cd web && npm run build

web-test: web-install
	cd web && npm run test

web-lint: web-install
	cd web && npm run lint && npm run typecheck

# Build the frontend Docker image locally. The context is `web/` so
# the .dockerignore + Dockerfile co-located with the SPA apply. Pin
# build args to the current short SHA + version string from
# package.json so the local image labels match the CI shape.
web-image:
	docker build \
		-f web/Dockerfile \
		--build-arg VITE_APP_VERSION=$$(node -p "require('./web/package.json').version") \
		--build-arg GIT_SHA=$$(git rev-parse --short HEAD 2>/dev/null || echo dev) \
		-t seasonfill-web:latest \
		web/

# Run the built frontend image on host port 8081. Useful for smoke-
# testing the SPA fallback (`curl localhost:8081/scans`) and the
# /healthz endpoint without booting the full Helm chart.
web-image-run: web-image
	docker run --rm -p 8081:8080 --name seasonfill-web-dev seasonfill-web:latest

# Install local pre-commit + pre-push git hooks. Requires `pre-commit`
# on PATH (brew install pre-commit OR pip install pre-commit). The
# config (.pre-commit-config.yaml) declares both hook types so a single
# `pre-commit install --install-hooks` registers .git/hooks/pre-commit
# AND .git/hooks/pre-push. Local-only gate; CI does not run pre-commit.
pre-commit-install: ## Install local pre-commit + pre-push hooks (local-only gate)
	@command -v pre-commit >/dev/null || { echo "Install: brew install pre-commit OR pip install pre-commit"; exit 1; }
	@# pre-commit refuses to install when core.hooksPath is set, even to the default.
	@# Unset locally if present so a fresh clone or a stale config doesn't break install.
	@git config --local --unset-all core.hooksPath 2>/dev/null || true
	pre-commit install --install-hooks

# Run the full pre-commit suite over every file in the tree. Useful
# before opening an MR or to sanity-check a freshly cloned checkout.
pre-commit-run: ## Run all pre-commit hooks on the whole tree
	pre-commit run --all-files

# ────────────────────────────────────────────────────────────────────
# Atlas dev-time schema tooling (PRD §6.6 / §D-1)
# ────────────────────────────────────────────────────────────────────
# Atlas is a dev-time codegen tool — production runtime uses golang-migrate
# to apply the generated SQL files. CI does NOT install atlas for the main
# test matrix; only the dedicated migrations-diff-check job (added in
# story 461 / D-1-8) requires the binary.

ATLAS_VERSION := v0.31.0

# Install the pinned atlas CLI into $GOPATH/bin (or GOBIN). Idempotent —
# re-running upgrades to the pinned version. Uses the official release
# binaries from release.ariga.io because the upstream `ariga.io/atlas/cmd/atlas`
# subpath carries `replace` directives that block `go install`.
# Falls back to a no-op exit code on `command -v` mismatch so CI can detect
# the misconfigured PATH.
atlas-install: ## Install pinned atlas CLI for dev-time schema work
	@echo "Installing atlas $(ATLAS_VERSION) ..."
	@os=$$(uname -s | tr 'A-Z' 'a-z'); \
	arch=$$(uname -m); \
	case "$$arch" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac; \
	dest_dir=$${GOBIN:-$$(go env GOPATH)/bin}; \
	mkdir -p "$$dest_dir"; \
	url="https://release.ariga.io/atlas/atlas-$$os-$$arch-$(ATLAS_VERSION)"; \
	echo "  -> $$url -> $$dest_dir/atlas"; \
	curl -sSL --fail "$$url" -o "$$dest_dir/atlas"; \
	chmod +x "$$dest_dir/atlas"
	@command -v atlas >/dev/null || { echo "ERROR: atlas not on PATH after install — ensure \$$GOPATH/bin (or GOBIN) is on PATH"; exit 1; }
	@atlas version

# Generate migration diff for BOTH dialects. NAME=... is required.
# Writes NNNNNN_<NAME>.{up,down}.sql to
# infrastructure/database/migrations/{postgres,sqlite}/.
#
# Atlas emits a Unix-timestamp prefix by default ("20260620234111_*.sql").
# Our legacy/runtime migrations under internal/shared/db/migrations/ use
# zero-padded sequential numbers ("000026_*.sql") and golang-migrate
# accepts both — but for visual consistency we rename Atlas-emitted files
# to the next sequential index after generation, then regenerate
# atlas.sum to match. See scripts/atlas-migrations-renumber.sh.
migrations-diff: ## Generate migration diff for both dialects (require NAME=)
	@test -n "$(NAME)" || (echo "Usage: make migrations-diff NAME=add_foo_column"; exit 1)
	@command -v atlas >/dev/null || (echo "atlas not found — run \`make atlas-install\`"; exit 1)
	atlas migrate diff $(NAME) --env postgres
	atlas migrate diff $(NAME) --env sqlite
	@./scripts/atlas-migrations-renumber.sh infrastructure/database/migrations/postgres
	@./scripts/atlas-migrations-renumber.sh infrastructure/database/migrations/sqlite
	atlas migrate hash --env postgres
	atlas migrate hash --env sqlite

# Lint all 16 migrations of each dialect — catches destructive ops,
# missing down, integrity hash drift, backwards-incompatible changes.
# Bumped from --latest 1 to --latest 13 in story 461 (D-1-8), from 13 to
# 15 in story 465b (D-4 scan_runs migration 000015), and from 15 to 16
# in story 466b (D-5 app_config + sonarr_instance_settings migration
# 000016). Every shipped migration is re-linted on every CI run.
migrations-lint: ## Lint all 25 migrations on both dialects
	@command -v atlas >/dev/null || (echo "atlas not found — run \`make atlas-install\`"; exit 1)
	atlas migrate lint --env postgres --latest 25
	atlas migrate lint --env sqlite --latest 25

# migrations-diff-check is the D-1-8 acceptance gate: it proves that the
# 13 committed migrations fully express schema.go on BOTH dialects. The
# probe runs `atlas migrate diff acceptance_probe --env <dialect>` and
# asserts atlas DID NOT emit a *_acceptance_probe.{up,down}.sql. If atlas
# emits any probe file, the schema has drifted from the migration tree —
# the target prints the offending SQL, cleans up, and exits non-zero.
#
# Atlas needs SEASONFILL_DATABASE_DSN for the postgres env block when
# using docker:// dev-DB (it spawns one ephemeral); for sqlite, atlas
# uses the in-memory dev-DB declared in atlas.hcl. CI sets the env in
# the .github/workflows/ci.yml job before invoking this target.
migrations-diff-check: ## D-1-8 gate — schema is fully expressed by all committed migrations on both dialects
	@command -v atlas >/dev/null || (echo "atlas not found — run \`make atlas-install\`"; exit 1)
	@set -e; for dialect in postgres sqlite; do \
	  echo ">>> diff probe ($$dialect)"; \
	  atlas migrate diff acceptance_probe --env $$dialect; \
	  PROBE=$$(ls infrastructure/database/migrations/$$dialect/*_acceptance_probe.up.sql 2>/dev/null || true); \
	  if [ -n "$$PROBE" ]; then \
	    echo "FAIL: atlas detected schema drift on $$dialect:"; \
	    cat "$$PROBE"; \
	    rm -f "$$PROBE" "$${PROBE%.up.sql}.down.sql"; \
	    atlas migrate hash --env $$dialect >/dev/null 2>&1 || true; \
	    exit 1; \
	  fi; \
	done
	@echo "OK: schema fully expressed on both dialects"

# Apply migrations to a local dev DB via atlas (dev convenience —
# production uses golang-migrate). Reads SEASONFILL_DATABASE_DRIVER to
# pick the env block from atlas.hcl.
migrations-apply-dev: ## Apply pending migrations to a local dev DB (atlas, not prod path)
	@command -v atlas >/dev/null || (echo "atlas not found — run \`make atlas-install\`"; exit 1)
	@test -n "$$SEASONFILL_DATABASE_DRIVER" || (echo "SEASONFILL_DATABASE_DRIVER must be set (postgres|sqlite)"; exit 1)
	atlas migrate apply --env $$SEASONFILL_DATABASE_DRIVER
