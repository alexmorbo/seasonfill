# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26
ARG BUILDER_REGISTRY=docker.io
ARG RUNTIME_REGISTRY=gcr.io

FROM --platform=$BUILDPLATFORM ${BUILDER_REGISTRY}/library/golang:${GO_VERSION}-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

COPY go.mod go.sum* ./
RUN if [ -f go.sum ]; then go mod download && go mod verify; fi

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags='-w -s' -trimpath \
    -o /build/bin/seasonfill \
    ./cmd/server

FROM ${RUNTIME_REGISTRY}/distroless/static-debian12:nonroot

COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /build/bin/seasonfill /app/seasonfill

USER nonroot:nonroot
WORKDIR /app

EXPOSE 8080
ENTRYPOINT ["/app/seasonfill"]
