// row.go declares the ADR-0017 D-1 row-config domain: the customisable
// discovery-row descriptor + the curated code-default set rendered when
// the discovery_rows table is empty. Global (pre-RBAC — no user_id;
// ADR-0017 defers per-user to Phase 8).
//
// ENUM SYNC (ADR-0017 §D-1): RowType / RowSource / MediaType are the
// single source of truth. FE mirrors them in web/src/api/discoveryRows.ts.
// Any change here MUST update that file + the DiscoveryRails render switch.
package domain

// RowType classifies a discovery row. FE dispatches the item fetch by
// this value (NOT by source): trending/popular hit the curated endpoints,
// the tmdb_discover types hit /discovery/discover, recently_added hits
// the library list.
type RowType string

const (
	RowTypeTrending         RowType = "trending"
	RowTypePopular          RowType = "popular"
	RowTypeUpcoming         RowType = "upcoming"
	RowTypeGenre            RowType = "genre"
	RowTypeNetwork          RowType = "network"
	RowTypeKeyword          RowType = "keyword"
	RowTypeWatchProvider    RowType = "watch_provider"
	RowTypeRecentlyAdded    RowType = "recently_added"
	RowTypeUpcomingReleases RowType = "upcoming_releases"
)

// IsValid reports whether t is a known row type. Used by the S2 write
// endpoint (add/reorder); S1 GET is read-only and never validates input.
func (t RowType) IsValid() bool {
	switch t {
	case RowTypeTrending, RowTypePopular, RowTypeUpcoming, RowTypeGenre,
		RowTypeNetwork, RowTypeKeyword, RowTypeWatchProvider,
		RowTypeRecentlyAdded, RowTypeUpcomingReleases:
		return true
	default:
		return false
	}
}

// RowSource selects the read backend (ADR-0017 F-12). tmdb_discover =
// parametrised discover engine + LRU; library = series_cache projection.
type RowSource string

const (
	SourceTMDBDiscover RowSource = "tmdb_discover"
	SourceLibrary      RowSource = "library"
)

func (s RowSource) IsValid() bool {
	return s == SourceTMDBDiscover || s == SourceLibrary
}

// MediaType is the ADR-0017 Phase-6 movie задел. S1 default set is all tv.
type MediaType string

const (
	MediaTypeTV    MediaType = "tv"
	MediaTypeMovie MediaType = "movie"
)

func (m MediaType) IsValid() bool {
	return m == MediaTypeTV || m == MediaTypeMovie
}

// Row is one discovery rail descriptor. Params is an opaque string→string
// bag of /discovery/discover query-param keys (with_genres, with_networks,
// sort_by, first_air_date.gte, …) for tmdb_discover rows; empty for
// trending/popular/recently_added (those dispatch by RowType, not params).
// ID is 0 for a code-default row (not yet persisted); non-zero once the
// row lives in discovery_rows. Title is the Russian rail heading.
type Row struct {
	ID        int64
	RowType   RowType
	Source    RowSource
	MediaType MediaType
	Params    map[string]string
	Position  int
	Enabled   bool
	Title     string
}

// DefaultRows is the curated code-default set (ADR-0017 §D-1 "MVP набор").
// Rendered verbatim when discovery_rows is empty. All media_type=tv (movies
// land after Radarr, Phase 6). Titles are Russian per the UI language.
//
// Param keys are the EXACT /discovery/discover query-param names the
// discover handler parses (internal/discovery/rest/discover_handler.go
// parse()): with_genres / with_networks / sort_by / first_air_date.gte.
// sort_by ∈ closed set {popularity.desc, vote_average.desc,
// first_air_date.desc, first_air_date.asc}. Genre 18 = Drama, network 213 = Netflix (TMDB ids).
//
// upcoming_releases carries only sort_by here; the FE injects a live
// first_air_date.gte=<today>. The upcoming row's FE injects a live
// first_air_date.gte=<today-45d> + .lte=<today> window (static dates would rot).
func DefaultRows() []Row {
	return []Row{
		{Position: 0, RowType: RowTypeTrending, Source: SourceTMDBDiscover, MediaType: MediaTypeTV, Enabled: true, Title: "Тренды", Params: map[string]string{}},
		{Position: 1, RowType: RowTypePopular, Source: SourceTMDBDiscover, MediaType: MediaTypeTV, Enabled: true, Title: "Популярное", Params: map[string]string{}},
		{Position: 2, RowType: RowTypeUpcoming, Source: SourceTMDBDiscover, MediaType: MediaTypeTV, Enabled: true, Title: "Новые сериалы", Params: map[string]string{"sort_by": "popularity.desc", "vote_count.gte": "10"}},
		{Position: 3, RowType: RowTypeRecentlyAdded, Source: SourceLibrary, MediaType: MediaTypeTV, Enabled: true, Title: "Недавно добавленное", Params: map[string]string{}},
		{Position: 4, RowType: RowTypeUpcomingReleases, Source: SourceTMDBDiscover, MediaType: MediaTypeTV, Enabled: true, Title: "Скоро на экраны", Params: map[string]string{"sort_by": "first_air_date.asc"}},
		{Position: 5, RowType: RowTypeGenre, Source: SourceTMDBDiscover, MediaType: MediaTypeTV, Enabled: true, Title: "Драмы", Params: map[string]string{"with_genres": "18", "sort_by": "popularity.desc"}},
		{Position: 6, RowType: RowTypeNetwork, Source: SourceTMDBDiscover, MediaType: MediaTypeTV, Enabled: true, Title: "Netflix", Params: map[string]string{"with_networks": "213", "sort_by": "popularity.desc"}},
	}
}
