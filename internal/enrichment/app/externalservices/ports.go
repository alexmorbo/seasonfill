package externalservices

import (
	"context"
	"time"

	infra "github.com/alexmorbo/seasonfill/internal/shared/clients/externalservices"
)

// Repository is the port the use case consumes. infrastructure
// supplies the implementation; application owns the contract.
type Repository interface {
	Get(ctx context.Context, svc infra.Service) (infra.Settings, error)
	List(ctx context.Context) ([]infra.Settings, error)
	Upsert(ctx context.Context, s infra.Settings) error
	MarkTest(ctx context.Context, svc infra.Service, at time.Time, outcome infra.Outcome, message string) error
}

// Publisher is the reload-bus hand-off. The use case calls Publish
// after every successful Upsert so the SonarrClients-style
// ExternalServicesSubscriber re-builds its cached *http.Client.
// Implementation lives in cmd/server — application stays decoupled.
type Publisher interface {
	Publish(ctx context.Context)
}
