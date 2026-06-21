package database

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type ScanRunModel struct {
	ID              string              `gorm:"primaryKey;size:36;index:idx_scan_runs_created_at_id,priority:2;index:idx_scan_runs_started_at_id,priority:2"`
	InstanceName    domain.InstanceName `gorm:"size:128;index"`
	Trigger         string              `gorm:"size:32"`
	StartedAt       time.Time           `gorm:"index:idx_scan_runs_started_at_id,priority:1"`
	FinishedAt      *time.Time
	Status          string `gorm:"size:32"`
	SeriesScanned   int
	CandidatesFound int
	GrabsPerformed  int
	GrabsFailed     int
	ErrorsCount     int
	ErrorMessage    string `gorm:"type:text"`
	DryRun          bool
	CreatedAt       time.Time `gorm:"index:idx_scan_runs_created_at_id,priority:1"`
	UpdatedAt       time.Time
}

func (ScanRunModel) TableName() string { return "scan_runs" }

type DecisionModel struct {
	ID string `gorm:"primaryKey;size:36;index:idx_decisions_created_at_id,priority:2"`
	// ScanRunID is *string so a uuid.Nil sentinel persists as SQL NULL.
	// Story 121b §B: watchdog replay decision rows have no parent
	// scan_run; persisting the all-zero UUID string as text was making
	// the UI's `d.scan_run_id && <Link>` guard render dead links.
	ScanRunID       *string               `gorm:"size:36;index"`
	InstanceName    domain.InstanceName   `gorm:"size:128;index"`
	SeriesID        domain.SonarrSeriesID `gorm:"index"`
	SeriesTitle     string                `gorm:"size:512"`
	SeasonNumber    int
	Decision        string `gorm:"size:32"`
	Reason          string `gorm:"size:128"`
	MissingCount    int
	ExistingCount   int
	ReleasesFound   int
	CandidatesCount int
	FilteredOut     datatypes.JSON
	SelectedGUID    string `gorm:"size:512"`
	SelectedData    datatypes.JSON
	DryRunWouldGrab bool `gorm:"column:would_grab"`
	// ErrorDetail mirrors domain/decision.Decision.ErrorDetail. Backed
	// by a `text` column (migration v20, story 092 / F-P2-4) so the full
	// upstream Sonarr body fits — the 4096-rune application-layer cap
	// (application/evaluate.truncateErrorDetail) holds the operator-visible
	// shape; the schema no longer constrains it.
	ErrorDetail    string  `gorm:"type:text"`
	SupersededByID *string `gorm:"size:36"`
	// 046a — partial-pack-aware season-stats snapshot. All four default
	// to 0 NOT NULL via the paired migration so pre-046a rows look like
	// "unknown" rather than null on read. TotalEpisodes / AiredEpisodes
	// / ExistingEpisodes come straight from Sonarr's per-season
	// statistics block at scan time; GrabbedEpisodes is computed once at
	// decision-persist time (single COUNT against grab_records
	// status=imported) so the value stays pinned to the scan that
	// produced the decision and historic decisions don't shift under
	// future grabs.
	TotalEpisodes    int `gorm:"not null;default:0;column:total_episodes"`
	AiredEpisodes    int `gorm:"not null;default:0;column:aired_episodes"`
	ExistingEpisodes int `gorm:"not null;default:0;column:existing_episodes"`
	GrabbedEpisodes  int `gorm:"not null;default:0;column:grabbed_episodes"`
	// Intent carries the F-P2-2 "why this grab" payload (091a). The
	// column is `jsonb` on Postgres and `text` on SQLite (see
	// migration 000021). Both backends accept a JSON document and
	// the GORM datatypes.JSON column handles the read/write transcode.
	// Nullable on purpose — pre-091a rows have no intent.
	Intent    datatypes.JSON `gorm:"column:intent"`
	CreatedAt time.Time      `gorm:"index:idx_decisions_created_at_id,priority:1"`
}

func (DecisionModel) TableName() string { return "decisions" }

type GrabRecordModel struct {
	ID                string                `gorm:"primaryKey;size:36;index:idx_grab_records_created_at_id,priority:2"`
	InstanceName      domain.InstanceName   `gorm:"size:128;index:idx_grab_inst_series,priority:1;index:idx_grab_dedupe_lookup,priority:1"`
	SeriesID          domain.SonarrSeriesID `gorm:"index:idx_grab_inst_series,priority:2;index:idx_grab_dedupe_lookup,priority:2"`
	SeriesTitle       string                `gorm:"size:512"`
	SeasonNumber      int                   `gorm:"index:idx_grab_inst_series,priority:3;index:idx_grab_dedupe_lookup,priority:3"`
	ReleaseGUID       string                `gorm:"size:512;index;index:idx_grab_dedupe_lookup,priority:4"`
	ReleaseTitle      string                `gorm:"size:1024"`
	DownloadID        string                `gorm:"size:128;index;column:download_id"`
	IndexerID         int
	IndexerName       string `gorm:"size:256"`
	CustomFormatScore int
	Quality           string `gorm:"size:128"`
	CoverageCount     int
	Status            string `gorm:"size:32;index"`
	ErrorMessage      string `gorm:"type:text"`
	ScanRunID         string `gorm:"size:36;index"`
	Attempts          int
	// TorrentHash is the qBit infohash (40-char lowercase hex) populated
	// by the OnGrab webhook handler in 039c. Nullable on purpose: rows
	// created before Phase 10 have no recorded hash and are intentionally
	// ignored by the Watchdog (D63 hash-required gate, no backfill).
	TorrentHash *domain.QbitHash `gorm:"column:torrent_hash;size:64"`
	// ReplayOfID is the uuid of the original grab_records row this row
	// re-grabs. Populated by the Phase 10 Watchdog when a re-grab is
	// triggered (039f-2). nil for scan / rescan / manual paths. Indexed
	// in the migration (partial index) so the future UI can fetch
	// "replays of original_id" cheaply.
	ReplayOfID *string `gorm:"column:replay_of_id;size:36"`
	// SizeBytes is Sonarr's release.size persisted on insert (043b,
	// Phase 12). Nullable: pre-Phase-12 rows and Sonarr payloads that
	// omit the size keep NULL. *int64 round-trips cleanly with the
	// BIGINT (Postgres) / INTEGER (SQLite) column.
	SizeBytes *int64 `gorm:"column:size_bytes"`
	// Parsed* fields hold the B2 Sonarr /api/v3/parse projection.
	// Nullable on purpose — pre-B2 rows stay NULL; the JSON repo
	// emits `parsed: null` on the API for those rows. Array columns
	// use gorm:"serializer:json" so SQLite and Postgres carry the same
	// TEXT shape (see migration 000016 header for the trade-off).
	ParsedCodec        *string    `gorm:"column:parsed_codec;type:text"`
	ParsedSource       *string    `gorm:"column:parsed_source;type:text"`
	ParsedQuality      *string    `gorm:"column:parsed_quality;type:text"`
	ParsedResolution   *int       `gorm:"column:parsed_resolution"`
	ParsedHDRFlags     []string   `gorm:"column:parsed_hdr_flags;serializer:json"`
	ParsedDub          *string    `gorm:"column:parsed_dub;type:text"`
	ParsedLanguages    []string   `gorm:"column:parsed_languages;serializer:json"`
	ParsedSubs         []string   `gorm:"column:parsed_subs;serializer:json"`
	ParsedReleaseGroup *string    `gorm:"column:parsed_release_group;type:text"`
	ParsedAt           *time.Time `gorm:"column:parsed_at"`
	CreatedAt          time.Time  `gorm:"index:idx_grab_records_created_at_id,priority:1"`
	UpdatedAt          time.Time
}

