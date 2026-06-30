// types.go declares the wire DTOs for the discovery HTTP surface
// (story 507 N-2f). Shapes pin PRD §5.1 lines 483-499 + §5.1.1
// cold-start envelope (lines 665-678).
//
// Domain rule: this file imports stdlib + gin-free types only. The
// handler converts disco.Item → DiscoverySeriesItem at projection
// time; the worker / repository never see these structs.
package rest

import "time"

// DiscoverySeriesItem is one row of GET /api/v1/discovery/* responses.
// Shape per PRD §5.1 lines 483-499 + story 554 (Z5).
//
// SeriesID is the local catalog primary key. TMDBID is the public
// TMDB id when present (a stub may have NULL tmdb_id). Pointer
// fields encode "column was NULL on the joined series row".
//
// TVDBID is surfaced because Sonarr's POST /api/v3/series identifies
// the series by TVDB id (story 523 / N-4 unblock). Optional — a stub
// upserted via the legacy Sonarr-orphan path may not have it; the FE
// AddToSonarr modal disables Submit until it appears (worker enrichment
// will fill it on the next /series/{id} pass).
//
// OriginalLanguage is the ISO 639-1 tag from TMDB (e.g. "en", "ja").
// Optional for the same legacy-stub reason as TVDBID; the FE renders
// a chip when present.
//
// InLibraryInstances is the list of Sonarr instance slugs the series
// is registered to. Empty slice (never nil) when not in any library
// — a discovery hit on a TMDB-only stub still returns [] so the FE
// can render an "Add to library" CTA. Story 507 populates this from
// a future cross-instance lookup; until N-2g lands the slice ships
// as []string{} unconditionally.
//
// Genres is the localised genre name slice — populated by the handler
// at projection time from the series_genres × genres_i18n join. The
// repo leaves it nil; the handler renders [] when empty.
//
// PosterHash / BackdropHash (story 554, audit §10.4 F-1) carry the
// sha256-hex content address that the FE feeds straight into
// mediaUrl() → GET /api/v1/media/:hash. The legacy PosterPath /
// BackdropPath fields ALSO carry the same hash value (not the raw
// TMDB path) during the FE bundle CDN transition window — once stale
// bundles are fully evicted (≥7d post-deploy of story 554) a follow-up
// can drop the legacy field names. Story 554 motivation: PLAN §10.4
// F-1 audit finding — the legacy name implied a raw TMDB path even
// though story 526 had already wired the resolver to emit a hash; the
// next contributor reading the type was a regression-bug-in-waiting.
type DiscoverySeriesItem struct {
	ID                 int64    `json:"series_id"`
	TMDBID             *int     `json:"tmdb_id,omitempty"`
	TVDBID             *int     `json:"tvdb_id,omitempty"`
	Title              string   `json:"title"`
	OriginalTitle      *string  `json:"original_title,omitempty"`
	OriginalLanguage   *string  `json:"original_language,omitempty"`
	Year               *int     `json:"year,omitempty"`
	PosterHash         *string  `json:"poster_hash,omitempty"`   // story 554 — new wire name
	PosterPath         *string  `json:"poster_path,omitempty"`   // legacy mirror of PosterHash
	BackdropHash       *string  `json:"backdrop_hash,omitempty"` // story 554 — new wire name
	BackdropPath       *string  `json:"backdrop_path,omitempty"` // legacy mirror of BackdropHash
	TMDBRating         *float64 `json:"tmdb_rating,omitempty"`
	IMDBRating         *float64 `json:"imdb_rating,omitempty"`
	Status             *string  `json:"status,omitempty"`
	InLibraryInstances []string `json:"in_library_instances"`
	Genres             []string `json:"genres,omitempty"`
}

// DiscoveryListResponse wraps the paged item slice + freshness +
// cold-start hints. Per PRD §5.1.1 lines 660-678.
//
// Degraded carries non-fatal status hints that change the FE render:
//   - "discovery_warming" — worker has not yet completed first refresh
//   - "tmdb_throttled"    — last refresh hit a 429 (data may be stale)
//   - "refresh_failed"    — on-demand refresh errored but stale rows
//     are being returned anyway
//   - "genre_unknown_to_tmdb" — on-demand refresh returned 0 items
//     for a long-tail param.
//
// WarmingEst is the rough seconds-until-first-list-ready estimate.
// Populated only when "discovery_warming" appears in Degraded.
type DiscoveryListResponse struct {
	Items       []DiscoverySeriesItem `json:"items"`
	RefreshedAt time.Time             `json:"refreshed_at"`
	Page        int                   `json:"page"`
	PerPage     int                   `json:"per_page"`
	Total       int                   `json:"total"`
	Degraded    []string              `json:"degraded,omitempty"`
	WarmingEst  *int                  `json:"warming_estimate_seconds,omitempty"`
}

// WarmingEstimateSeconds is the constant value surfaced when the
// worker is still in cold-start. Picked from PRD §5.1.1 line 678 —
// "30s is the 95th-percentile cold-start latency at homelab scale".
const WarmingEstimateSeconds = 30
