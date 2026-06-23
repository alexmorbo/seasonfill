// list.go hosts the discovery bounded-context value objects: the
// ranked-list kind enum (trending_day / popular / by_genre / by_network /
// by_keyword / trending_week), the materialised Item row, and the Page
// wrapper returned by DiscoveryListRepo.GetRanked.
//
// PRD §5.1.1 — every list row materialises onto a (kind, param, language,
// position, series_id, refreshed_at) tuple in the discovery_lists table.
// Item is the join projection over discovery_lists × series that the
// repository hydrates; Page wraps the items with the freshness clock
// the §5.1.1 refresh policy reads.
//
// Import rule (PRD §3.3): domain imports stdlib + internal/shared/domain
// only. Pinned by tests/lint_discovery_imports_test.go.
package domain

import (
	"time"

	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Kind is the discovery-list family. The string values match the
// `kind` column in discovery_lists (migration 000021) verbatim.
type Kind string

const (
	KindTrendingDay  Kind = "trending_day"
	KindTrendingWeek Kind = "trending_week"
	KindPopular      Kind = "popular"
	KindByGenre      Kind = "by_genre"
	KindByNetwork    Kind = "by_network"
	KindByKeyword    Kind = "by_keyword"
)

// IsValid reports whether k is one of the six known list kinds. Used by
// the worker (story 506) and the handler (story 507) at the IO boundary
// — every untrusted kind value MUST be IsValid()-gated before it
// reaches the repository.
func (k Kind) IsValid() bool {
	switch k {
	case KindTrendingDay, KindTrendingWeek, KindPopular,
		KindByGenre, KindByNetwork, KindByKeyword:
		return true
	default:
		return false
	}
}

// Item is the materialised join row returned by DiscoveryListRepo.GetRanked.
// Fields mirror PRD §5.1.1 line 612-625 (the response shape contract).
//
// Pointer fields encode "column was NULL on the joined series row" — the
// handler may surface a stub-only row whose TMDB metadata has not yet
// been hydrated (PRD §5.1.4 §`tmdb_type` internal filter relies on this).
//
// SeriesID is the local primary key (PRD A-5). TMDBID is a pointer
// because a stub upserted via the legacy Sonarr-orphan path may have
// NULL tmdb_id.
//
// OriginCountries arrives as a JSON-decoded slice (the `series.origin_countries`
// column is text-stored JSON per migration 000041). Genres is populated
// by the handler at projection time (story 507), so the repository
// leaves it nil-or-empty.
//
// TMDBType holds the TMDB content-type discriminator (0..6) the
// §5.1.4 filter rides on. The `series.tmdb_type` column is NOT exposed
// by SeriesModel in `internal/shared/db/models.go`; the repository
// hydrates the field via raw SQL Scan.
type Item struct {
	SeriesID        shareddomain.SeriesID
	TMDBID          *shareddomain.TMDBID
	Title           string
	Year            *int
	PosterPath      *string
	BackdropPath    *string
	OriginCountries []string
	Genres          []string
	TMDBType        *int
}

// Page is the GetRanked return shape: the materialised items plus the
// freshness clock the refresh policy reads.
//
// RefreshedAt is the max(discovery_lists.refreshed_at) over the queried
// (kind, param, language) tuple — a single page is a single
// ReplaceList atomic write, so every row carries the same timestamp.
//
// Total is the unpaged row count (used by the handler to render the
// "X of Y" pager). Returned via a second cheap COUNT(*) — the
// discovery_lists_lookup_idx covers the predicate so the count is
// index-only.
type Page struct {
	Items       []Item
	RefreshedAt time.Time
	Total       int
}
