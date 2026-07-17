# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26
ARG BUILDER_REGISTRY=docker.io
ARG RUNTIME_REGISTRY=gcr.io

FROM --platform=$BUILDPLATFORM ${BUILDER_REGISTRY}/library/golang:${GO_VERSION}-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG GIT_SHA=dev

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

COPY go.mod go.sum* ./
RUN if [ -f go.sum ]; then go mod download && go mod verify; fi

COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false \
    -ldflags="-w -s -X main.version=${GIT_SHA}" \
    -o /build/bin/seasonfill \
    ./cmd/server

FROM ${RUNTIME_REGISTRY}/distroless/static-debian12:nonroot

ARG GIT_SHA=dev
LABEL org.opencontainers.image.title="seasonfill" \
      org.opencontainers.image.description="Seasonfill server" \
      org.opencontainers.image.source="https://github.com/alexmorbo/seasonfill" \
      org.opencontainers.image.url="https://github.com/alexmorbo/seasonfill" \
      org.opencontainers.image.licenses="GPL-3.0-or-later" \
      org.opencontainers.image.vendor="AlexMorbo" \
      org.opencontainers.image.revision="${GIT_SHA}"

COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /build/bin/seasonfill /app/seasonfill

USER nonroot:nonroot
WORKDIR /app

EXPOSE 8080
ENTRYPOINT ["/app/seasonfill"]
