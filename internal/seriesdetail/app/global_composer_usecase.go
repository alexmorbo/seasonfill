// Package seriesdetail — see ports.go header.
//
// global_composer_usecase.go (Story 491 / N-1a, rewritten B1b-1). The
// GlobalComposerUseCase is the entry point behind GET /api/v1/series/:id.
// B1b-1 collapsed the former two-path dispatch (per-instance Composer.Get
// vs TMDBFallbackUseCase.GetCanonical) into a single canon-only
// SkeletonComposer.Compose call: SkeletonComposer computes
// in_library_instances itself, so the same skeleton path serves both a
// series that is in a library and a TMDB-only series ([] distinguishes
// them in the DTO). The response is the above-fold SkeletonDTO — Sonarr /
// qBit / seasons / cast / recommendations move to their own endpoints
// (§7.0 bounded-context separation).
package seriesdetail

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// SkeletonComposerPort is the narrow seam the GlobalComposerUseCase
// delegates to. *SkeletonComposer satisfies it; tests inject a fake so
// they don't have to stand up the full skeleton dependency graph. B1b-1.
type SkeletonComposerPort interface {
	Compose(ctx context.Context, seriesID domain.SeriesID, lang values.LanguageTag) (SkeletonDTO, error)
}

// GlobalComposerDeps — narrow ports the global composer needs.
//
// Skeleton: SkeletonComposerPort. *SkeletonComposer satisfies it.
// Logger:   domain="composer" anchor.
type GlobalComposerDeps struct {
	Skeleton SkeletonComposerPort
	Logger   *slog.Logger
}

// GlobalComposerUseCase is the application use case wired to the global
// /api/v1/series/:id endpoint.
type GlobalComposerUseCase struct {
	d GlobalComposerDeps
}

// NewGlobalComposerUseCase constructs the use case. Skeleton is required.
// Logger defaults to a domain-tagged slog.
func NewGlobalComposerUseCase(d GlobalComposerDeps) (*GlobalComposerUseCase, error) {
	if d.Skeleton == nil {
		return nil, fmt.Errorf("globalcomposer: Skeleton required")
	}
	if d.Logger == nil {
		d.Logger = sharedports.DomainLogger(slog.Default(), "composer")
	}
	return &GlobalComposerUseCase{d: d}, nil
}

// Get resolves the series.id to the above-fold SkeletonDTO.
//
// lang is the raw ?lang query string; it is parsed into a BCP-47
// LanguageTag VO. Empty / non xx-XX input defaults to en-US (LanguageTag
// requires the strict 5-char form; SkeletonComposer.Compose consumes the
// VO). ports.ErrNotFound on an invalid id → handler 404/400; a missing
// canon row bubbles ErrNotFound up from SkeletonComposer.
func (u *GlobalComposerUseCase) Get(ctx context.Context, seriesID domain.SeriesID, lang string) (SkeletonDTO, error) {
	if seriesID <= 0 {
		return SkeletonDTO{}, fmt.Errorf("globalcomposer: invalid series id %d: %w", seriesID, ports.ErrNotFound)
	}
	tag, err := values.NewLanguageTag(strings.TrimSpace(lang))
	if err != nil {
		// Query omitted this or sent a non xx-XX tag; default to en-US.
		tag, _ = values.NewLanguageTag("en-US")
	}
	dto, cerr := u.d.Skeleton.Compose(ctx, seriesID, tag)
	if cerr != nil {
		return SkeletonDTO{}, fmt.Errorf("globalcomposer: skeleton compose: %w", cerr)
	}
	u.d.Logger.InfoContext(ctx, "global_series_composed",
		slog.Int64("series_id", int64(seriesID)),
		slog.String("lang", tag.Value()),
		slog.Int("in_library_count", len(dto.InLibraryInstances)),
	)
	return dto, nil
}
