package wiring

import (
	"log/slog"
	"time"

	mdapp "github.com/alexmorbo/seasonfill/internal/mediadetail/app"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// MediaDetailBundle groups the universal MediaDetail engine skeleton (ADR-0022
// S1): an EMPTY section registry plus the generic read-through freshener and
// composer built over it. Mirror of MovieDetailBundle's holder-carrying shape.
//
// S1: no plugins are registered — the engine is inert (Freshener.EnsureFresh
// returns Fresh, Composer.Compose returns identity-only). Later stories register
// per-(type,section) plugins into Registry and bind Freshener.SetAsyncFallback
// at the composition-root late-bind zone.
type MediaDetailBundle struct {
	Registry  *mdapp.SectionRegistry
	Freshener *mdapp.Freshener
	Composer  *mdapp.Composer
}

// BuildMediaDetail assembles the engine skeleton. Takes only a logger in S1
// (the registry/freshener/composer need no repos yet); later stories extend the
// signature with *gorm.DB + enrichment seams as plugins are added. domainLog is
// tagged "http" to match the other detail wirers.
func BuildMediaDetail(log *slog.Logger) *MediaDetailBundle {
	domainLog := sharedports.DomainLogger(log, "http")
	registry := mdapp.NewSectionRegistry()
	freshener := mdapp.NewFreshener(registry, 5*time.Second, time.Now, domainLog)
	composer := mdapp.NewComposer(domainLog)
	return &MediaDetailBundle{
		Registry:  registry,
		Freshener: freshener,
		Composer:  composer,
	}
}
