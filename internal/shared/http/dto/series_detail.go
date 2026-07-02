// Package dto — series detail DTO file (Story 215 / PRD v4 §9 row 1
// and §5.6 composite read path). The shape is the locked-down
// frontend contract: stories I-1 / I-2 / I-3 consume it. Every
// field below documents its zero-value semantics — nil vs empty vs
// absent — because the frontend renders the page from this one
// JSON document.
package dto

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeriesDetailResponse — the composite document returned by
// GET /api/v1/instances/:name/series/:id?lang=. Every nested
// struct corresponds to a UI section in the design brief §2; field
// ordering mirrors the visual top-down order so reviewers can map
// JSON → page at a glance.
type SeriesDetailResponse struct {
	// Instance is the Sonarr instance the request hit.
	// Echoed for clients that need to disambiguate cross-instance
	// state (a series can exist on multiple instances).
	Instance domain.InstanceName `json:"instance" example:"alpha"`
	// SonarrSeriesID is the Sonarr-side id from the URL.
	SonarrSeriesID domain.SonarrSeriesID `json:"sonarr_series_id" example:"123"`
	// SeriesID is the resolved canonical series.id. Useful to
	// frontend for sibling endpoints (cast, person) that key on
	// canonical id rather than Sonarr id.
	SeriesID domain.SeriesID `json:"series_id" example:"42"`
	// Lang is the BCP-47 language code the response was rendered
	// in. Echoes the request when present; defaults to "en-US"
	// when the request omitted ?lang= or sent an invalid value.
	Lang string `json:"lang" example:"ru-RU"`

	// Hero is the page header (backdrop + poster + title + meta).
	// Always present (the canon row always exists post-208 cutover).
	Hero SeriesHero `json:"hero"`
	// Library is the Sonarr-derived "what's on disk" tile. Always
	// present — Sonarr is the system of record per design brief §2.4.
	// Counts come from series_cache.missing_count + episode_states.
	Library LibraryStrip `json:"library"`
	// Download is the single active in-flight Sonarr queue item, if
	// any. nil when the queue is empty OR Sonarr is unreachable
	// (then degraded[] includes "sonarr").
	Download *DownloadChip `json:"download,omitempty"`
	// Overview is the localised description block. nil when no
	// series_texts row exists in any language (rare — cold series
	// before TMDB sync).
	Overview *OverviewAside `json:"overview,omitempty"`
	// Recent is the last-5 activity events for the Library tile.
	// EMPTY in this story (recent_activity_deferred — see §9 note);
	// frontend treats empty as "no recent activity yet".
	Recent []RecentEvent `json:"recent"`
	// Torrents is the torrent inventory section (design brief §4).
	// In this story it is always SyncPending=true + Items=[] — full
	// implementation comes from stories A-1..A-4 (219..222).
	Torrents TorrentsHint `json:"torrents"`
	// Seasons is the seasons-accordion data — one entry per season,
	// episodes included (lazy-load avoided per PRD §5.5: TMDB sync
	// pulls all seasons eagerly on first pass, so reading them all
	// here is cheap).
	Seasons []Season `json:"seasons"`
	// Cast is the top-10 cast carousel. Empty slice when no
	// series_people rows exist (cold series or TMDB stub).
	Cast []CastMember `json:"cast"`
	// Recommendations is the "you might also like" carousel. Empty
	// when no rows in series_recommendations (cold series, or TMDB
	// returned no recommendations).
	Recommendations []Recommendation `json:"recommendations"`
	// ExternalLinks is the IMDb / TMDB / TVDB / homepage footer row.
	// Always present (struct is never nil); inner fields are
	// individually nil when the corresponding id is missing.
	ExternalLinks ExternalLinks `json:"external_links"`
	// Degraded is the list of sources that produced stale or absent
	// data. UI renders a stale affordance per source. Empty slice
	// when every source is fresh.
	Degraded []string `json:"degraded"`
	// InLibraryInstances is the sorted list of Sonarr instance names that
	// currently carry this series (canonical series.id resolution). Empty
	// slice `[]` when the series is in zero libraries (TMDB-only canon).
	// Always present on the wire — the FE branches "Add to Sonarr" vs
	// per-instance widgets on `length > 0`. Story 491 / N-1a.
	InLibraryInstances []string `json:"in_library_instances" example:"homelab,beta"`
	// SyncedAt is the request timestamp (server-side now()); the
	// frontend uses it for the "synced Xs ago" microcopy.
	SyncedAt time.Time `json:"synced_at"`
}

