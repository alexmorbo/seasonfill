package database

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

type ScanRunModel struct {
	ID              string    `gorm:"primaryKey;size:36;index:idx_scan_runs_created_at_id,priority:2;index:idx_scan_runs_started_at_id,priority:2"`
	InstanceName    string    `gorm:"size:128;index"`
	Trigger         string    `gorm:"size:32"`
	StartedAt       time.Time `gorm:"index:idx_scan_runs_started_at_id,priority:1"`
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
	ScanRunID       *string `gorm:"size:36;index"`
	InstanceName    string  `gorm:"size:128;index"`
	SeriesID        int     `gorm:"index"`
	SeriesTitle     string  `gorm:"size:512"`
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
	ID                string `gorm:"primaryKey;size:36;index:idx_grab_records_created_at_id,priority:2"`
	InstanceName      string `gorm:"size:128;index:idx_grab_inst_series,priority:1;index:idx_grab_dedupe_lookup,priority:1"`
	SeriesID          int    `gorm:"index:idx_grab_inst_series,priority:2;index:idx_grab_dedupe_lookup,priority:2"`
	SeriesTitle       string `gorm:"size:512"`
	SeasonNumber      int    `gorm:"index:idx_grab_inst_series,priority:3;index:idx_grab_dedupe_lookup,priority:3"`
	ReleaseGUID       string `gorm:"size:512;index;index:idx_grab_dedupe_lookup,priority:4"`
	ReleaseTitle      string `gorm:"size:1024"`
	DownloadID        string `gorm:"size:128;index;column:download_id"`
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
	TorrentHash *string `gorm:"column:torrent_hash;size:64"`
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
	InstanceName string `gorm:"primaryKey;size:128"`
	SeriesID     int    `gorm:"primaryKey"`
	SeasonNumber int    `gorm:"primaryKey"`
	GUID         string `gorm:"size:512"`
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

type AdminUserModel struct {
	ID            uint   `gorm:"primaryKey"`
	Username      string `gorm:"size:128;uniqueIndex"`
	PasswordHash  string `gorm:"size:128"`
	AutoGenerated bool
	OIDCSubject   *string `gorm:"column:oidc_subject;type:text;uniqueIndex:idx_admin_users_oidc_subject,where:oidc_subject IS NOT NULL"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (AdminUserModel) TableName() string { return "admin_users" }

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
	ID           uint   `gorm:"primaryKey"`
	InstanceID   uint   `gorm:"uniqueIndex:idx_watchdog_blacklist_triple,priority:1;index:idx_watchdog_blacklist_instance_id"`
	SeriesID     int    `gorm:"uniqueIndex:idx_watchdog_blacklist_triple,priority:2"`
	SeasonNumber int    `gorm:"uniqueIndex:idx_watchdog_blacklist_triple,priority:3"`
	Reason       string `gorm:"type:text;not null"`
	Consecutive  int    `gorm:"not null"`
	CreatedAt    time.Time
	ExpiresAt    *time.Time
}

func (WatchdogBlacklistModel) TableName() string { return "watchdog_blacklist" }

// SeriesCacheModel — per-instance Sonarr series metadata (D66).
// Primary key is (instance_name, sonarr_series_id). Soft-deleted via
// DeletedAt so grab_records that reference removed series stay readable.
// Genres is a JSON-encoded string slice; the repo serialises on write
// and parses on read. No DB-level FK on instance_name (consistent with
// the rest of the schema) — cascade happens application-side.
type SeriesCacheModel struct {
	InstanceName   string     `gorm:"primaryKey;size:128;column:instance_name"`
	SonarrSeriesID int        `gorm:"primaryKey;column:sonarr_series_id"`
	Title          string     `gorm:"type:text;not null"`
	TitleSlug      string     `gorm:"type:text;not null;column:title_slug"`
	Year           *int       `gorm:"column:year"`
	TVDBID         *int       `gorm:"column:tvdb_id"`
	IMDBID         *string    `gorm:"column:imdb_id;type:text"`
	TMDBID         *int       `gorm:"column:tmdb_id"`
	Status         *string    `gorm:"column:status;type:text"`
	Network        *string    `gorm:"column:network;type:text"`
	Genres         *string    `gorm:"column:genres;type:text"`
	RuntimeMinutes *int       `gorm:"column:runtime_minutes"`
	Monitored      bool       `gorm:"column:monitored;not null;default:false"`
	Overview       *string    `gorm:"column:overview;type:text"`
	PosterPath     *string    `gorm:"column:poster_path;type:text"`
	FanartPath     *string    `gorm:"column:fanart_path;type:text"`
	BannerPath     *string    `gorm:"column:banner_path;type:text"`
	MissingCount   int        `gorm:"column:missing_count;not null;default:0"`
	LastAiredAt    *time.Time `gorm:"column:last_aired_at"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null"`
	DeletedAt      *time.Time `gorm:"column:deleted_at"`
}

func (SeriesCacheModel) TableName() string { return "series_cache" }
