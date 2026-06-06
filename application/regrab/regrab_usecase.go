package regrab

//go:generate mockgen -source=ports.go -destination=mocks/ports_mock.go -package=mocks
//go:generate mockgen -source=regrab_usecase.go -destination=mocks/regrab_usecase_mock.go -package=mocks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/grab"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	"github.com/alexmorbo/seasonfill/domain/decision"
	domaingrab "github.com/alexmorbo/seasonfill/domain/grab"
	domainregrab "github.com/alexmorbo/seasonfill/domain/regrab"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/qbit"
	"github.com/alexmorbo/seasonfill/internal/logger"
)

// QbitClientFactory builds a qbit.Client from a fully-resolved Settings
// projection. The factory port lets 039g inject the real
// qbit.NewClient(...) while unit tests pass a stub. The factory takes
// Settings (not raw ciphertext) because the decryption already happened
// in the call site that built Settings — the factory only needs the
// plaintext credentials.
type QbitClientFactory interface {
	NewClient(s Settings) (qbit.Client, error)
}

// DetectorFactory builds a qbit.Detector from a Client + custom-msg
// list. Mirrors QbitClientFactory: real impl returns
// qbit.NewDetector(c, msgs); tests stub it.
type DetectorFactory interface {
	NewDetector(c qbit.Client, customMsgs []string) Detector
}

// Detector is the regrab use case's view of qbit.Detector — a single
// method. Defined here so the test mocks don't need to import
// infrastructure/qbit/.
type Detector interface {
	Detect(ctx context.Context, hash string) (qbit.DetectionResult, error)
}

// SettingsLookup is the regrab use case's view of *SettingsUseCase. The
// use case only ever calls GetByInstanceName + DecryptPassword; we
// abstract both into one Lookup method that returns the fully-resolved
// Settings projection so the use case stays one-shot per RunInstance.
type SettingsLookup interface {
	// Lookup resolves the instance name, reads the settings row,
	// decrypts the password, and returns a Settings projection ready
	// to feed QbitClientFactory. ports.ErrNotFound bubbles for both
	// "instance not found" and "settings not found" paths.
	Lookup(ctx context.Context, instanceName string) (Settings, error)
}

// InstanceRegistry is the regrab use case's view of the running Sonarr
// instance list. RunInstance needs the Sonarr client to feed the
// evaluator (search releases, ForceGrab the selected release). The
// registry is the same one cmd/server passes to scan.UseCase — the
// regrab subscriber (039g) wires it.
type InstanceRegistry interface {
	// Get returns the scan.Instance bundle (config + Sonarr client) by
	// name. Returns (zero, false) on miss — the use case maps to
	// ErrUnknownInstance.
	Get(name string) (scan.Instance, bool)
}

// ErrUnknownInstance — instance name lookup miss in the registry.
// Used by tests to assert the error path.
var ErrUnknownInstance = errors.New("regrab: unknown instance")

// UseCase is the Phase 10 Watchdog regrab orchestrator. Constructed
// once per process by cmd/server (039g) and shared across
// goroutines — its method RunInstance is safe to call concurrently
// for different instance names.
type UseCase struct {
	settings    SettingsLookup
	instances   InstanceRegistry
	qbitFac     QbitClientFactory
	detectorFac DetectorFactory
	grabs       ports.GrabRepository
	cooldowns   ports.CooldownRepository
	blacklist   ports.WatchdogBlacklistRepository
	counter     ports.NoBetterCounterRepository
	evaluate    EvaluateExecutor
	grabExec    GrabExecutor
	metrics     Metrics
	logger      *slog.Logger
	now         func() time.Time
}

// NewUseCase wires the regrab orchestrator. logger=nil → slog.Default().
// metrics=nil → nullMetrics. The instances/qbitFac/detectorFac trio MUST
// be supplied — there are no sensible defaults for those (Sonarr
// registry + qBit factory live in cmd/server).
func NewUseCase(
	settings SettingsLookup,
	instances InstanceRegistry,
	qbitFac QbitClientFactory,
	detectorFac DetectorFactory,
	grabs ports.GrabRepository,
	cooldowns ports.CooldownRepository,
	blacklist ports.WatchdogBlacklistRepository,
	counter ports.NoBetterCounterRepository,
	evaluator EvaluateExecutor,
	grabExec GrabExecutor,
	logger *slog.Logger,
) *UseCase {
	if logger == nil {
		logger = slog.Default()
	}
	return &UseCase{
		settings:    settings,
		instances:   instances,
		qbitFac:     qbitFac,
		detectorFac: detectorFac,
		grabs:       grabs,
		cooldowns:   cooldowns,
		blacklist:   blacklist,
		counter:     counter,
		evaluate:    evaluator,
		grabExec:    grabExec,
		metrics:     nullMetrics{},
		logger:      logger,
		now:         func() time.Time { return time.Now().UTC() },
	}
}