func (GrabRecordModel) TableName() string { return "grab_records" }

type OriginReleaseModel struct {
	InstanceName domain.InstanceName   `gorm:"primaryKey;size:128"`
	SeriesID     domain.SonarrSeriesID `gorm:"primaryKey"`
	SeasonNumber int                   `gorm:"primaryKey"`
	GUID         string                `gorm:"size:512"`
	IndexerID    int
	IndexerName  string `gorm:"size:256"`
	Source       string `gorm:"size:32"`
	FirstSeenAt  time.Time
	LastSeenAt   time.Time
	LastUsedAt   *time.Time
}

func (OriginReleaseModel) TableName() string { return "origin_releases" }

type CooldownModel struct {
	Scope     string    `gorm:"primaryKey;size:16"`
	Key       string    `gorm:"primaryKey;size:512"`
	ExpiresAt time.Time `gorm:"index"`
	Reason    string    `gorm:"type:text"`
	CreatedAt time.Time
}

func (CooldownModel) TableName() string { return "cooldowns" }

// UserModel — the greenfield D-5 rename of AdminUserModel. The `users`
// table (000011_auth.up.sql) folds the legacy user_settings per-row
// entries (preferred_language, avatar_mode) directly here because the
// 1:1 cardinality made the indirection wasteful. CHECK constraints on
// role + avatar_mode are enforced at the DB. Multi-row capable for the
// future N-1 multi-user UI; D-5 keeps the single-user invariant via the
// repository Get ORDER BY id ASC LIMIT 1.
type UserModel struct {
	ID                uint       `gorm:"primaryKey;column:id"`
	Username          string     `gorm:"column:username;type:text;not null;uniqueIndex:users_username_uniq"`
	Email             *string    `gorm:"column:email;type:text"`
	PasswordHash      *string    `gorm:"column:password_hash;type:text"`
	OIDCSubject       *string    `gorm:"column:oidc_subject;type:text;uniqueIndex:users_oidc_subject_uniq,where:oidc_subject IS NOT NULL"`
	Role              string     `gorm:"column:role;type:text;not null;default:'admin'"`
	AvatarMode        string     `gorm:"column:avatar_mode;type:text;not null;default:'auto'"`
	PreferredLanguage *string    `gorm:"column:preferred_language;type:text"`
	CreatedAt         time.Time  `gorm:"column:created_at"`
	UpdatedAt         time.Time  `gorm:"column:updated_at"`
	LastLoginAt       *time.Time `gorm:"column:last_login_at"`
}

func (UserModel) TableName() string { return "users" }

// UserInstanceTagModel — sf-<user> tag cache per (user, instance). Used
// by the discovery TagResolver (N-4); D-5 ships the repo with no
// production callers yet, so the schema is exercised by tests only and
// N-4 wires the consumer.
//
// PK is the composite (user_id, instance_name); the (instance_name,
// sonarr_tag_label) UNIQUE index prevents two users from claiming the
// same Sonarr label on one instance.
type UserInstanceTagModel struct {
	UserID         uint                `gorm:"primaryKey;column:user_id"`
	InstanceName   domain.InstanceName `gorm:"primaryKey;column:instance_name;type:text"`
	SonarrTagID    int                 `gorm:"column:sonarr_tag_id;not null"`
	SonarrTagLabel string              `gorm:"column:sonarr_tag_label;type:text;not null;uniqueIndex:user_instance_tags_label,composite:instance_name"`
	CreatedAt      time.Time           `gorm:"column:created_at"`
	UpdatedAt      time.Time           `gorm:"column:updated_at"`
}

func (UserInstanceTagModel) TableName() string { return "user_instance_tags" }

