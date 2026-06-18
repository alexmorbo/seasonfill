package regrab

import (
	"time"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Settings is the use-case-friendly projection of ports.QbitSettingsRecord
// plus the resolved instance name. The settings_usecase API returns
// QbitSettingsView (which masks PasswordEncrypted as PasswordSet bool);
// the regrab use case in 039f-2 needs the ciphertext to decrypt and
// build a qbit.Client, so it cannot consume the view. Hence a separate
// projection here, populated by NewSettingsFromRecord below.
//
// PasswordPlaintext is filled in by the regrab use case after a
// successful Cipher.Open; the settings repository never sees plaintext.
type Settings struct {
	InstanceID             uint
	InstanceName           domain.InstanceName
	Enabled                bool
	URL                    string
	Username               string
	PasswordPlaintext      string
	Category               string
	PollInterval           time.Duration
	RegrabCooldown         time.Duration
	MaxConsecutiveNoBetter int
	CustomUnregisteredMsgs []string
	// PublicURL is the optional browser-reachable qBittorrent web UI URL
	// (082, F-P2-1). Empty string when unset. The regrab loop never
	// consumes this — it is a passthrough for the SPA GrabDrawer deep
	// link, and the loop's bootstrap projects it on each instance load
	// so the runtime cache stays in sync after a settings hot-swap.
	PublicURL string
	UpdatedAt time.Time
}

// NewSettingsFromRecord projects a repository record into the use-case
// shape and decrypts the password via the supplied cipher. Returns the
// raw cipher.Open error wrapped so the caller can log the failure
// without dumping ciphertext bytes.
//
// The instance name is passed alongside the record because the
// repository row only carries InstanceID; the regrab loop has the name
// in hand (it cycled instances by name in step 1 of RunInstance) and
// the projection's downstream consumers (slog, metrics labels) need
// the human-readable name.
func NewSettingsFromRecord(rec ports.QbitSettingsRecord, instanceName domain.InstanceName, cipher *crypto.Cipher) (Settings, error) {
	out := Settings{
		InstanceID:             rec.InstanceID,
		InstanceName:           instanceName,
		Enabled:                rec.Enabled,
		URL:                    rec.URL,
		Category:               rec.Category,
		PollInterval:           time.Duration(rec.PollIntervalMinutes) * time.Minute,
		RegrabCooldown:         time.Duration(rec.RegrabCooldownHours) * time.Hour,
		MaxConsecutiveNoBetter: rec.MaxConsecutiveNoBetter,
		CustomUnregisteredMsgs: rec.CustomUnregisteredMsgs,
		PublicURL:              rec.PublicURL,
		UpdatedAt:              rec.UpdatedAt,
	}
	if rec.Username != nil {
		out.Username = *rec.Username
	}
	if len(rec.PasswordEncrypted) == 0 {
		return out, nil
	}
	if cipher == nil {
		return Settings{}, errCipherRequired
	}
	pt, err := cipher.Open(rec.PasswordEncrypted)
	if err != nil {
		return Settings{}, err
	}
	out.PasswordPlaintext = string(pt)
	return out, nil
}

// OutcomeReason is the metric `result` label value the regrab use case
// emits on each RunInstance iteration. Frozen const block per parent
// 039 D-T5 / §Open-questions §039f — adding a new outcome means a new
// constant here AND a co-ordinated update of any Grafana dashboards
// that consume seasonfill_watchdog_regrab_triggered_total{result}.
type OutcomeReason string

const (
	// OutcomeGrabbed — evaluator picked a candidate, grab succeeded.
	OutcomeGrabbed OutcomeReason = "grabbed"
	// OutcomeNothingBetter — evaluator found candidates but none scored
	// better than the existing file (no upgrade-worthy release).
	OutcomeNothingBetter OutcomeReason = "nothing_better"
	// OutcomeFilterDropped — evaluator's Filter stage dropped every
	// candidate (cooldown, format, quality, etc.).
	OutcomeFilterDropped OutcomeReason = "filter_dropped"
	// OutcomeError — evaluator or grab path returned a transport error.
	OutcomeError OutcomeReason = "error"
	// OutcomeSkipCooldown — regrab-retry cooldown is active for the triple.
	OutcomeSkipCooldown OutcomeReason = "skip_cooldown"
	// OutcomeSkipBlacklist — triple is on the watchdog blacklist.
	OutcomeSkipBlacklist OutcomeReason = "skip_blacklist"
	// OutcomeSkipUnknown — torrent state didn't match unregistered or
	// tracker-down patterns (no-op iteration).
	OutcomeSkipUnknown OutcomeReason = "skip_unknown"
)

// IsTerminal reports whether the outcome is one that should activate
// the regrab-retry cooldown. All outcomes EXCEPT OutcomeSkipCooldown,
// OutcomeSkipBlacklist, and OutcomeSkipUnknown are terminal — they
// represent a real decision the cooldown should throttle.
func (o OutcomeReason) IsTerminal() bool {
	switch o {
	case OutcomeSkipCooldown, OutcomeSkipBlacklist, OutcomeSkipUnknown:
		return false
	default:
		return true
	}
}

// RunResult is the per-iteration summary the regrab use case returns
// from RunInstance. The reload-bus subscriber (039g) aggregates these
// across iterations to feed metrics + slog.
type RunResult struct {
	InstanceName         domain.InstanceName
	TorrentsSeen         int
	UnregisteredCount    int
	TrackerDownCount     int
	RegrabbedCount       int
	NothingBetterCount   int
	FilterDroppedCount   int
	ErrorCount           int
	SkippedCooldown      int
	SkippedBlacklist     int
	BlacklistedThisCycle []TripleKey
	QbitError            error
	StartedAt            time.Time
	FinishedAt           time.Time
}

// TripleKey is the lightweight (series, season) pair the RunResult
// uses to report fresh blacklist entries. Instance name is implicit
// in the parent RunResult.
type TripleKey struct {
	SeriesID     domain.SonarrSeriesID
	SeasonNumber int
}
