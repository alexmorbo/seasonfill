.PHONY: build test test-race test-coverage lint run clean tidy docker-build help

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