// RuntimeConfigModel — singleton row (id=1) holding all DB-stored
// runtime config except per-instance details.
type RuntimeConfigModel struct {
	ID                         uint `gorm:"primaryKey"`
	CronEnabled                bool
	CronSchedule               string `gorm:"size:64"`
	CronOnStart                bool
	CronJitterSeconds          int
	ScanShutdownGraceSec       int
	ScanCooldownSweepSec       int
	DryRun                     bool
	GlobalRPM                  int
	GlobalBurst                int
	AuthSessionTTLSec          int
	AuthSecureCookie           bool
	AuthTrustedProxies         string `gorm:"type:text"`
	AuthMode                   string `gorm:"size:16;default:forms"`
	AuthLocalBypass            bool
	AuthLocalNetworks          string `gorm:"type:text"`
	AuthSessionEpoch           int64
	OIDCIssuer                 string `gorm:"column:oidc_issuer;type:text"`
	OIDCClientID               string `gorm:"column:oidc_client_id;type:text"`
	OIDCRedirectURL            string `gorm:"column:oidc_redirect_url;type:text"`
	OIDCScopes                 string `gorm:"column:oidc_scopes;type:text"`
	OIDCUsernameClaim          string `gorm:"column:oidc_username_claim;type:text"`
	OIDCAllowedGroups          string `gorm:"column:oidc_allowed_groups;type:text"`
	OIDCGroupsClaim            string `gorm:"column:oidc_groups_claim;type:text;default:groups"`
	OIDCClientSecretCiphertext []byte `gorm:"column:oidc_client_secret_ciphertext"`
	// GUIDRewrites stores the operator-curated tracker GUID substitution
	// table as a JSON array of {from,to} objects. Default '[]' means a
	// fresh row starts with no rewrites. The repo marshals/unmarshals;
	// the column is plain text on both backends (same pattern as
	// auth_local_networks). Story 107.
	GUIDRewrites        string `gorm:"column:guid_rewrites;type:text;not null;default:'[]'"`
	APIKeyCiphertext    []byte
	APIKeyAutoGenerated bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (RuntimeConfigModel) TableName() string { return "runtime_config" }

// AppSettingsModel — singleton (id=1) row holding cross-cutting
// app-level settings. Today: only timezone. Future: locale,
// theme. Kept separate from RuntimeConfigModel so the watchdog +
// reload-bus surface there stays untouched.
type AppSettingsModel struct {
	ID        uint    `gorm:"primaryKey;default:1"`
	Timezone  *string `gorm:"size:64"` // NULL = use env / UTC fallback
	UpdatedAt time.Time
}

func (AppSettingsModel) TableName() string { return "app_settings" }

// QuotaStateModel — generic external-service rate-limit counter state.
// One row per (service_name, window_start) pair; rows are upserted on
// every Increment via clause.OnConflict. GC sweep deletes rows where
// window_start < (now - retention). See internal/runtime/quota for the
// window-derivation helpers and the port contract.
type QuotaStateModel struct {
	ServiceName string    `gorm:"primaryKey;size:64;column:service_name"`
	WindowStart time.Time `gorm:"primaryKey;column:window_start"`
	Count       int       `gorm:"not null;default:0;column:count"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (QuotaStateModel) TableName() string { return "external_service_quota_state" }

// SonarrInstanceModel — one row per Sonarr instance. Secret api_key
// is held in instance_secret to keep this row free of PII.
type SonarrInstanceModel struct {
	ID                            uint   `gorm:"primaryKey"`
	Name                          string `gorm:"size:128;uniqueIndex"`
	URL                           string `gorm:"size:512"`
	Mode                          string `gorm:"size:16"`
	TimeoutSeconds                int
	SearchTimeoutSeconds          int
	DryRun                        *bool
	TagsMode                      string `gorm:"size:16"`
	TagsInclude                   string `gorm:"type:text"`
	TagsExclude                   string `gorm:"type:text"`
	SearchRequireAllAired         bool
	SearchSkipSpecials            bool
	SearchSkipAnime               bool
	SearchMinCustomFormatScore    int
	RankingIndexerPriorityEnabled bool
	RankingOriginBonus            float64
	LimitsScanMaxSeries           int
	LimitsMaxGrabsPerScan         int
	RateLimitRPM                  int
	RateLimitBurst                int
	CooldownMode                  string `gorm:"size:16"`
	CooldownSeriesAfterGrabSec    int
	CooldownGUIDFailedGrabSec     int
	CooldownGUIDFailedImportSec   int
	RetryMaxAttempts              int
	RetryInitialBackoffSec        int
	RetryMaxBackoffSec            int
	HealthCheckRecheckAuthSec     int
	HealthCheckRecheckNetSec      int
	// PublicURL is the browser-facing URL (D64). NULL = fall back to URL.
	PublicURL *string `gorm:"column:public_url;type:text"`
	// WebhookInstallEnabled toggles the auto-install reconciler (D65).
	// Defaults to TRUE so the existing homelab row backfills correctly.
	WebhookInstallEnabled bool `gorm:"column:webhook_install_enabled;not null;default:true"`
	// WebhookURLOverride is the optional base URL for the webhook (D65).
	// NULL = use the derived public URL from runtime config.
	WebhookURLOverride *string `gorm:"column:webhook_url_override;type:text"`
	// ParseOnGrabEnabled toggles the 044b parse-on-OnGrab hook. Defaults
	// to TRUE on every existing row (migration default) so the homelab's
	// pre-B2 row keeps the new behaviour. Set FALSE per instance to
	// disable parse calls on a flaky Sonarr.
	ParseOnGrabEnabled bool `gorm:"column:parse_on_grab_enabled;not null;default:true"`
	// ScanSkipHandledSeasons toggles the 046b scan pre-filter. Defaults
	// to TRUE on every existing row (migration 000017 default). Turn
	// FALSE when an operator wants the full evaluator path to run for
	// every monitored season (regression guard / debugging).
	ScanSkipHandledSeasons bool `gorm:"column:scan_skip_handled_seasons;not null;default:true"`
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (SonarrInstanceModel) TableName() string { return "sonarr_instance" }

// InstanceSecretModel — encrypted secret(s) per instance.
type InstanceSecretModel struct {
	InstanceID uint   `gorm:"primaryKey"`
	SecretName string `gorm:"primaryKey;size:64"`
	Ciphertext []byte `gorm:"not null"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (InstanceSecretModel) TableName() string { return "instance_secret" }

func NewScanID() string { return uuid.New().String() }

// InstanceQbitSettingsModel is the per-instance Watchdog configuration row.
// One row per Sonarr instance enforced by the unique index on instance_id.
// PasswordEncrypted is opaque AES-GCM ciphertext (nonce|ciphertext|tag)
// produced by the 039d HTTP handler with the `-aes-gcm-v1` HKDF subkey;
// the repo treats it as bytes.
type InstanceQbitSettingsModel struct {
	ID                     uint   `gorm:"primaryKey"`
	InstanceID             uint   `gorm:"uniqueIndex:idx_instance_qbit_settings_instance_id"`
	Enabled                bool   `gorm:"not null;default:false"`
	URL                    string `gorm:"type:text;not null"`
	Username               *string
	PasswordEncrypted      []byte         `gorm:"column:password_encrypted"`
	Category               string         `gorm:"type:text;not null;default:'sonarr'"`
	PollIntervalMinutes    int            `gorm:"not null;default:30"`
	RegrabCooldownHours    int            `gorm:"not null;default:120"`
	MaxConsecutiveNoBetter int            `gorm:"not null;default:3"`
	CustomUnregisteredMsgs datatypes.JSON `gorm:"not null"`
	// PublicURL is the optional browser-reachable qBittorrent web UI URL
	// (082, F-P2-1). NULL = SPA falls back to URL. Backend never consumes
	// this field — it is a passthrough for the frontend GrabDrawer deep
	// link.
	PublicURL *string `gorm:"column:qbit_public_url;type:text"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (InstanceQbitSettingsModel) TableName() string { return "instance_qbit_settings" }

// WatchdogBlacklistModel parks (instance, series, season) triples that
// exhausted the consecutive-no-better budget or whose parent qBit is
// persistently unreachable. ExpiresAt is *time.Time because v1 always
// writes NULL (manual unblock only per parent 039 §Out of scope).
type WatchdogBlacklistModel struct {
	ID           uint                  `gorm:"primaryKey"`
	InstanceID   uint                  `gorm:"uniqueIndex:idx_watchdog_blacklist_triple,priority:1;index:idx_watchdog_blacklist_instance_id"`
	SeriesID     domain.SonarrSeriesID `gorm:"uniqueIndex:idx_watchdog_blacklist_triple,priority:2"`
	SeasonNumber int                   `gorm:"uniqueIndex:idx_watchdog_blacklist_triple,priority:3"`
	Reason       string                `gorm:"type:text;not null"`
	Consecutive  int                   `gorm:"not null"`
	CreatedAt    time.Time
	ExpiresAt    *time.Time
}

func (WatchdogBlacklistModel) TableName() string { return "watchdog_blacklist" }

// SeriesCacheModel — thin per-instance Sonarr projection after the
// 000032 cutover (PRD v4 §5.11). All canon columns (title / year /
// tvdb_id / imdb_id / tmdb_id / status / network / genres /
// runtime_minutes / overview / last_aired_at / poster_path /
// fanart_path / banner_path) moved to `series` and are JOIN-read via
// SeriesID. SeriesID is non-nullable post-cutover because every
// active row has a canon row; the *int64 type is preserved (vs.
// switching to int64) so legacy GORM serialisers that emit NULL on
// the wire don't break in mid-deploy windows.
//
// Soft-deleted via DeletedAt so grab_records that reference removed
// series stay readable. No DB-level FK on instance_name (consistent
// with the rest of the schema).
type SeriesCacheModel struct {
	InstanceName      domain.InstanceName   `gorm:"primaryKey;size:128;column:instance_name"`
	SonarrSeriesID    domain.SonarrSeriesID `gorm:"primaryKey;column:sonarr_series_id"`
	SeriesID          *domain.SeriesID      `gorm:"column:series_id;index:series_cache_series_id;not null"`
	TitleSlug         string                `gorm:"type:text;not null;column:title_slug"`
	Monitored         bool                  `gorm:"column:monitored;not null;default:false"`
	MissingCount      int                   `gorm:"column:missing_count;not null;default:0"`
	EpisodeFileCount  int                   `gorm:"column:episode_file_count;not null;default:0"`
	SizeOnDiskBytes   int64                 `gorm:"column:size_on_disk_bytes;not null;default:0"`
	AiredEpisodeCount int                   `gorm:"column:aired_episode_count;not null;default:0"`
	UpdatedAt         time.Time             `gorm:"column:updated_at;not null"`
	DeletedAt         *time.Time            `gorm:"column:deleted_at"`
}

func (SeriesCacheModel) TableName() string { return "series_cache" }

// ExternalServiceSettingsModel is the runtime config row for one
// enrichment service (tmdb|omdb|tvdb). All *_enc columns are AES-GCM
// ciphertext produced by internal/runtime/crypto.Cipher.Seal; the
// repo treats them as opaque bytes. api_key_last4 is the last 4
// characters of plaintext, captured before encryption for the masked
// UI display so list-calls don't need to decrypt.
type ExternalServiceSettingsModel struct {
	Service          string  `gorm:"primaryKey;type:text"`
	Enabled          bool    `gorm:"not null;default:false"`
	APIKeyEnc        []byte  `gorm:"column:api_key_enc"`
	APIKeyLast4      *string `gorm:"column:api_key_last4;type:text"`
	ProxyURLEnc      []byte  `gorm:"column:proxy_url_enc"`
	ProxyUsernameEnc []byte  `gorm:"column:proxy_username_enc"`
	ProxyPasswordEnc []byte  `gorm:"column:proxy_password_enc"`
	LastTestAt       *time.Time
	LastTestOutcome  *string `gorm:"type:text"`
	LastTestMessage  *string `gorm:"type:text"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (ExternalServiceSettingsModel) TableName() string { return "external_service_settings" }

// SeriesModel — canonical local entity (PRD §5, migration 000026).
// One row per real series; tmdb_id has a partial unique index where
// not NULL so Sonarr orphans without a TMDB match still fit. Hydration
// is text(stub|full); defaults to 'stub' on insert.
type SeriesModel struct {
	ID               domain.SeriesID `gorm:"primaryKey;autoIncrement;column:id"`
	TMDBID           *domain.TMDBID  `gorm:"column:tmdb_id"`
	TVDBID           *domain.TVDBID  `gorm:"column:tvdb_id;index:series_tvdb_id"`
	IMDBID           *domain.IMDBID  `gorm:"column:imdb_id;type:text;index:series_imdb_id"`
	Hydration        string          `gorm:"column:hydration;type:text;not null;default:'stub'"`
	Title            string          `gorm:"column:title;type:text;not null"`
	OriginalTitle    *string         `gorm:"column:original_title;type:text"`
	Status           *string         `gorm:"column:status;type:text"`
	FirstAirDate     *time.Time      `gorm:"column:first_air_date"`
	LastAirDate      *time.Time      `gorm:"column:last_air_date"`
	NextAirDate      *time.Time      `gorm:"column:next_air_date"`
	Year             *int            `gorm:"column:year"`
	RuntimeMinutes   *int            `gorm:"column:runtime_minutes"`
	Homepage         *string         `gorm:"column:homepage;type:text"`
	OriginalLanguage *string         `gorm:"column:original_language;type:text"`
	OriginCountry    *string         `gorm:"column:origin_country;type:text"`
	// OriginCountries is a JSON-encoded array of ISO 3166-1 alpha-2 codes
	// (e.g. `["US","CA"]`). Migration 000041 introduced it; OriginCountry
	// is kept in sync as the first element for compat. NULL on rows older
	// than 000041 OR Sonarr-only cold rows that never went through TMDB.
	OriginCountries datatypes.JSON `gorm:"column:origin_countries;type:text"`
	Popularity      *float64       `gorm:"column:popularity"`
	InProduction    bool           `gorm:"column:in_production;not null;default:false"`
	// Network field REMOVED in E-1 (000033). Network membership lives
	// in series_networks join, resolved via NetworksRepository.
	PosterAsset   *string  `gorm:"column:poster_asset;type:text"`
	BackdropAsset *string  `gorm:"column:backdrop_asset;type:text"`
	TMDBRating    *float64 `gorm:"column:tmdb_rating"`
	TMDBVotes     *int     `gorm:"column:tmdb_votes"`
	IMDBRating    *float64 `gorm:"column:imdb_rating"`
	IMDBVotes     *int     `gorm:"column:imdb_votes"`
	OMDBRated     *string  `gorm:"column:omdb_rated;type:text"`
	OMDBAwards    *string  `gorm:"column:omdb_awards;type:text"`
	// EnrichmentTMDBSyncedAt / EnrichmentOMDBSyncedAt — D-3 enrichment
	// freshness columns (migration 000001 §D-3). NULL = never
	// enriched. Set by workers on success; canonical replacement for
	// the legacy per-source hydration journal — workers stamp the
	// column directly on success, no separate row write.
	EnrichmentTMDBSyncedAt *time.Time `gorm:"column:enrichment_tmdb_synced_at"`
	EnrichmentOMDBSyncedAt *time.Time `gorm:"column:enrichment_omdb_synced_at"`
	CreatedAt              time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt              time.Time  `gorm:"column:updated_at;not null"`
}

func (SeriesModel) TableName() string { return "series" }

// SeriesTextModel — one localised text row per (series_id, language).
// The §5.6 fallback helper reads against this table. EnrichedAt is
// the D-3 TMDB-worker freshness stamp (column added in D-1 migration
// 000002); NULL until the worker runs.
type SeriesTextModel struct {
	SeriesID   domain.SeriesID `gorm:"primaryKey;column:series_id"`
	Language   string          `gorm:"primaryKey;column:language;type:text"`
	Title      *string         `gorm:"column:title;type:text"`
	Overview   *string         `gorm:"column:overview;type:text"`
	Tagline    *string         `gorm:"column:tagline;type:text"`
	EnrichedAt *time.Time      `gorm:"column:enriched_at"`
	UpdatedAt  time.Time       `gorm:"column:updated_at;not null"`
}

func (SeriesTextModel) TableName() string { return "series_texts" }

// SeasonModel — one row per (series_id, season_number).
type SeasonModel struct {
	ID           int64           `gorm:"primaryKey;autoIncrement;column:id"`
	SeriesID     domain.SeriesID `gorm:"column:series_id;not null"`
	SeasonNumber int             `gorm:"column:season_number;not null"`
	TMDBSeasonID *int            `gorm:"column:tmdb_season_id"`
	Name         *string         `gorm:"column:name;type:text"`
	Overview     *string         `gorm:"column:overview;type:text"`
	AirDate      *time.Time      `gorm:"column:air_date"`
	EpisodeCount *int            `gorm:"column:episode_count"`
	PosterAsset  *string         `gorm:"column:poster_asset;type:text"`
	CreatedAt    time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time       `gorm:"column:updated_at;not null"`
}

func (SeasonModel) TableName() string { return "seasons" }

// EpisodeModel — canonical episode row, unique on
// (series_id, season_number, episode_number).
type EpisodeModel struct {
	ID                int64           `gorm:"primaryKey;autoIncrement;column:id"`
	SeriesID          domain.SeriesID `gorm:"column:series_id;not null"`
	SeasonID          *int64          `gorm:"column:season_id"`
	SeasonNumber      int             `gorm:"column:season_number;not null"`
	EpisodeNumber     int             `gorm:"column:episode_number;not null"`
	TMDBEpisodeNumber *int            `gorm:"column:tmdb_episode_number"`
	TMDBEpisodeID     *int            `gorm:"column:tmdb_episode_id"`
	SonarrEpisodeID   *int            `gorm:"column:sonarr_episode_id"`
	AbsoluteNumber    *int            `gorm:"column:absolute_number"`
	AirDate           *time.Time      `gorm:"column:air_date"`
	RuntimeMinutes    *int            `gorm:"column:runtime_minutes"`
	FinaleType        *string         `gorm:"column:finale_type;type:text"`
	StillAsset        *string         `gorm:"column:still_asset;type:text"`
	TMDBRating        *float64        `gorm:"column:tmdb_rating"`
	TMDBVotes         *int            `gorm:"column:tmdb_votes"`
	CreatedAt         time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt         time.Time       `gorm:"column:updated_at;not null"`
}

func (EpisodeModel) TableName() string { return "episodes" }

// EpisodeTextModel — one localised text row per (episode_id, language).
// EnrichedAt mirrors SeriesTextModel — TMDB-worker freshness stamp.
type EpisodeTextModel struct {
	EpisodeID  domain.EpisodeID `gorm:"primaryKey;column:episode_id"`
	Language   string           `gorm:"primaryKey;column:language;type:text"`
	Title      *string          `gorm:"column:title;type:text"`
	Overview   *string          `gorm:"column:overview;type:text"`
	EnrichedAt *time.Time       `gorm:"column:enriched_at"`
	UpdatedAt  time.Time        `gorm:"column:updated_at;not null"`
}

func (EpisodeTextModel) TableName() string { return "episode_texts" }

// EpisodeStateModel — per-instance file state. PK
// (instance_name, episode_id) — file state is instance-scoped (§5.11).
type EpisodeStateModel struct {
	InstanceName  domain.InstanceName `gorm:"primaryKey;column:instance_name;type:text"`
	EpisodeID     domain.EpisodeID    `gorm:"primaryKey;column:episode_id"`
	Monitored     bool                `gorm:"column:monitored;not null;default:false"`
	HasFile       bool                `gorm:"column:has_file;not null;default:false"`
	EpisodeFileID *int                `gorm:"column:episode_file_id"`
	Quality       *string             `gorm:"column:quality;type:text"`
	SizeBytes     *int64              `gorm:"column:size_bytes"`
	// VideoCodec, AudioCodec, AudioChannels, ReleaseGroup come from
	// Sonarr's episodeFile.mediaInfo block + releaseGroup. All
	// nullable — mediaInfo is absent when Sonarr never probed the file.
	VideoCodec    *string   `gorm:"column:video_codec;type:text"`
	AudioCodec    *string   `gorm:"column:audio_codec;type:text"`
	AudioChannels *string   `gorm:"column:audio_channels;type:text"`
	ReleaseGroup  *string   `gorm:"column:release_group;type:text"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null"`
	// DeletedAt is set by the SeriesDelete webhook cascade
	// (story 218 E-2). Production readers filter by IS NULL.
	DeletedAt *time.Time `gorm:"column:deleted_at"`
}

func (EpisodeStateModel) TableName() string { return "episode_states" }

// SeasonStatModel — per-(instance, series, season) Sonarr statistics
// projection. PK (instance_name, sonarr_series_id, season_number).
// Story 377. Soft-deleted via DeletedAt; the SeriesDelete cascade
// (scan.CascadeSeriesDelete) stamps it alongside series_cache +
// episode_states.
type SeasonStatModel struct {
	InstanceName      domain.InstanceName   `gorm:"primaryKey;column:instance_name;type:text;size:128"`
	SonarrSeriesID    domain.SonarrSeriesID `gorm:"primaryKey;column:sonarr_series_id"`
	SeasonNumber      int                   `gorm:"primaryKey;column:season_number"`
	EpisodeCount      int                   `gorm:"column:episode_count;not null;default:0"`
	EpisodeFileCount  int                   `gorm:"column:episode_file_count;not null;default:0"`
	TotalEpisodeCount int                   `gorm:"column:total_episode_count;not null;default:0"`
	AiredEpisodeCount int                   `gorm:"column:aired_episode_count;not null;default:0"`
	Monitored         bool                  `gorm:"column:monitored;not null;default:false"`
	SizeOnDiskBytes   int64                 `gorm:"column:size_on_disk_bytes;not null;default:0"`
	UpdatedAt         time.Time             `gorm:"column:updated_at;not null"`
	DeletedAt         *time.Time            `gorm:"column:deleted_at"`
}

func (SeasonStatModel) TableName() string { return "season_stats" }

// PeopleModel — canonical local person entity (PRD §5.3, migration
// 000027). One row per real person; tmdb_id has a partial unique
// index where not NULL so non-TMDB stubs (rare — should only happen
// if a future TVDB-sourced credit lands without a TMDB id) still
// fit. Hydration is text(stub|full); defaults to 'stub' on insert.
//
// Name + OriginalName intentionally stay on this entity (no
// people_names i18n table) — TMDB does not localise person names
// reliably. This is the only canon i18n exception in the schema —
// see PRD §5.3 row "people" + §5.4 row "people.*". A future
// contributor MUST NOT add a people_names table without first
// re-evaluating that decision; the value would be three write paths
// feeding columns that are 99% identical.
type PeopleModel struct {
	ID                 int64          `gorm:"primaryKey;autoIncrement;column:id"`
	TMDBID             *domain.TMDBID `gorm:"column:tmdb_id"`
	IMDBID             *string        `gorm:"column:imdb_id;type:text;index:people_imdb_id"`
	Hydration          string         `gorm:"column:hydration;type:text;not null;default:'stub'"`
	Name               string         `gorm:"column:name;type:text;not null"`
	OriginalName       *string        `gorm:"column:original_name;type:text"`
	Gender             *int           `gorm:"column:gender"`
	Birthday           *time.Time     `gorm:"column:birthday"`
	Deathday           *time.Time     `gorm:"column:deathday"`
	PlaceOfBirth       *string        `gorm:"column:place_of_birth;type:text"`
	KnownForDepartment *string        `gorm:"column:known_for_department;type:text"`
	Popularity         *float64       `gorm:"column:popularity"`
	ProfileAsset       *string        `gorm:"column:profile_asset;type:text"`
	// EnrichmentSyncedAt — D-3 (migration 000014) per-person TMDB
	// enrichment freshness column. NULL = never enriched. Set by
	// PersonWorker on success; replaces the legacy sync_log(tmdb_person,
	// outcome='ok') row TTL gate.
	EnrichmentSyncedAt *time.Time `gorm:"column:enrichment_synced_at"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;not null"`
}

func (PeopleModel) TableName() string { return "people" }

// PersonBiographyModel — one localised biography row per
// (person_id, language). Read via the shared
// repositories.pickLanguageFallback helper introduced in story 203;
// no per-table fallback code lives in PersonBiographiesRepository.
type PersonBiographyModel struct {
	PersonID  int64     `gorm:"primaryKey;column:person_id"`
	Language  string    `gorm:"primaryKey;column:language;type:text"`
	Biography *string   `gorm:"column:biography;type:text"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (PersonBiographyModel) TableName() string { return "person_biographies" }

// SeriesPersonModel — one series-level credit row (PRD §5.3
// "series_people"). Natural key (series_id, tmdb_credit_id) makes
// re-ingest of TMDB aggregate_credits idempotent.
type SeriesPersonModel struct {
	ID            int64           `gorm:"primaryKey;autoIncrement;column:id"`
	SeriesID      domain.SeriesID `gorm:"column:series_id;not null"`
	PersonID      int64           `gorm:"column:person_id;not null"`
	Kind          string          `gorm:"column:kind;type:text;not null"`
	TMDBCreditID  string          `gorm:"column:tmdb_credit_id;type:text;not null"`
	CharacterName *string         `gorm:"column:character_name;type:text"`
	Department    *string         `gorm:"column:department;type:text"`
	Job           *string         `gorm:"column:job;type:text"`
	CreditOrder   *int            `gorm:"column:credit_order"`
	EpisodeCount  *int            `gorm:"column:episode_count"`
	CreatedAt     time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time       `gorm:"column:updated_at;not null"`
}

func (SeriesPersonModel) TableName() string { return "series_people" }

// EpisodePersonModel — one per-episode credit row (PRD §5.3
// "episode_people"). Natural key (episode_id, tmdb_credit_id).
type EpisodePersonModel struct {
	ID            int64            `gorm:"primaryKey;autoIncrement;column:id"`
	EpisodeID     domain.EpisodeID `gorm:"column:episode_id;not null"`
	PersonID      int64            `gorm:"column:person_id;not null"`
	Kind          string           `gorm:"column:kind;type:text;not null"`
	TMDBCreditID  string           `gorm:"column:tmdb_credit_id;type:text;not null"`
	CharacterName *string          `gorm:"column:character_name;type:text"`
	Department    *string          `gorm:"column:department;type:text"`
	Job           *string          `gorm:"column:job;type:text"`
	CreditOrder   *int             `gorm:"column:credit_order"`
	CreatedAt     time.Time        `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time        `gorm:"column:updated_at;not null"`
}

func (EpisodePersonModel) TableName() string { return "episode_people" }

// NetworkModel — canonical network dictionary row (PRD §5.3,
// migration 000028). name stays on the entity — brand names are not
// meaningfully translated. tmdb_id has a partial unique index where
// not NULL so a Sonarr-string fallback (PRD §5.4 row
// "series_networks") can create a row without a TMDB id.
type NetworkModel struct {
	ID            int64          `gorm:"primaryKey;autoIncrement;column:id"`
	TMDBID        *domain.TMDBID `gorm:"column:tmdb_id"`
	Name          string         `gorm:"column:name;type:text;not null"`
	LogoAsset     *string        `gorm:"column:logo_asset;type:text"`
	OriginCountry *string        `gorm:"column:origin_country;type:text"`
	CreatedAt     time.Time      `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time      `gorm:"column:updated_at;not null"`
}

func (NetworkModel) TableName() string { return "networks" }

// ProductionCompanyModel — canonical production company dictionary
// row (PRD §5.3, migration 000028). Same shape as NetworkModel.
type ProductionCompanyModel struct {
	ID            int64          `gorm:"primaryKey;autoIncrement;column:id"`
	TMDBID        *domain.TMDBID `gorm:"column:tmdb_id"`
	Name          string         `gorm:"column:name;type:text;not null"`
	LogoAsset     *string        `gorm:"column:logo_asset;type:text"`
	OriginCountry *string        `gorm:"column:origin_country;type:text"`
	CreatedAt     time.Time      `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time      `gorm:"column:updated_at;not null"`
}

func (ProductionCompanyModel) TableName() string { return "production_companies" }

// GenreModel — canonical genre dictionary row (PRD §5.3, migration
// 000028). The name lives in GenreI18nModel (one row per language) —
// the entity carries only the natural-key id + audit columns. tmdb_id
// has a partial unique index where not NULL so the Sonarr-string
// fallback (PRD §5.4 row "series_genres") can hypothetically create
// rows without a TMDB id (in practice every TMDB TV genre has an id;
// the partial unique mirrors networks for shape uniformity).
type GenreModel struct {
	ID        int64          `gorm:"primaryKey;autoIncrement;column:id"`
	TMDBID    *domain.TMDBID `gorm:"column:tmdb_id"`
	CreatedAt time.Time      `gorm:"column:created_at;not null"`
	UpdatedAt time.Time      `gorm:"column:updated_at;not null"`
}

func (GenreModel) TableName() string { return "genres" }

// GenreI18nModel — one localised name row per (genre_id, language).
// Read via the shared repositories.pickLanguageFallback helper from
// story 203. The (language, name) index on this table is what makes
// the PRD §5.4 Sonarr-genre fallback efficient — resolve a "Drama"
// string to a canonical genres.id by querying
// WHERE language='en-US' AND name='Drama'.
type GenreI18nModel struct {
	GenreID   int64     `gorm:"primaryKey;column:genre_id"`
	Language  string    `gorm:"primaryKey;column:language;type:text"`
	Name      string    `gorm:"column:name;type:text;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (GenreI18nModel) TableName() string { return "genres_i18n" }

// KeywordModel — canonical keyword dictionary row (PRD §5.3, migration
// 000028). v1 keywords are en-only (TMDB does not localise the
// /tv/{id}/keywords payload). The same partial-unique-on-tmdb_id
// + sibling i18n shape is preserved for forward-compat — a future
// RU / de keyword source adds rows to keywords_i18n with no
// migration.
type KeywordModel struct {
	ID        int64          `gorm:"primaryKey;autoIncrement;column:id"`
	TMDBID    *domain.TMDBID `gorm:"column:tmdb_id"`
	CreatedAt time.Time      `gorm:"column:created_at;not null"`
	UpdatedAt time.Time      `gorm:"column:updated_at;not null"`
}

func (KeywordModel) TableName() string { return "keywords" }

// KeywordI18nModel — one localised name row per (keyword_id, language).
// Same shape as GenreI18nModel — read via the shared §5.6 helper.
type KeywordI18nModel struct {
	KeywordID int64     `gorm:"primaryKey;column:keyword_id"`
	Language  string    `gorm:"primaryKey;column:language;type:text"`
	Name      string    `gorm:"column:name;type:text;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (KeywordI18nModel) TableName() string { return "keywords_i18n" }

// SeriesNetworkModel — join row (PRD §5.3 row "series_networks").
// Composite PK (series_id, network_id); idempotent re-upsert.
// Position is the TMDB ordering on `networks[]`; Set() writes
// position deterministically by input order (i = position).
type SeriesNetworkModel struct {
	SeriesID  domain.SeriesID `gorm:"primaryKey;column:series_id"`
	NetworkID int64           `gorm:"primaryKey;column:network_id"`
	Position  *int            `gorm:"column:position"`
}

func (SeriesNetworkModel) TableName() string { return "series_networks" }

// SeriesCompanyModel — join row (PRD §5.3 row "series_companies").
// Same shape as SeriesNetworkModel.
type SeriesCompanyModel struct {
	SeriesID  domain.SeriesID `gorm:"primaryKey;column:series_id"`
	CompanyID int64           `gorm:"primaryKey;column:company_id"`
	Position  *int            `gorm:"column:position"`
}

func (SeriesCompanyModel) TableName() string { return "series_companies" }

// SeriesGenreModel — join row (PRD §5.3 row "series_genres"). Same
// shape; position preserves the TMDB-emitted order when present.
type SeriesGenreModel struct {
	SeriesID domain.SeriesID `gorm:"primaryKey;column:series_id"`
	GenreID  int64           `gorm:"primaryKey;column:genre_id"`
	Position *int            `gorm:"column:position"`
}

func (SeriesGenreModel) TableName() string { return "series_genres" }

// SeriesKeywordModel — join row (PRD §5.3 row "series_keywords").
// Keywords are unordered per PRD; no position column.
type SeriesKeywordModel struct {
	SeriesID  domain.SeriesID `gorm:"primaryKey;column:series_id"`
	KeywordID int64           `gorm:"primaryKey;column:keyword_id"`
}

func (SeriesKeywordModel) TableName() string { return "series_keywords" }

// VideoModel — TMDB-sourced video row (PRD §5.3 row "videos",
// migration 000029). Natural key tmdb_video_id has a partial unique
// index where not NULL so operator-curated rows (rare) can coexist
// without a TMDB id — mirrors the series/people/taxonomy partial-unique
// pattern from 203/204/205.
type VideoModel struct {
	ID          int64           `gorm:"primaryKey;autoIncrement;column:id"`
	SeriesID    domain.SeriesID `gorm:"column:series_id;not null"`
	TMDBVideoID *string         `gorm:"column:tmdb_video_id;type:text"`
	Name        string          `gorm:"column:name;type:text;not null"`
	Site        *string         `gorm:"column:site;type:text"`
	Key         *string         `gorm:"column:key;type:text"`
	Type        *string         `gorm:"column:type;type:text"`
	Official    bool            `gorm:"column:official;not null;default:false"`
	Language    *string         `gorm:"column:language;type:text"`
	PublishedAt *time.Time      `gorm:"column:published_at"`
	CreatedAt   time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time       `gorm:"column:updated_at;not null"`
}

func (VideoModel) TableName() string { return "videos" }

// ContentRatingModel — per-country age rating row (PRD §5.3 row
// "content_ratings", migration 000029). Composite PK (series_id,
// country_code).
type ContentRatingModel struct {
	SeriesID    domain.SeriesID `gorm:"primaryKey;column:series_id"`
	CountryCode string          `gorm:"primaryKey;column:country_code;type:text"`
	Rating      string          `gorm:"column:rating;type:text;not null"`
	UpdatedAt   time.Time       `gorm:"column:updated_at;not null"`
}

func (ContentRatingModel) TableName() string { return "content_ratings" }

// ExternalIDModel — polymorphic cross-provider id row (PRD §5.3 row
// "external_ids", migration 000029). Composite PK
// (entity_type, entity_id, provider). entity_type domain
// (series|person|episode) is enforced at the domain layer via the
// typed enrichment.EntityType enum, NOT by DB constraint — keeps the
// table schema-portable.
type ExternalIDModel struct {
	EntityType string    `gorm:"primaryKey;column:entity_type;type:text"`
	EntityID   int64     `gorm:"primaryKey;column:entity_id"`
	Provider   string    `gorm:"primaryKey;column:provider;type:text"`
	Value      string    `gorm:"column:value;type:text;not null"`
	UpdatedAt  time.Time `gorm:"column:updated_at;not null"`
}

func (ExternalIDModel) TableName() string { return "external_ids" }

// SeriesRecommendationModel — TMDB-sourced "you might also like" join
// row (PRD §5.3 row "series_recommendations", migration 000029). Self-
// joining on series — recommended_series_id references series.id
// (typically a stub row hydrated by series_enrichment_worker when an
// unknown title first surfaces).
type SeriesRecommendationModel struct {
	SeriesID            domain.SeriesID `gorm:"primaryKey;column:series_id"`
	RecommendedSeriesID domain.SeriesID `gorm:"primaryKey;column:recommended_series_id"`
	Position            *int            `gorm:"column:position"`
	UpdatedAt           time.Time       `gorm:"column:updated_at;not null"`
}

func (SeriesRecommendationModel) TableName() string { return "series_recommendations" }

// PersonCreditModel — materialised filmography row (PRD §5.3 row
// "person_credits", migration 000030). Natural key
// (person_id, tmdb_credit_id) — idempotent re-ingest of TMDB
// /person/{id}/tv_credits + /movie_credits. PosterPath is an upstream
// TMDB image path string in v1; the media downloader picks it up
// lazily on person-page open. Conversion to a media_assets.hash
// reference is deferred to a later media-prewarm story.
type PersonCreditModel struct {
	ID            int64     `gorm:"primaryKey;autoIncrement;column:id"`
	PersonID      int64     `gorm:"column:person_id;not null"`
	TMDBCreditID  string    `gorm:"column:tmdb_credit_id;type:text;not null"`
	MediaType     string    `gorm:"column:media_type;type:text;not null"`
	TMDBMediaID   int       `gorm:"column:tmdb_media_id;not null"`
	Title         string    `gorm:"column:title;type:text;not null"`
	OriginalTitle *string   `gorm:"column:original_title;type:text"`
	Year          *int      `gorm:"column:year"`
	CharacterName *string   `gorm:"column:character_name;type:text"`
	Kind          string    `gorm:"column:kind;type:text;not null"`
	Department    *string   `gorm:"column:department;type:text"`
	Job           *string   `gorm:"column:job;type:text"`
	PosterPath    *string   `gorm:"column:poster_path;type:text"`
	VoteAverage   *float64  `gorm:"column:vote_average"`
	TMDBVotes     *int      `gorm:"column:tmdb_votes"`
	EpisodeCount  *int      `gorm:"column:episode_count"`
	CreatedAt     time.Time `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null"`
}

func (PersonCreditModel) TableName() string { return "person_credits" }

// MediaAssetModel is the persistent row for the media_assets table
// (migration 000024, PRD v4 §6). One row per stored object — the
// bytes live in mediastore; this row is the lookup index for the
// GET /media/:hash endpoint plus future GC sweeps (E-2).
//
// Hash is sha256(source_url) in lowercase hex; doubles as the
// content-addressed primary key. Status lifecycle is pending →
// stored | failed (see domain/media.Status).
type MediaAssetModel struct {
	Hash         string     `gorm:"primaryKey;column:hash;type:text"`
	SourceURL    string     `gorm:"column:source_url;type:text;not null;uniqueIndex:idx_media_assets_source_url"`
	Kind         string     `gorm:"column:kind;type:text;not null"`
	Status       string     `gorm:"column:status;type:text;not null;default:'pending'"`
	ContentType  *string    `gorm:"column:content_type;type:text"`
	SizeBytes    *int64     `gorm:"column:size_bytes"`
	FetchedAt    *time.Time `gorm:"column:fetched_at"`
	LastAccessAt *time.Time `gorm:"column:last_access_at"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null"`
}

func (MediaAssetModel) TableName() string { return "media_assets" }

// QbitTorrentModel — per-(instance_name, hash) snapshot of the last
// known qBit state (PRD v4 §7.3, migration 000035). Story 219 (A-1)
// adds the table; story 220 (A-2) adds the repository and the
// torrentsync loop that writes upsert + state-transition events.
//
// Hash is the v1 infohash in lowercase hex when non-empty, otherwise
// the v2 hash — see `infrastructure/qbit.NormaliseHash`. The
// `present` boolean + `deleted_at` timestamp implement the soft-
// delete pattern PRD §4.6 calls for: a torrent that disappears
// from qBit gets `present=false, deleted_at=now` but the row stays
// forever (history of "what we ever downloaded for this series").
//
// Live telemetry (dlspeed, upspeed, eta, num_seeds, num_leechs,
// progress) is intentionally absent — those fields live in the
// in-memory store (story 220) only. Mutable counters that DO
// persist (ratio, uploaded, time_active_s, popularity,
// last_activity) flush in 5-minute batches to keep write
// amplification low.
type QbitTorrentModel struct {
	InstanceName domain.InstanceName `gorm:"primaryKey;column:instance_name;type:text"`
	Hash         string              `gorm:"primaryKey;column:hash;type:text"`
	InfohashV2   *string             `gorm:"column:infohash_v2;type:text"`
	Name         string              `gorm:"column:name;type:text;not null"`
	Category     *string             `gorm:"column:category;type:text"`
	Tags         *string             `gorm:"column:tags;type:text"`
	TrackerHost  *string             `gorm:"column:tracker_host;type:text"`
	SavePath     *string             `gorm:"column:save_path;type:text"`
	ContentPath  *string             `gorm:"column:content_path;type:text"`
	StateRaw     string              `gorm:"column:state_raw;type:text;not null"`
	StateGroup   string              `gorm:"column:state_group;type:text;not null"`
	SizeBytes    int64               `gorm:"column:size_bytes;not null;default:0"`
	TotalSize    int64               `gorm:"column:total_size;not null;default:0"`
	Downloaded   int64               `gorm:"column:downloaded;not null;default:0"`
	Uploaded     int64               `gorm:"column:uploaded;not null;default:0"`
	Ratio        float64             `gorm:"column:ratio;not null;default:0"`
	Popularity   float64             `gorm:"column:popularity;not null;default:0"`
	TimeActiveS  int64               `gorm:"column:time_active_s;not null;default:0"`
	SeedingTimeS int64               `gorm:"column:seeding_time_s;not null;default:0"`
	AddedOn      *time.Time          `gorm:"column:added_on"`
	CompletionOn *time.Time          `gorm:"column:completion_on"`
	LastActivity *time.Time          `gorm:"column:last_activity"`
	SeasonNumber *int                `gorm:"column:season_number"`
	Present      bool                `gorm:"column:present;not null;default:true"`
	DeletedAt    *time.Time          `gorm:"column:deleted_at"`
	FirstSeenAt  time.Time           `gorm:"column:first_seen_at;not null"`
	UpdatedAt    time.Time           `gorm:"column:updated_at;not null"`
}

func (QbitTorrentModel) TableName() string { return "qbit_torrents" }

// TorrentSeriesMapModel — bridge from a qBit torrent hash to a
// Sonarr series_id (PRD v4 §4.5, §7.3, migration 000035). One row
// per (instance_name, torrent_hash). Populated by three sources in
// priority order: webhook capture (story 220), reconciler lookup in
// `grab_records.torrent_hash` (story 221), Sonarr `/queue` and
// `/history?eventType=1` fallbacks (story 221). The `source` column
// records which path won.
//
// `season_number` is nullable because cross-series packs do not
// exist in Sonarr's release model (one release = one series), but
// individual-episode releases inside a season may bridge without an
// authoritative season number until reconciliation completes —
// nullable lets the row land without lying.
type TorrentSeriesMapModel struct {
	InstanceName domain.InstanceName   `gorm:"primaryKey;column:instance_name;type:text"`
	TorrentHash  domain.QbitHash       `gorm:"primaryKey;column:torrent_hash;type:text"`
	SeriesID     domain.SonarrSeriesID `gorm:"column:series_id;not null"`
	SeasonNumber *int                  `gorm:"column:season_number"`
	Source       string                `gorm:"column:source;type:text;not null"`
	CreatedAt    time.Time             `gorm:"column:created_at;not null"`
}

func (TorrentSeriesMapModel) TableName() string { return "torrent_series_map" }

// QbitTorrentEventModel — append-only log of state_group transitions
// and synthetic added / completed / deleted events (PRD v4 §4.6,
// §7.3, migration 000035). State_group (not state_raw) intentional:
// raw-state churn (stalled↔downloading flapping) would dominate the
// table; PRD §4.6 calls for grain at the group level. Pruned by the
// weekly GC introduced in 218 (E-2) — 180-day retention.
type QbitTorrentEventModel struct {
	ID           int64               `gorm:"primaryKey;autoIncrement;column:id"`
	InstanceName domain.InstanceName `gorm:"column:instance_name;type:text;not null"`
	TorrentHash  domain.QbitHash     `gorm:"column:torrent_hash;type:text;not null"`
	Event        string              `gorm:"column:event;type:text;not null"`
	FromGroup    *string             `gorm:"column:from_group;type:text"`
	ToGroup      *string             `gorm:"column:to_group;type:text"`
	OccurredAt   time.Time           `gorm:"column:occurred_at;not null"`
}

func (QbitTorrentEventModel) TableName() string { return "qbit_torrent_events" }
