.PHONY: build test test-race test-coverage test-integration test-integration-sqlite test-integration-postgres test-integration-e2e test-all test-lint-rule lint vuln vuln-go vuln-web run clean tidy docker-build openapi openapi-check web-install web-dev web-build web-test web-lint web-image web-image-run help

BINARY := seasonfill
PKG    := github.com/alexmorbo/seasonfill

help:
	@echo "Targets: build test test-race test-coverage lint vuln run clean tidy docker-build"

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
test-integration-postgres:
	SEASONFILL_TEST_POSTGRES_ENABLE=1 go test -tags integration -race -count=1 -timeout 20m \
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
	go test -tags lint -run 'TestUseAnyRejectsInterfaceLiteral|TestBareIDIntRegression|TestModernizeRejectsLegacyPatterns|TestNoGormInApplication|TestMediaProxyNoBackwardsImports|TestAdminNoBackwardsImports|TestAdminPersistenceNoBackwardsImports|TestAdminRestNoBackwardsImports' ./tests/...

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
	go tool swag init -g interface/http/doc.go -o docs --outputTypes yaml,json --v3.1
	cd web && npm install --no-audit --no-fund && npm run gen-types

# CI drift detector. Regenerates into temp dir and diffs against
# the committed artefacts. Also runs tsc on the generated TS.
openapi-check:
	@tmp=$$(mktemp -d); \
	go tool swag init -g interface/http/doc.go -o $$tmp --outputTypes yaml,json --v3.1; \
	diff -u docs/swagger.yaml $$tmp/swagger.yaml || (echo "::error::docs/swagger.yaml is stale — run \`make openapi\`"; rm -rf $$tmp; exit 1); \
	rm -rf $$tmp
	cd web && npm install --no-audit --no-fund && npm run gen-types && \
	  git diff --exit-code src/api/schema.ts || \
	  (echo "::error::web/src/api/schema.ts is stale — run \`make openapi\`"; exit 1)
	cd web && npm run typecheck

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
