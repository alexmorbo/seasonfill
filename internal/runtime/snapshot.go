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

type AuthSnapshot struct {
	SessionTTL     time.Duration
	SecureCookie   bool
	TrustedProxies []string
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
}

func SortInstances(s []InstanceSnapshot) {
	sort.SliceStable(s, func(i, j int) bool { return s[i].Name < s[j].Name })
}