// SeriesHero — backdrop + poster + meta block (design brief §2.1).
type SeriesHero struct {
	Title         string  `json:"title" example:"Breaking Bad"`
	OriginalTitle *string `json:"original_title,omitempty"`
	Tagline       *string `json:"tagline,omitempty"`
	YearStart     *int    `json:"year_start,omitempty" example:"2008"`
	YearEnd       *int    `json:"year_end,omitempty" example:"2013"`
	// Status is one of "continuing", "ended", "canceled",
	// "in_production", "upcoming", or "unknown". Mapping from
	// TMDB / Sonarr → these tokens lives in the composer; frontend
	// renders the pill colour from this enum, NOT from the raw
	// upstream string.
	Status         string  `json:"status" example:"ended"`
	RuntimeMinutes *int    `json:"runtime_minutes,omitempty" example:"45"`
	PosterAsset    *string `json:"poster_asset,omitempty"`
	BackdropAsset  *string `json:"backdrop_asset,omitempty"`
	// TitleLanguage is the BCP-47 language the Title was served in.
	// Empty when no series_texts row was found and the canon row's
	// own title was used.
	TitleLanguage string `json:"title_language,omitempty" example:"ru-RU"`
	// Ratings — TMDB and IMDb sides; nil when the source has no
	// score for this series. Two-rating row is the design brief's
	// RatingDuo component.
	TMDBRating *RatingScore `json:"tmdb_rating,omitempty"`
	IMDbRating *RatingScore `json:"imdb_rating,omitempty"`
	// Genres are localised chips (max 5 rendered, the composer
	// returns all available — frontend caps display).
	Genres []TaxonomyChip `json:"genres"`
	// Networks are the network-logo strip (max 3 displayed).
	Networks []NetworkChip `json:"networks"`
	// Studio is the headline production company name (first row of
	// series_companies ordered by position). nil when the series has
	// no companies attached (cold series, no TMDB sync).
	Studio *string `json:"studio,omitempty" example:"Sony Pictures Television"`
	// Country is the ISO 3166-1 alpha-2 origin country code (e.g. "US",
	// "RU"). FE maps the token to a localised label. nil when the canon
	// row has no origin_country (cold series). DEPRECATED for new consumers:
	// use Countries (plural) instead — Country is kept as Countries[0] for
	// back-compat with pre-365 clients.
	Country *string `json:"country,omitempty" example:"US"`
	// Countries is the full origin-country list (ISO 3166-1 alpha-2 each).
	// Empty/nil → FE hides the "Страны" row. FE localises each code via
	// Intl.DisplayNames and switches the label between singular/plural
	// based on length. Sourced from TMDB's `origin_country` array.
	Countries []string `json:"countries,omitempty" example:"US,CA"`
	// PremiereDate is the series' first-air-date as ISO YYYY-MM-DD. nil
	// when canon.first_air_date is NULL. FE formats locale-aware via
	// Intl.DateTimeFormat. Date-only (no timezone) — the underlying TMDB
	// field is a calendar date, not an instant.
	PremiereDate *string `json:"premiere_date,omitempty" example:"2026-05-28"`
	// OriginalLanguage is the BCP-47 / ISO 639-1 code (e.g. "en", "ru").
	// nil when canon.original_language is NULL. FE renders the localised
	// display name via Intl.DisplayNames({type:'language'}).
	OriginalLanguage *string `json:"original_language,omitempty" example:"en"`
	// ContentRating is the displayed age-rating badge. nil when no
	// content_ratings row matches the user locale OR en-US OR US
	// fallback.
	ContentRating *ContentRatingBadge `json:"content_rating,omitempty"`
	// Trailer is the single best official YouTube trailer. nil when
	// no videos row matches (no trailer hidden by design brief §2.1).
	Trailer *Trailer `json:"trailer,omitempty"`
	// NextEpisode is the "Next Episode" card data when available;
	// nil collapses the card to text-only states ("not yet
	// scheduled" / "ended" / "in production").
	NextEpisode *NextEpisode `json:"next_episode,omitempty"`
}

