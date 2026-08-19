package app

import (
	"context"
	"log/slog"

	"github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// Composer assembles the normalized MediaDetail aggregate from the registered
// plugins' readers after the Freshener has run. One Composer serves both
// verticals (the section readers are plugin-provided).
//
// S1 SCOPE: skeletal. Compose returns a MediaDetail carrying only the identity.
// Later ADR-0022 section stories grow it to read each plugin's composed output
// (text, cast, recs, media, keywords, seasons, collection, hero). Kept
// compiling with a trivial method so the wiring + package exist from S1.
type Composer struct {
	log *slog.Logger
}

// NewComposer constructs the Composer. log nil → default domain logger.
func NewComposer(log *slog.Logger) *Composer {
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "composer")
	}
	return &Composer{log: log}
}

// Compose returns the assembled MediaDetail for id+lang. S1: identity-only.
func (c *Composer) Compose(_ context.Context, id domain.MediaID, _ string) (domain.MediaDetail, error) {
	return domain.MediaDetail{ID: id}, nil
}