// WithMetrics swaps the null emitter for the production implementation.
// Returning the use case keeps the cmd/server wiring fluent.
func (u *UseCase) WithMetrics(m Metrics) *UseCase {
	if m == nil {
		m = nullMetrics{}
	}
	u.metrics = m
	return u
}

// WithClock pins the time source — tests only.
func (u *UseCase) WithClock(f func() time.Time) *UseCase {
	if f != nil {
		u.now = f
	}
	return u
}

// RunInstance executes one regrab cycle for the named instance.
// Synchronous from this method's perspective: the 039g subscriber owns
// the surrounding goroutine + detached writeCtx. RunInstance is safe to
// invoke concurrently for DIFFERENT instance names; concurrent invocation
// for the SAME instance is the subscriber's contract to prevent (it
// uses a single per-instance ticker, so there's no overlap by design).
func (u *UseCase) RunInstance(ctx context.Context, instanceName string) (RunResult, error) {
	startedAt := u.now()
	res := RunResult{InstanceName: instanceName, StartedAt: startedAt}

	// Step 1 — settings.
	sett, err := u.settings.Lookup(ctx, instanceName)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			// No settings row → treat as disabled (subscriber should
			// not have called us, but be defensive).
			res.FinishedAt = u.now()
			return res, nil
		}
		return res, fmt.Errorf("lookup settings: %w", err)
	}
	if !sett.Enabled {
		res.FinishedAt = u.now()
		return res, nil
	}

	// Step 2 — qBit client + detector.
	client, err := u.qbitFac.NewClient(sett)
	if err != nil {
		u.metrics.IncPollResult(instanceName, "qbit_error")
		u.logger.WarnContext(ctx, "regrab_qbit_client_failed",
			slog.String("instance", instanceName),
			slog.String("error", err.Error()))
		res.QbitError = err
		res.FinishedAt = u.now()
		return res, nil
	}
	defer func() { _ = client.Close() }()
	if err := client.Login(ctx); err != nil {
		u.metrics.IncPollResult(instanceName, "qbit_error")
		u.logger.WarnContext(ctx, "regrab_qbit_login_failed",
			slog.String("instance", instanceName),
			slog.String("error", err.Error()))
		res.QbitError = err
		res.FinishedAt = u.now()
		return res, nil
	}
	det := u.detectorFac.NewDetector(client, sett.CustomUnregisteredMsgs)

	// Step 3 — list torrents.
	torrents, err := client.ListTorrents(ctx)
	if err != nil {
		u.metrics.IncPollResult(instanceName, "qbit_error")
		u.logger.WarnContext(ctx, "regrab_list_torrents_failed",
			slog.String("instance", instanceName),
			slog.String("error", err.Error()))
		res.QbitError = err
		res.FinishedAt = u.now()
		return res, nil
	}
	res.TorrentsSeen = len(torrents)

	// Resolve the Sonarr instance once — the loop body reuses it for
	// each evaluate invocation. Lookup miss → typed error; the
	// subscriber treats this as a hard failure (no point retrying).
	inst, ok := u.instances.Get(instanceName)
	if !ok {
		return res, fmt.Errorf("%w: %q", ErrUnknownInstance, instanceName)
	}

	// Track per-cycle triple short-circuit: the dual-pack + per-episode
	// case from parent §Open-questions §039f. We evaluate any given
	// (series, season) at most once per RunInstance call even if
	// multiple unregistered torrents map to it.
	seenTriple := make(map[tripleKey]struct{}, len(torrents))

	for _, t := range torrents {
		if ctx.Err() != nil {
			break
		}
		if t.Category != "" && sett.Category != "" && t.Category != sett.Category {
			continue
		}
		if t.Hash == "" {
			continue
		}

		// Step 4 — map qBit hash → grab row.
		origGrab, err := u.grabs.FindLatestSuccessByHash(ctx, strings.ToLower(t.Hash))
		if err != nil {
			if errors.Is(err, ports.ErrNotFound) {
				continue // D63: untracked torrent, not ours
			}
			u.logger.WarnContext(ctx, "regrab_lookup_grab_failed",
				slog.String("instance", instanceName),
				slog.String("hash", t.Hash),
				slog.String("error", err.Error()))
			continue
		}

		// Step 5 — detect.
		verdict, err := det.Detect(ctx, t.Hash)
		if err != nil {
			u.logger.WarnContext(ctx, "regrab_detect_failed",
				slog.String("instance", instanceName),
				slog.String("hash", t.Hash),
				slog.String("error", err.Error()))
			continue
		}
		if verdict.TrackerDown {
			res.TrackerDownCount++
			continue
		}
		if !verdict.Unregistered {
			continue
		}
		res.UnregisteredCount++
		u.metrics.IncUnregistered(instanceName, lowerHost(verdict.TrackerURL))

		key := tripleKey{seriesID: origGrab.SeriesID, season: origGrab.SeasonNumber}
		if _, dup := seenTriple[key]; dup {
			continue
		}
		seenTriple[key] = struct{}{}

		cdKey := cooldown.SeriesKey(instanceName, origGrab.SeriesID, origGrab.SeasonNumber)

		// Step 6 — cooldown gate.
		if active, err := u.isCooldownActive(ctx, cdKey, startedAt); err != nil {
			u.logger.WarnContext(ctx, "regrab_cooldown_lookup_failed",
				slog.String("instance", instanceName),
				slog.String("key", cdKey),
				slog.String("error", err.Error()))
			continue
		} else if active {
			res.SkippedCooldown++
			u.metrics.IncRegrabResult(instanceName, string(OutcomeSkipCooldown))
			continue
		}

		// Step 7 — blacklist gate.
		if _, err := u.blacklist.Find(ctx, sett.InstanceID, origGrab.SeriesID, origGrab.SeasonNumber); err == nil {
			res.SkippedBlacklist++
			u.metrics.IncRegrabResult(instanceName, string(OutcomeSkipBlacklist))
			continue
		} else if !errors.Is(err, ports.ErrNotFound) {
			u.logger.WarnContext(ctx, "regrab_blacklist_lookup_failed",
				slog.String("instance", instanceName),
				slog.Int("series_id", origGrab.SeriesID),
				slog.String("error", err.Error()))
			continue
		}

		// Step 8 — evaluate.
		outcome, decisionRow, evalErr := u.runEvaluator(ctx, inst, origGrab)
		if evalErr != nil {
			res.ErrorCount++
			u.metrics.IncRegrabResult(instanceName, string(OutcomeError))
			u.activateCooldown(ctx, cdKey, sett.RegrabCooldown)
			u.logger.WarnContext(ctx, "regrab_evaluate_failed",
				slog.String("instance", instanceName),
				slog.Int("series_id", origGrab.SeriesID),
				slog.Int("season", origGrab.SeasonNumber),
				slog.String("error", evalErr.Error()))
			continue
		}

		// Step 9 — process outcome.
		switch outcome {
		case OutcomeGrabbed:
			grabErr := u.runGrab(ctx, inst, sett, origGrab, decisionRow)
			if grabErr != nil {
				res.ErrorCount++
				u.metrics.IncRegrabResult(instanceName, string(OutcomeError))
			} else {
				res.RegrabbedCount++
				u.metrics.IncRegrabResult(instanceName, string(OutcomeGrabbed))
				// Step 10a — reset counter on success.
				if rstErr := u.counter.Reset(ctx, sett.InstanceID, origGrab.SeriesID, origGrab.SeasonNumber, startedAt); rstErr != nil && !errors.Is(rstErr, ports.ErrNotFound) {
					u.logger.WarnContext(ctx, "regrab_counter_reset_failed",
						slog.String("instance", instanceName),
						slog.String("error", rstErr.Error()))
				}
			}
		case OutcomeNothingBetter, OutcomeFilterDropped:
			// Step 10b — increment counter, maybe escalate to blacklist.
			counter, incErr := u.counter.Increment(ctx, sett.InstanceID, origGrab.SeriesID, origGrab.SeasonNumber, startedAt)
			if incErr != nil {
				u.logger.WarnContext(ctx, "regrab_counter_increment_failed",
					slog.String("instance", instanceName),
					slog.String("error", incErr.Error()))
			} else if counter.HasReachedThreshold(sett.MaxConsecutiveNoBetter) {
				entry, blErr := domainregrab.NewBlacklistEntry(
					sett.InstanceID, origGrab.SeriesID, origGrab.SeasonNumber,
					counter.Consecutive, domainregrab.ReasonConsecutiveNoBetter,
					startedAt)
				if blErr != nil {
					u.logger.WarnContext(ctx, "regrab_blacklist_construct_failed",
						slog.String("instance", instanceName),
						slog.String("error", blErr.Error()))
				} else if wErr := u.blacklist.Upsert(ctx, entry); wErr != nil {
					u.logger.WarnContext(ctx, "regrab_blacklist_write_failed",
						slog.String("instance", instanceName),
						slog.String("error", wErr.Error()))
				} else {
					res.BlacklistedThisCycle = append(res.BlacklistedThisCycle, TripleKey{
						SeriesID:     origGrab.SeriesID,
						SeasonNumber: origGrab.SeasonNumber,
					})
					_ = u.counter.Reset(ctx, sett.InstanceID, origGrab.SeriesID, origGrab.SeasonNumber, startedAt)
					u.logger.InfoContext(ctx, "regrab_blacklisted",
						slog.String("instance", instanceName),
						slog.Int("series_id", origGrab.SeriesID),
						slog.Int("season", origGrab.SeasonNumber),
						slog.Int("consecutive", counter.Consecutive))
				}
			}
			if outcome == OutcomeNothingBetter {
				res.NothingBetterCount++
			} else {
				res.FilterDroppedCount++
			}
			u.metrics.IncRegrabResult(instanceName, string(outcome))
		}

		// Step 11 — cooldown ALWAYS (after a successful evaluate).
		u.activateCooldown(ctx, cdKey, sett.RegrabCooldown)
	}

	u.metrics.IncPollResult(instanceName, "ok")
	res.FinishedAt = u.now()
	return res, nil
}