// RatingScore — two-source rating row entry.
type RatingScore struct {
	Score float64 `json:"score" example:"8.7"`
	Votes int     `json:"votes" example:"2031"`
}

// TaxonomyChip — localised name + canonical id for genres/keywords.
type TaxonomyChip struct {
	ID       int64  `json:"id"`
	Name     string `json:"name" example:"Drama"`
	Language string `json:"language" example:"en-US"`
}

// NetworkChip — network logo strip entry.
type NetworkChip struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name" example:"AMC"`
	LogoAsset *string `json:"logo_asset,omitempty"`
}

// ContentRatingBadge — single displayed age-rating badge after
// locale-fallback resolution (user locale → en-US → US).
type ContentRatingBadge struct {
	CountryCode string `json:"country_code" example:"US"`
	Rating      string `json:"rating" example:"TV-MA"`
}

// Trailer — single best official YouTube trailer.
type Trailer struct {
	Site        string     `json:"site" example:"YouTube"`
	Key         string     `json:"key" example:"X9F1jh5jc-Y"`
	Name        string     `json:"name"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
}

// NextEpisode — what's-next card data.
type NextEpisode struct {
	SeasonNumber  int        `json:"season_number"`
	EpisodeNumber int        `json:"episode_number"`
	Title         *string    `json:"title,omitempty"`
	AirDate       *time.Time `json:"air_date,omitempty"`
}

// InProgress — single best in-flight Sonarr queue episode for the LibraryStrip
// in-progress pill. nil when no record has status=="downloading". Story 379.
type InProgress struct {
	SeasonNumber  int     `json:"season_number" example:"5"`
	EpisodeNumber int     `json:"episode_number" example:"3"`
	Title         *string `json:"title,omitempty"`
	// Percent is computed server-side from (size − sizeleft) / size, rounded
	// to integer 0..100. 0 when upstream reports zero size.
	Percent int `json:"percent" example:"45"`
}

// LibraryStrip — Sonarr-derived "what's on disk" tile (design
// brief §2.4). The progress bar + count line are derived from
// these fields on the client.
type LibraryStrip struct {
	Monitored      bool `json:"monitored"`
	EpisodesTotal  int  `json:"episodes_total"`
	EpisodesOnDisk int  `json:"episodes_on_disk"`
	EpisodesAired  int  `json:"episodes_aired"`
	MissingCount   int  `json:"missing_count"`
	// SizeOnDiskBytes is the sum of episode_states.size_bytes for
	// this series + instance. 0 when nothing on disk yet.
	SizeOnDiskBytes int64 `json:"size_on_disk_bytes"`
	// DominantQuality is the most common quality string across
	// on-disk episode_states rows (e.g., "WEB-DL 1080p"). Empty
	// when nothing on disk.
	DominantQuality string `json:"dominant_quality"`
	// InProgress — story 379. Live Sonarr queue best pick for the
	// in-progress pill. nil when no record is downloading OR Sonarr
	// is unreachable (then degraded[] includes "sonarr").
	InProgress *InProgress `json:"in_progress,omitempty"`
}

// DownloadChip — single in-flight Sonarr queue item (design brief
// §2.4 — overlaps conceptually with Torrents but stays here for
// the Library tile's one-line summary).
type DownloadChip struct {
	QueueID      int    `json:"queue_id"`
	EpisodeID    int    `json:"episode_id,omitempty"`
	SeasonNumber int    `json:"season_number,omitempty"`
	Title        string `json:"title"`
	Status       string `json:"status" example:"downloading"`
	Protocol     string `json:"protocol,omitempty" example:"torrent"`
	DownloadID   string `json:"download_id,omitempty"`
}

// OverviewAside — localised description block (design brief §2.2).
type OverviewAside struct {
	Overview string `json:"overview"`
	// Language is the BCP-47 language the Overview was served in.
	// Empty only when no row was found in any language (rare).
	Language string `json:"language" example:"ru-RU"`
	// Keywords are the small tag chips below the overview.
	Keywords []TaxonomyChip `json:"keywords"`
	// Awards is the OMDb awards line ("Won 16 Emmys..."). nil when
	// no OMDb sync ran or awards = "N/A".
	Awards *string `json:"awards,omitempty"`
}

// SeriesOverviewResponse — wire shape of GET /api/v1/series/:id/overview
// (Story 529 — decomposition 1/3). Embeds the existing OverviewAside as
// the overview payload + the slim ids/lang/degraded envelope shared by
// the other per-section endpoints.
type SeriesOverviewResponse struct {
	Instance       domain.InstanceName   `json:"instance"`
	SonarrSeriesID domain.SonarrSeriesID `json:"sonarr_series_id"`
	SeriesID       domain.SeriesID       `json:"series_id"`
	Lang           string                `json:"lang" example:"ru-RU"`
	Overview       OverviewAside         `json:"overview"`
	Degraded       []string              `json:"degraded"`
}

// SeriesRecommendationsResponse — wire shape of
// GET /api/v1/series/:id/recommendations (Story 530 — decomposition 2/3).
// Mirrors SeriesOverviewResponse envelope shape so the FE hook layer has
// a uniform reading pattern across all per-section endpoints. Items
// reuses the existing Recommendation DTO from the monolith response.
type SeriesRecommendationsResponse struct {
	Instance       domain.InstanceName   `json:"instance"`
	SonarrSeriesID domain.SonarrSeriesID `json:"sonarr_series_id"`
	SeriesID       domain.SeriesID       `json:"series_id"`
	Items          []Recommendation      `json:"items"`
	TotalCount     int                   `json:"total_count" example:"42"`
	HasMore        bool                  `json:"has_more"`
	Limit          int                   `json:"limit" example:"20"`
	Offset         int                   `json:"offset" example:"0"`
	Degraded       []string              `json:"degraded"`
}

// RecentEvent — one row of the last-5 activity strip. Empty in
// this story (recent_activity_deferred — §9 note); the type stays
// in the DTO so frontend can iterate the (always present) slice.
type RecentEvent struct {
	EventType string `json:"event_type" example:"imported"`
	// Subject is a one-line human description ("S05E03").
	Subject string    `json:"subject"`
	At      time.Time `json:"at"`
}

// TorrentsHint — torrents section placeholder. In this story
// SyncPending is always true and Items is always nil — A-*
// branch fills both. The shape stays here so the DTO contract is
// stable when A-* lands.
type TorrentsHint struct {
	// SyncPending=true means "torrent inventory not yet available
	// for this series". UI hides the section or shows a quiet
	// skeleton — design brief §4.5 covers the empty-state shapes.
	SyncPending bool `json:"sync_pending"`
	// Count is the number of known torrents; 0 in this story.
	Count int `json:"count"`
	// TotalSizeBytes — aggregate size; 0 in this story.
	TotalSizeBytes int64 `json:"total_size_bytes"`
}

// Season — one seasons-accordion entry (design brief §2.8). The
// Episodes slice is always present (frontend lazy-render decisions
// are local).
type Season struct {
	SeasonNumber int        `json:"season_number"`
	Name         *string    `json:"name,omitempty"`
	Overview     *string    `json:"overview,omitempty"`
	AirDate      *time.Time `json:"air_date,omitempty"`
	PosterAsset  *string    `json:"poster_asset,omitempty"`
	Monitored    bool       `json:"monitored"`
	EpisodeCount int        `json:"episode_count"`
	OnDiskCount  int        `json:"on_disk_count"`
	MissingCount int        `json:"missing_count"`
	// DownloadingCount — story 379. Count of Sonarr queue records with
	// status=="downloading" matching this season number. 0 when nothing
	// is downloading OR Sonarr is unreachable.
	DownloadingCount int       `json:"downloading_count"`
	Episodes         []Episode `json:"episodes"`
}

// Episode — one row of a season's expanded episode list. Quality /
// SizeBytes / EpisodeFileID nil when not on disk.
type Episode struct {
	EpisodeNumber    int        `json:"episode_number"`
	SonarrEpisodeID  *int       `json:"sonarr_episode_id,omitempty"`
	Title            *string    `json:"title,omitempty"`
	TitleLanguage    string     `json:"title_language,omitempty"`
	Overview         *string    `json:"overview,omitempty"`
	OverviewLanguage string     `json:"overview_language,omitempty"`
	AirDate          *time.Time `json:"air_date,omitempty"`
	RuntimeMinutes   *int       `json:"runtime_minutes,omitempty"`
	FinaleType       *string    `json:"finale_type,omitempty"`
	StillAsset       *string    `json:"still_asset,omitempty"`
	Monitored        bool       `json:"monitored"`
	HasFile          bool       `json:"has_file"`
	Quality          *string    `json:"quality,omitempty"`
	SizeBytes        *int64     `json:"size_bytes,omitempty"`
	// Media meta — from Sonarr episodeFile.mediaInfo + releaseGroup.
	// All nil when the file was never probed (rare) or the episode is
	// not on disk.
	VideoCodec    *string `json:"video_codec,omitempty" example:"HEVC"`
	AudioCodec    *string `json:"audio_codec,omitempty" example:"DDP"`
	AudioChannels *string `json:"audio_channels,omitempty" example:"5.1"`
	ReleaseGroup  *string `json:"release_group,omitempty" example:"RARBG"`
}

// CastMember — one row of the cast carousel (design brief §2.6).
// PersonID + TMDBID enable navigation to the person page.
type CastMember struct {
	PersonID      int64          `json:"person_id"`
	TMDBPersonID  *domain.TMDBID `json:"tmdb_person_id,omitempty"`
	Name          string         `json:"name"`
	CharacterName *string        `json:"character_name,omitempty"`
	EpisodeCount  *int           `json:"episode_count,omitempty"`
	ProfileAsset  *string        `json:"profile_asset,omitempty"`
	CreditOrder   *int           `json:"credit_order,omitempty"`
}

// Recommendation — one tile of the "you might also like" carousel
// (design brief §2.9). InLibrary=true → click navigates to that
// series's detail page; false → "Add to Sonarr" overlay (inert v1).
type Recommendation struct {
	SeriesID     domain.SeriesID `json:"series_id"`
	TMDBSeriesID *domain.TMDBID  `json:"tmdb_series_id,omitempty"`
	Title        string          `json:"title"`
	Year         *int            `json:"year,omitempty"`
	PosterAsset  *string         `json:"poster_asset,omitempty"`
	TMDBRating   *float64        `json:"tmdb_rating,omitempty"`
	InLibrary    bool            `json:"in_library"`
	// InstanceName + SonarrSeriesID identify which instance the
	// recommendation lives on (when InLibrary=true). Empty
	// otherwise. Used for the in-library click-through link.
	InstanceName   domain.InstanceName   `json:"instance_name,omitempty"`
	SonarrSeriesID domain.SonarrSeriesID `json:"sonarr_series_id,omitempty"`
}

// ExternalLinks — IMDb / TMDB / TVDB / homepage footer row
// (design brief §2.9). Each link rendered only when its id exists.
type ExternalLinks struct {
	IMDbID   *domain.IMDBID `json:"imdb_id,omitempty" example:"tt0903747"`
	TMDBID   *domain.TMDBID `json:"tmdb_id,omitempty" example:"1396"`
	TVDBID   *domain.TVDBID `json:"tvdb_id,omitempty" example:"81189"`
	Homepage *string        `json:"homepage,omitempty"`
}

// SeasonDetailResponse — single-season subset returned by
// GET /api/v1/instances/:name/series/:id/season/:n. Reuses Season
// + adds the parent series id for the SPA's polling code path.
type SeasonDetailResponse struct {
	Instance       domain.InstanceName   `json:"instance"`
	SonarrSeriesID domain.SonarrSeriesID `json:"sonarr_series_id"`
	SeriesID       domain.SeriesID       `json:"series_id"`
	Lang           string                `json:"lang"`
	Season         Season                `json:"season"`
	Degraded       []string              `json:"degraded"`
	SyncedAt       time.Time             `json:"synced_at"`
}

// LibrarySeasonCount — per-season on-disk / downloading tally for ONE Sonarr
// instance. Powers the seasons-accordion row counters ("X/total on disk" + the
// downloading chip) without the FE having to expand every season to lazy-load
// episodes. Instance-specific library state — deliberately NOT part of the
// canonical /seasons (SeasonSummaryDTO) contract, which stays instance-agnostic.
// Story 970 / C3c-2.
type LibrarySeasonCount struct {
	SeasonNumber int `json:"season_number" example:"1"`
	// EpisodesOnDisk is the count of canon episodes in this season whose
	// per-instance episode_states row has has_file=true. DB-deterministic.
	EpisodesOnDisk int `json:"episodes_on_disk" example:"6"`
	// Downloading is the count of live Sonarr queue records with
	// status=="downloading" for this season. 0 when nothing is downloading OR
	// Sonarr is unreachable (best-effort; mirrors Library.in_progress).
	Downloading int `json:"downloading" example:"1"`
}

// SeriesLibraryResponse — wire shape of GET /api/v1/series/:id/library
// (Story 577 / E-1-B2). Per-instance Sonarr library state, carved out of the
// fat SeriesDetailResponse into its own endpoint (bounded-context separation,
// PLAN §7.0). Mirrors SeriesTorrentsResponse's envelope shape.
type SeriesLibraryResponse struct {
	Instance       domain.InstanceName   `json:"instance" example:"homelab"`
	SonarrSeriesID domain.SonarrSeriesID `json:"sonarr_series_id" example:"123"`
	SeriesID       domain.SeriesID       `json:"series_id" example:"42"`
	// Library is the Sonarr "what's on disk" tile (counts + dominant quality).
	Library LibraryStrip `json:"library"`
	// Recent is the last-5 grab_records activity strip, newest-first. Always a
	// present (possibly empty) slice.
	Recent []RecentEvent `json:"recent"`
	// InProgress is the best in-flight Sonarr queue download. nil when the
	// queue is empty OR Sonarr is unreachable. Also mirrored under
	// Library.in_progress for FE parity with the legacy fat response.
	InProgress *InProgress `json:"in_progress,omitempty"`
	// NextEpisodeToAir is the earliest future-dated episode (monitored
	// preferred). Title is nil by design — titles live in the canon episode
	// endpoints, not this Sonarr-state handle.
	NextEpisodeToAir *NextEpisode `json:"next_episode_to_air,omitempty"`
	// LastGrabAt is the created_at of the most recent grab_records row. nil
	// when the series has no grab history.
	LastGrabAt *time.Time `json:"last_grab_at,omitempty"`
	// LastImportedAt is the updated_at of the most recent imported grab. nil
	// when nothing has imported yet.
	LastImportedAt *time.Time `json:"last_imported_episode_at,omitempty"`
	// Monitored is the series-level Sonarr monitored flag.
	Monitored bool `json:"monitored"`
	// Seasons is the per-season on-disk / downloading breakdown for this
	// instance — the seasons-accordion row counters. Always a present (possibly
	// empty) slice, season-number ASC; empty for TMDB-only canon (which never
	// reaches this handler — 204). Instance-specific; NOT part of the canonical
	// /seasons contract. Story 970.
	Seasons []LibrarySeasonCount `json:"seasons"`
	// SyncedAt = max(series_cache.updated_at, newest episode_states.updated_at).
	SyncedAt time.Time `json:"synced_at"`
}

// SeriesSeasonsResponse — wire shape of GET /api/v1/series/:id/seasons
// (Story 582 / E-1 B3c). Canon-level list of seasons (posters + counts +
// localized names) for the SPA accordion. No episodes embed, no per-instance
// Sonarr state. Mirrors the composer's SeasonsListDTO.
type SeriesSeasonsResponse struct {
	SeriesID domain.SeriesID    `json:"series_id" example:"42"`
	Seasons  []SeasonSummaryDTO `json:"seasons"`
	// Degraded lists cold/timeout sources ("tmdb_series", "freshener"); omitted
	// when the document is fully fresh.
	Degraded []string `json:"degraded,omitempty"`
	// SyncedAt is the canon series row's updated_at.
	SyncedAt time.Time `json:"synced_at"`
}

// SeasonSummaryDTO — one accordion row. AirDateEnd is MAX(episodes.air_date) for
// the season (no source column on `seasons`). PosterAsset is a sha256 hash served
// via /api/v1/media/:hash, omitted when TMDB gave no season poster.
type SeasonSummaryDTO struct {
	SeasonNumber int        `json:"season_number" example:"1"`
	Name         string     `json:"name" example:"Сезон 1"`
	AirDateStart *time.Time `json:"air_date_start,omitempty"`
	AirDateEnd   *time.Time `json:"air_date_end,omitempty"`
	EpisodeCount int        `json:"episode_count" example:"22"`
	PosterAsset  *string    `json:"poster_asset,omitempty"`
	Overview     string     `json:"overview,omitempty"`
}
