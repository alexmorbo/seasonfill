package runtime

import (
	"sort"
	"time"
)

type Snapshot struct {
	Cron            CronSnapshot
	Scan            ScanSnapshot
	DryRun          bool
	GlobalRateLimit RateLimitSnapshot
	Auth            AuthSnapshot
	Instances       []InstanceSnapshot
}

type CronSnapshot struct {
	Enabled  bool
	Schedule string
	OnStart  bool
	Jitter   time.Duration
}

type ScanSnapshot struct {
	ShutdownGrace time.Duration
	CooldownSweep time.Duration
}

type RateLimitSnapshot struct {
	RPM   int
	Burst int
}

// Poster proxy tuning. These live as package-level constants (not DB-
// persisted) because the whole point of the poster fix is to give
// operators a sensible default they don't have to think about. Sized
// for a 60-poster frontend grid: 60-token burst drains instantly,
// 200 rpm sustains heavy navigation, and the 256 MiB cache holds
// every poster for a 1000-series library at 200 KB/each.
const (
	PosterLimitRPM   = 200
	PosterLimitBurst = 60
	// PosterCacheMaxBytes — total cache size cap. Approximate, byte-
	// accounted on Put with a small per-entry overhead.
	PosterCacheMaxBytes int64 = 256 << 20
	// PosterCacheTTL — entries older than this look like misses to
	// the next Get; eviction is lazy.
	PosterCacheTTL = 24 * time.Hour
)

// Queue episodes-embed cache tuning. The Missing handler embeds
// per-episode presence inline for small seasons (aired ≤ 100) by
// fetching the full episode list per series and slicing in memory.
// Cache hits skip the upstream call entirely. Episodes are
// lightweight (~150 B each); 32 MiB holds ~200k episodes — more
// than any realistic library. TTL is short so operator-driven
// /missing polls reflect new imports within the next refetch.
const (
	EpisodesCacheMaxBytes int64         = 32 << 20 // 32 MiB
	EpisodesCacheTTL      time.Duration = 5 * time.Minute
	// MissingPerSeriesEpisodeFetchConcurrency caps the parallel
	// ListEpisodesBySeries fan-out per /missing request. Each goroutine
	// still serializes on the per-instance Sonarr rate limiter inside
	// the client, so the practical ceiling is the limiter burst; 5
	// keeps the in-process scheduler load light while shaving wall-
	// clock by ~5× vs. sequential on a typical 9-series backlog.
	MissingPerSeriesEpisodeFetchConcurrency = 5
	// MissingSeasonEmbedAiredCap — seasons with more aired episodes
	// than this skip the inline embed (the chip grid wouldn't fit on
	// screen anyway). The drill endpoint stays the fallback.
	MissingSeasonEmbedAiredCap = 100
)

// AuthModeForms, AuthModeBasic, AuthModeNone enumerate the auth backends
// the dispatcher accepts. Any other value is rejected at validation time
// (the DB-layer CHECK constraint only exists on postgres; the
// application layer is the single source of truth for the enum).
const (
	AuthModeForms = "forms"
	AuthModeBasic = "basic"
	AuthModeNone  = "none"
	AuthModeOIDC  = "oidc"
)

// OIDCSnapshot carries OIDC provider settings from runtime_config plus the
// resolved client secret (env > DB-decrypted). ClientSecret is transient —
// populated by the reload subscriber at publish time and never written to the
// wire. GroupsClaim is the dot-notation path into ID-token claims used by
// the group ACL.
type OIDCSnapshot struct {
	Issuer        string
	ClientID      string
	ClientSecret  string
	RedirectURL   string
	Scopes        []string
	UsernameClaim string
	AllowedGroups []string
	GroupsClaim   string
}

// IsReady reports whether the OIDC routes can accept traffic. The
// redirect_url field is intentionally NOT required here — empty
// triggers auto-derivation from request headers at Start() time.
func (o OIDCSnapshot) IsReady() bool {
	return o.Issuer != "" && o.ClientID != "" && o.ClientSecret != ""
}

func DefaultOIDCSnapshot() OIDCSnapshot {
	return OIDCSnapshot{
		Scopes:        []string{"openid", "profile", "email"},
		UsernameClaim: "preferred_username",
		AllowedGroups: []string{},
		GroupsClaim:   "groups",
	}
}

