package dto

import "time"

// MovieDetailResponse is the wire aggregate for GET /api/v1/movies/:tmdb_id
// (Ф6-R-6a). Sourced from local repos; degraded lists absent slices.
type MovieDetailResponse struct {
	TMDBID     int                    `json:"tmdb_id"`
	IMDBID     *string                `json:"imdb_id"`
	Title      string                 `json:"title"`
	Overview   *string                `json:"overview"`
	Tagline    *string                `json:"tagline"`
	Year       *int                   `json:"year"`
	Status     *string                `json:"status"`
	Runtime    *int                   `json:"runtime_minutes"`
	Poster     *string                `json:"poster"`
	Backdrop   *string                `json:"backdrop"`
	Released   *time.Time             `json:"release_date"`
	Digital    *time.Time             `json:"digital_release_date"`
	Physical   *time.Time             `json:"physical_release_date"`
	TMDBRating *float64               `json:"tmdb_rating"`
	IMDBRating *float64               `json:"imdb_rating"`
	Collection *MovieDetailCollection `json:"collection"`
	Library    []MovieDetailLibrary   `json:"library"`
	// Genres are localized taxonomy chips mirroring the series hero (Ф2.5a).
	// Each chip carries its own resolved Language (en-US when the requested lang
	// had no row). Omitted when the movie has no genres attached yet.
	Genres []TaxonomyChip `json:"genres,omitempty"`
	// Keywords are localized taxonomy chips (Ф2.5a). v1 keywords are en-only, so
	// Language is en-US for any requested lang. Omitted when none attached.
	Keywords []TaxonomyChip `json:"keywords,omitempty"`
	// Studio is the headline production company name (first movie_companies row by
	// position). Omitted when the movie has no companies attached (cold/never-enriched).
	// Mirror of the series hero Studio (Ф2.5b).
	Studio *string `json:"studio,omitempty" example:"Legendary Pictures"`
	// Companies is the full production-company list in join order (position ASC).
	// Omitted when none attached.
	Companies []MovieDetailCompany `json:"companies,omitempty"`
	// Country is the first ISO 3166-1 alpha-2 origin country (Countries[0]).
	// DEPRECATED for new consumers — use Countries. Omitted when canon has none.
	Country *string `json:"country,omitempty" example:"US"`
	// Countries is the full origin-country list (ISO 3166-1 alpha-2 each), sourced
	// from canon.origin_countries. Omitted/empty → FE hides the row.
	Countries []string `json:"countries,omitempty" example:"US,CA"`
	// OriginalLanguage is the ISO 639-1 code (e.g. "en", "ru") from canon. FE renders
	// the localized display name via Intl.DisplayNames. Omitted when canon has none.
	OriginalLanguage *string `json:"original_language,omitempty" example:"en"`
	// OriginalTitle is the movie's title in its original language (canon.original_title,
	// e.g. "Sen to Chihiro no kamikakushi"). Distinct from the localized/display Title.
	// Omitted when canon has none. FE shows it as a subtitle only when it differs from Title.
	OriginalTitle *string `json:"original_title,omitempty" example:"Sen to Chihiro no kamikakushi"`
	// Homepage is the movie's official homepage URL from canon. Omitted when canon has none.
	Homepage *string `json:"homepage,omitempty" example:"https://www.dune-movie.com"`
	// Budget is the production budget in whole US dollars (canon.budget). A non-nil pointer
	// to 0 is a known "no reported budget" and the FE hides a zero-value money row (ADR §S5);
	// nil = unknown and is omitted. Omitted when canon has none.
	Budget *int64 `json:"budget,omitempty" example:"85000000"`
	// Revenue is the worldwide box-office gross in whole US dollars (canon.revenue). Same
	// nil-vs-zero semantics as Budget. Omitted when canon has none.
	Revenue *int64 `json:"revenue,omitempty" example:"451746275"`
	// Trailer is the single best official trailer. Omitted when the movie has no
	// trailer row (movie_videos empty). Reuses the shared dto.Trailer shape.
	Trailer *Trailer `json:"trailer,omitempty"`
	// SyncedAt is the movie's last successful base TMDB enrichment moment
	// (canon.enrichment_tmdb_synced_at). Mirrors the series detail synced-at that
	// backs the "synced N ago" footer microcopy. nil = never enriched (cold canon /
	// Radarr-only stub) → omitted so the FE MovieSyncFooter renders nothing.
	SyncedAt *time.Time `json:"synced_at,omitempty" example:"2026-08-17T03:14:00Z"`
	Degraded []string   `json:"degraded"`
}

// MovieDetailCompany is one production-company sidebar row (Ф2.5b). name / logo_asset /
// origin_country live on the production_companies dict row (no i18n side-table).
type MovieDetailCompany struct {
	TMDBID        *int    `json:"tmdb_id,omitempty"`
	Name          string  `json:"name" example:"Legendary Pictures"`
	LogoAsset     *string `json:"logo_asset,omitempty"`
	OriginCountry *string `json:"origin_country,omitempty" example:"US"`
}

// MovieDetailCollection is the franchise-collection header on a movie detail.
type MovieDetailCollection struct {
	TMDBCollectionID int     `json:"tmdb_collection_id"`
	Name             string  `json:"name"`
	Poster           *string `json:"poster"`
	RadarrMonitored  bool    `json:"radarr_monitored"`
}

// MovieDetailLibrary is one per-instance Radarr library-membership row.
type MovieDetailLibrary struct {
	InstanceName  string  `json:"instance_name"`
	RadarrMovieID int     `json:"radarr_movie_id"`
	Monitored     bool    `json:"monitored"`
	HasFile       bool    `json:"has_file"`
	Availability  *string `json:"availability"`
	SizeOnDisk    int64   `json:"size_on_disk_bytes"`
	// Quality is the downloaded release's quality name (Radarr
	// movieFile.quality.quality.name, e.g. "Bluray-1080p"). nil when the
	// instance has no file or the rich radarr-sync hasn't captured it yet.
	Quality *string `json:"quality,omitempty" example:"Bluray-1080p"`
	// Resolution is the vertical pixel resolution (Radarr
	// movieFile.quality.quality.resolution).
	Resolution *int `json:"resolution,omitempty" example:"1080"`
	// VideoCodec comes from Radarr movieFile.mediaInfo. nil when absent
	// (Radarr never probed the file) or no file.
	VideoCodec *string `json:"video_codec,omitempty" example:"x265"`
	// AudioCodec comes from Radarr movieFile.mediaInfo. nil when absent
	// (Radarr never probed the file) or no file.
	AudioCodec *string `json:"audio_codec,omitempty" example:"EAC3"`
}