// runEvaluator is the per-triple evaluator invocation. Resolves the
// Series row via the Sonarr client, builds the evaluate.Input the same
// way the rescan use case does (D60 pattern), and translates the
// evaluator's decision.Outcome into the regrab use case's OutcomeReason
// enum.
func (u *UseCase) runEvaluator(ctx context.Context, inst scan.Instance, origGrab domaingrab.Record) (OutcomeReason, decision.Decision, error) {
	// Sonarr-side reads — same shape as rescan.runDetached.
	seriesRow, err := inst.Client.GetSeries(ctx, origGrab.SeriesID)
	if err != nil {
		return OutcomeError, decision.Decision{}, fmt.Errorf("get series: %w", err)
	}
	episodes, err := inst.Client.ListEpisodes(ctx, origGrab.SeriesID, origGrab.SeasonNumber)
	if err != nil {
		return OutcomeError, decision.Decision{}, fmt.Errorf("list episodes: %w", err)
	}
	fileQuality, err := inst.Client.ListEpisodeFiles(ctx, origGrab.SeriesID)
	if err != nil {
		return OutcomeError, decision.Decision{}, fmt.Errorf("list episode files: %w", err)
	}
	profile, err := inst.Client.GetQualityProfile(ctx, seriesRow.QualityProfile)
	if err != nil {
		return OutcomeError, decision.Decision{}, fmt.Errorf("get profile: %w", err)
	}
	for i := range episodes {
		if q, ok := fileQuality[episodes[i].EpisodeFileID]; ok {
			episodes[i].QualityID = q
		}
	}
	season := series.Season{
		Number:    origGrab.SeasonNumber,
		Monitored: true,
		Episodes:  episodes,
	}

	// Detached writeCtx for the evaluator persist path — D60 pattern.
	// The request ctx may be cancelled mid-iteration; the evaluator's
	// decision row MUST still land.
	runCtx := logger.WithTraceID(context.Background(), origGrab.ID.String())
	d, err := u.evaluate.Execute(runCtx, evaluate.Input{
		ScanRunID:            uuid.New(), // synthetic — re-grab has no parent scan
		Instance:             origGrab.InstanceName,
		Sonarr:               inst.Client,
		Series:               seriesRow,
		Season:               season,
		Profile:              profile,
		MinCustomFormatScore: inst.Config.Search.MinCustomFormatScore,
		RequireAllAired:      inst.Config.Search.RequireAllAired,
		SkipSpecials:         inst.Config.Search.SkipSpecials,
		SkipAnime:            inst.Config.Search.SkipAnime,
		DryRun:               false,
		Now:                  u.now(),
		IgnoreCooldown:       false,
		PreferredDecisionID:  nil,
	})
	if err != nil {
		return OutcomeError, d, err
	}
	return classifyOutcome(d), d, nil
}