// DefaultAuthLocalNetworks is the hardcoded private/loopback/link-local/ULA
// allow-list used both at fresh-install seed time and as the snapshot
// fallback when a stored row lacks the column. Kept in sync with the
// migration v2 default JSON literal.
func DefaultAuthLocalNetworks() []string {
	return []string{
		"127.0.0.0/8",
		"::1/128",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"fe80::/10",
		"fc00::/7",
	}
}

type AuthSnapshot struct {
	SessionTTL     time.Duration
	SecureCookie   bool
	TrustedProxies []string
	// Mode is one of AuthMode{Forms,Basic,None,OIDC}.
	Mode string
	// LocalBypass=true → trusted-local clients skip auth entirely
	// (except for /api/v1/webhook/*, which always requires X-Api-Key).
	LocalBypass bool
	// LocalNetworks is the CIDR allow-list driving local-bypass.
	LocalNetworks []string
	// SessionEpoch is bumped by the usecase whenever a change should
	// invalidate live sessions (mode change, bypass toggle, network
	// list change). Cookies carry the epoch they were minted under;
	// the middleware rejects payloads with ep < SessionEpoch.
	SessionEpoch int64
	OIDC         OIDCSnapshot
}

type InstanceSnapshot struct {
	ID            uint
	Name          string
	URL           string
	APIKey        string // plaintext (decrypted)
	Mode          string
	Timeout       time.Duration
	SearchTimeout time.Duration
	DryRun        *bool
	Tags          TagsSnapshot
	Search        SearchSnapshot
	Ranking       RankingSnapshot
	Limits        LimitsSnapshot
	RateLimit     RateLimitSnapshot
	Cooldown      CooldownSnapshot
	Retry         RetrySnapshot
	HealthCheck   HealthCheckSnapshot
	// PublicURL is the browser-facing URL (D64). nil = fall back to URL.
	// Set only by the instance load/save path; downstream callers go
	// through UIURL() instead of reading the pointer directly.
	PublicURL *string
	// WebhookInstallEnabled toggles the auto-install reconciler (D65).
	// True on every existing row (migration default).
	WebhookInstallEnabled bool
	// WebhookURLOverride is an optional base URL for the webhook (D65).
	// nil = use derivedPublic supplied by the caller.
	WebhookURLOverride *string
	// ParseOnGrabEnabled toggles 044b's OnGrab → /api/v3/parse hook.
	// Defaults to true on every existing row (migration default).
	ParseOnGrabEnabled bool
	// ScanSkipHandledSeasons toggles 046b's pre-filter that short-circuits
	// seasons Sonarr already handles (complete OR zero-on-disk). True on
	// every existing row (migration 000017 default). The `all_complete`
	// branch runs regardless (no-op safety net); only `sonarr_handles`
	// is flag-gated — flip false when investigating why seasonfill
	// isn't picking up a seemingly-orphaned season.
	ScanSkipHandledSeasons bool
}

// UIURL returns the URL the browser should link to (D64). If PublicURL
// is set and non-empty, it wins; otherwise we fall back to URL (the
// internal Sonarr endpoint that the backend uses for API calls).
func (i InstanceSnapshot) UIURL() string {
	if i.PublicURL != nil && *i.PublicURL != "" {
		return *i.PublicURL
	}
	return i.URL
}

// WebhookBaseURL returns the base URL the webhook reconciler should
// install in Sonarr (D65). If WebhookURLOverride is set and non-empty,
// it wins; otherwise the supplied derivedPublic (typically the
// app-level public URL from runtime_config) is used.
func (i InstanceSnapshot) WebhookBaseURL(derivedPublic string) string {
	if i.WebhookURLOverride != nil && *i.WebhookURLOverride != "" {
		return *i.WebhookURLOverride
	}
	return derivedPublic
}

type TagsSnapshot struct {
	Mode    string
	Include []string
	Exclude []string
}

type SearchSnapshot struct {
	RequireAllAired      bool
	SkipSpecials         bool
	SkipAnime            bool
	MinCustomFormatScore int
}

type RankingSnapshot struct {
	IndexerPriorityEnabled bool
	OriginBonus            float64
}

