// Package app is the generic orchestration layer of the universal MediaDetail
// engine (ADR-0022). It depends ONLY on its own domain
// (internal/mediadetail/domain), the standard library, and
// golang.org/x/sync/singleflight. It NEVER imports enrichment/app,
// seriesdetail/app, or moviedetail/app — the vertical adapters implement the
// SectionPlugin port and are wired at the composition root.
package app

import (
	"context"
	"time"

	"github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
)

// SectionPlugin is the narrow port a per-(MediaType, Section) adapter implements.
// It is the single mechanism that removes `if type` branching (ADR-0022 D3): the
// engine drives every plugin uniformly and each plugin encapsulates its own
// type-specific reads/writes.
//
// A plugin exposes BOTH freshness shapes so the engine never needs to know which
// shape a given section uses:
//
//   - Coverage: fractional/series-shape freshness. Returns how many of the
//     expected items are present (e.g. localized cast names covered vs total).
//     total==0 means "coverage does not apply to this plugin" — a NO-OP; the
//     engine treats covered<total as stale ONLY when total>0.
//   - Staleness: boolean/movie-shape freshness. Returns a SectionVerdict whose
//     Stale bool is the contract (stamp TTL / changes). A plugin that uses only
//     coverage NO-OPs this by returning SectionVerdict{Stale:false}.
//
// The CONTRACT: a plugin implements the ONE shape relevant to its section and
// NO-OPs the other (Coverage → (0,0,nil); Staleness → {Stale:false}). Combining
// both means the engine has no type knowledge. IO errors from either method are
// treated fail-CLOSED by the Freshener (that check contributes "not stale") so a
// flaky read never forces a synchronous refresh storm.
type SectionPlugin interface {
	// Coverage reports (covered, total) localized/expected items for id+lang.
	// total==0 → coverage not applicable (no-op).
	Coverage(ctx context.Context, id domain.MediaID, lang string) (covered, total int, err error)

	// Staleness reports the boolean freshness verdict for id+lang at now.
	Staleness(ctx context.Context, id domain.MediaID, lang string, now time.Time) (domain.SectionVerdict, error)

	// Refresh fetches from the source of truth (TMDB) and writes the section
	// for id+lang. Type-specific; idempotent + COALESCE-safe by contract.
	Refresh(ctx context.Context, id domain.MediaID, lang string) error

	// Section returns the canonical section this plugin owns.
	Section() domain.Section
}