// classifyOutcome maps a decision.Decision into the regrab OutcomeReason
// enum. The evaluator's Reason enum is the source of truth for the
// "nothing better" vs "filter dropped" split — see the decision package
// for the full Reason list.
func classifyOutcome(d decision.Decision) OutcomeReason {
	switch d.Outcome {
	case decision.OutcomeGrab:
		if d.Selected != nil {
			return OutcomeGrabbed
		}
		// Grab outcome with nil Selected is a defensive impossibility,
		// treat as nothing-better so the cooldown still throttles.
		return OutcomeNothingBetter
	case decision.OutcomeSkip:
		switch d.Reason {
		case decision.ReasonSkipNoCandidates:
			return OutcomeFilterDropped
		case decision.ReasonSkipNoMissing,
			decision.ReasonSkipFullMissing,
			decision.ReasonSkipNoReleases:
			return OutcomeNothingBetter
		default:
			return OutcomeNothingBetter
		}
	case decision.OutcomeError:
		return OutcomeError
	default:
		return OutcomeNothingBetter
	}
}

// runGrab calls the grab use case with the evaluator's selected release
// and the ReplayOfID audit pointer in hand. The grab use case persists
// the row; we don't second-guess its retry / cooldown logic — that's
// the same path scan + rescan use.
func (u *UseCase) runGrab(ctx context.Context, inst scan.Instance, sett Settings, origGrab domaingrab.Record, d decision.Decision) error {
	if d.Selected == nil {
		return fmt.Errorf("evaluator returned grab outcome but Selected is nil")
	}
	selected := *d.Selected
	cfg := grab.Config{
		MaxAttempts:    inst.Config.Retry.MaxAttempts,
		InitialBackoff: inst.Config.Retry.InitialBackoff,
		MaxBackoff:     inst.Config.Retry.MaxBackoff,
		SeriesCooldown: inst.Config.Cooldown.SeriesAfterGrab,
		GUIDCooldown:   inst.Config.Cooldown.GUIDAfterFailedGrab,
	}

	in := grab.Input{
		ScanRunID:    uuid.New(),
		InstanceName: origGrab.InstanceName,
		SeriesID:     origGrab.SeriesID,
		SeriesTitle:  origGrab.SeriesTitle,
		SeasonNumber: origGrab.SeasonNumber,
		Selected:     selected,
		Coverage:     origGrab.CoverageCount,
		Sonarr:       inst.Client,
		Config:       cfg,
	}

	// Use a detached writeCtx — same reason as rescan: the request ctx
	// may already be cancelled when we land here.
	runCtx := logger.WithTraceID(context.Background(), origGrab.ID.String())
	out := u.grabExec.Execute(runCtx, in)
	if out.Err != nil {
		return fmt.Errorf("grab execute: %w", out.Err)
	}

	// Stamp the audit pointer via SetReplayOfID. This is a follow-up
	// UPDATE called after grab.UseCase.Execute returns success. Trade-off:
	// two writes per re-grab (the original INSERT, then this UPDATE).
	// Acceptable because re-grabs are rare.
	if err := u.grabs.SetReplayOfID(runCtx, out.Record.ID, origGrab.ID); err != nil {
		u.logger.WarnContext(runCtx, "regrab_replay_stamp_failed",
			slog.String("new_id", out.Record.ID.String()),
			slog.String("original_id", origGrab.ID.String()),
			slog.String("error", err.Error()))
		// Best-effort: the grab landed; the audit pointer is missing
		// but the row itself is fine. Do NOT fail the regrab outcome.
	}
	u.logger.InfoContext(runCtx, "regrab_grabbed",
		slog.String("instance", origGrab.InstanceName),
		slog.Int("series_id", origGrab.SeriesID),
		slog.Int("season", origGrab.SeasonNumber),
		slog.String("original_grab_id", origGrab.ID.String()),
		slog.String("new_grab_id", out.Record.ID.String()),
		slog.String("guid", selected.Release.GUID))
	return nil
}

func (u *UseCase) isCooldownActive(ctx context.Context, key string, now time.Time) (bool, error) {
	cd, found, err := u.cooldowns.Get(ctx, cooldown.ScopeRegrabRetry, key)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	return cd.IsActive(now), nil
}

func (u *UseCase) activateCooldown(ctx context.Context, key string, ttl time.Duration) {
	now := u.now()
	cd := cooldown.Cooldown{
		Scope:     cooldown.ScopeRegrabRetry,
		Key:       key,
		ExpiresAt: now.Add(ttl),
		Reason:    "regrab_throttle",
		CreatedAt: now,
	}
	if err := u.cooldowns.Set(ctx, cd); err != nil {
		u.logger.WarnContext(ctx, "regrab_cooldown_set_failed",
			slog.String("key", key),
			slog.String("error", err.Error()))
	}
}

type tripleKey struct {
	seriesID int
	season   int
}

// lowerHost extracts the host portion of a tracker URL and lowercases
// it for the metrics tracker label. Empty / malformed URLs return "".
func lowerHost(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Host)
}

// Compile-time guard the new release symbol stays referenced even if
// classifyOutcome's switch shape changes.
var _ = release.Scored{}