type LimitsSnapshot struct {
	ScanMaxSeries   int
	MaxGrabsPerScan int
}

type CooldownSnapshot struct {
	Mode                  string
	SeriesAfterGrab       time.Duration
	GUIDAfterFailedGrab   time.Duration
	GUIDAfterFailedImport time.Duration
}

type RetrySnapshot struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

type HealthCheckSnapshot struct {
	RecheckAuth    time.Duration
	RecheckNetwork time.Duration
}

func Defaults() Snapshot {
	return Snapshot{
		Cron: CronSnapshot{
			Enabled:  true,
			Schedule: "0 */6 * * *",
			OnStart:  false,
			Jitter:   time.Minute,
		},
		Scan: ScanSnapshot{
			ShutdownGrace: 60 * time.Second,
			CooldownSweep: 15 * time.Minute,
		},
		DryRun: true,
		GlobalRateLimit: RateLimitSnapshot{
			RPM: 30, Burst: 10,
		},
		Auth: AuthSnapshot{
			SessionTTL:     12 * time.Hour,
			SecureCookie:   false,
			TrustedProxies: []string{"127.0.0.1", "::1"},
			Mode:           AuthModeForms,
			LocalBypass:    false,
			LocalNetworks:  DefaultAuthLocalNetworks(),
			SessionEpoch:   0,
			OIDC:           DefaultOIDCSnapshot(),
		},
	}
}

// MaxSearchTimeoutDefault caps the value the Timeout*6 default writes
// into SearchTimeout when the caller omits it. Must stay <= the
// instance.SearchTimeout validator max (600s) or callers with a large
// explicit Timeout would land a default value that fails validation.
const MaxSearchTimeoutDefault = 600 * time.Second

func ApplyInstanceDefaults(inst *InstanceSnapshot) {
	if inst.Timeout <= 0 {
		inst.Timeout = 10 * time.Second
	}
	if inst.SearchTimeout <= 0 {
		inst.SearchTimeout = min(inst.Timeout*6, MaxSearchTimeoutDefault)
	}
	if inst.Cooldown.Mode == "" {
		inst.Cooldown.Mode = "smart"
	}
	if inst.Cooldown.SeriesAfterGrab == 0 {
		inst.Cooldown.SeriesAfterGrab = 24 * time.Hour
	}
	if inst.Cooldown.GUIDAfterFailedGrab == 0 {
		inst.Cooldown.GUIDAfterFailedGrab = 72 * time.Hour
	}
	if inst.Cooldown.GUIDAfterFailedImport == 0 {
		inst.Cooldown.GUIDAfterFailedImport = 48 * time.Hour
	}
	if inst.Retry.MaxAttempts == 0 {
		inst.Retry.MaxAttempts = 3
	}
	if inst.Retry.InitialBackoff == 0 {
		inst.Retry.InitialBackoff = time.Second
	}
	if inst.Retry.MaxBackoff == 0 {
		inst.Retry.MaxBackoff = 30 * time.Second
	}
	if inst.Limits.MaxGrabsPerScan == 0 {
		inst.Limits.MaxGrabsPerScan = 10
	}
	if inst.HealthCheck.RecheckAuth <= 0 {
		inst.HealthCheck.RecheckAuth = 5 * time.Minute
	}
	if inst.HealthCheck.RecheckNetwork <= 0 {
		inst.HealthCheck.RecheckNetwork = time.Minute
	}
	if inst.Mode == "" {
		inst.Mode = "auto"
	}
	// 046b: no nil-pointer story (ScanSkipHandledSeasons is a concrete
	// bool); the migration DEFAULT TRUE handles every existing row. This
	// branch covers freshly-constructed snapshots (registry reload, test
	// fixtures) so a zero-value struct still gets the production default.
	// Callers building from a request go through requestToSnapshot which
	// collapses the *bool with scanSkipHandledSeasonsOrDefault BEFORE
	// reaching ApplyInstanceDefaults — so we never accidentally undo an
	// explicit `false` from an API client.
	if !inst.ScanSkipHandledSeasons {
		inst.ScanSkipHandledSeasons = true
	}
}

func SortInstances(s []InstanceSnapshot) {
	sort.SliceStable(s, func(i, j int) bool { return s[i].Name < s[j].Name })
}
