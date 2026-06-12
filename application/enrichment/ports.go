// Package enrichment owns the workflow surface shared by the three
// enrichment workers (series, person, OMDb). Story 207 shipped the
// merge-policy / TTL / degraded helpers in domain/enrichment; story
// 209 adds the TMDBClient port here at the application layer. The
// port intentionally returns RAW infrastructure response types —
// the worker is the unit of merge-policy enforcement, and the
// mapper functions live next to the client. Workers import
// infrastructure/tmdb for both the constructor AND the mapper
// functions; the port abstraction exists for swap-out in tests,
// not for layer purity.
package enrichment

import (
	"context"

	"github.com/alexmorbo/seasonfill/infrastructure/tmdb"
)

// TMDBClient is the substitution seam C-2 / C-3 use under test.
// The production implementation is *tmdb.Client (see
// infrastructure/tmdb). A test double implements this interface
// directly, returning fixture responses.
type TMDBClient interface {
	// GetTV fetches /tv/{id} with the canonical append_to_response.
	// Language is BCP-47 ("en-US" / "ru-RU").
	GetTV(ctx context.Context, id int64, language string) (*tmdb.TVResponse, error)

	// GetSeason fetches /tv/{id}/season/{n}.
	GetSeason(ctx context.Context, tvID int64, seasonNumber int, language string) (*tmdb.SeasonResponse, error)

	// GetPerson fetches /person/{id} with tv_credits, movie_credits,
	// external_ids.
	GetPerson(ctx context.Context, id int64, language string) (*tmdb.PersonResponse, error)

	// FindByTVDB resolves a tvdb_id to a TMDB id via /find. Returns
	// nil on empty result; the worker treats nil as
	// sync_log.outcome=not_found.
	FindByTVDB(ctx context.Context, tvdbID int64) (*tmdb.FindResponse, error)
}
