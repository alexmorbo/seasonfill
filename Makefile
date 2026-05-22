.PHONY: build test test-race test-coverage lint run clean tidy docker-build openapi openapi-check web-install web-dev web-build web-test web-lint web-image web-image-run help

BINARY := seasonfill
PKG    := github.com/alexmorbo/seasonfill

help:
	@echo "Targets: build test test-race test-coverage lint run clean tidy docker-build"

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

lint:
	golangci-lint run ./...

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
