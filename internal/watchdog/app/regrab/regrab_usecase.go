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

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
	grab "github.com/alexmorbo/seasonfill/internal/grab/app"
	"github.com/alexmorbo/seasonfill/internal/grab/app/evaluate"
	domaingrab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	"github.com/alexmorbo/seasonfill/internal/grab/domain/decision"
	"github.com/alexmorbo/seasonfill/internal/logger"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/qbit"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
	domainregrab "github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
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
	Lookup(ctx context.Context, instanceName domain.InstanceName) (Settings, error)
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

// regrabDebugHashSample bounds the per-cycle "first N hashes" sample
// emitted at debug level by the regrab_torrents_listed checkpoint. Kept
// small so debug output remains readable on a 100+ torrent client.
const regrabDebugHashSample = 3

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
	// decisions is the optional DecisionRepository used to refine
	// the watchdog Intent payload post-Execute (091a / F-P2-2). When
	// nil, the placeholder ChosenBecauseWatchdogBetterOther sticks
	// — frontend still renders something useful (the parent grab id
	// in the detail string).
	decisions ports.DecisionRepository
	evaluate  EvaluateExecutor
	grabExec  GrabExecutor
	metrics   Metrics
	state     *RuntimeStateStore
	logger    *slog.Logger
	now       func() time.Time
	// releaseGoneClassifier reports whether a Sonarr ForceGrab error is
	// a 404 / 410 "release gone on indexer" — the signal that lets the
	// same-GUID replay path fall through to the evaluator search instead
	// of surfacing as OutcomeError. Defaults to sonarr.IsReleaseGone.
	// Tests inject a stub via WithReleaseGoneClassifier so they don't
	// have to construct real Sonarr StatusError values.
	releaseGoneClassifier func(error) bool
	// releaseAlreadyAddedClassifier reports whether a Sonarr ForceGrab
	// error is the success-equivalent "qBit already has this hash"
	// case (story 117). Default sonarr.IsReleaseAlreadyAdded; tests
	// inject a stub via WithReleaseAlreadyAddedClassifier the same way
	// they do for releaseGoneClassifier.
	releaseAlreadyAddedClassifier func(error) bool
}

// NewUseCase wires the regrab orchestrator. logger=nil → sharedports.DomainLogger(slog.Default(), "watchdog") per F-4b-3.
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
		logger = sharedports.DomainLogger(slog.Default(), "watchdog")
	}
	return &UseCase{
		settings:                      settings,
		instances:                     instances,
		qbitFac:                       qbitFac,
		detectorFac:                   detectorFac,
		grabs:                         grabs,
		cooldowns:                     cooldowns,
		blacklist:                     blacklist,
		counter:                       counter,
		evaluate:                      evaluator,
		grabExec:                      grabExec,
		metrics:                       nullMetrics{},
		state:                         NewRuntimeStateStore(),
		logger:                        logger,
		now:                           func() time.Time { return time.Now().UTC() },
		releaseGoneClassifier:         sonarr.IsReleaseGone,
		releaseAlreadyAddedClassifier: sonarr.IsReleaseAlreadyAdded,
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

// WithDecisions wires the DecisionRepository so the watchdog Intent
// payload can be refined post-Execute (091a / F-P2-2). Optional —
// when unset, the watchdog placeholder stays on every replay
// decision row.
func (u *UseCase) WithDecisions(d ports.DecisionRepository) *UseCase {
	u.decisions = d
	return u
}

// WithReleaseGoneClassifier swaps the Sonarr 404/410 detector — tests
// only. Nil restores the production sonarr.IsReleaseGone adapter so a
// caller can reset the hook without having to know the default.
func (u *UseCase) WithReleaseGoneClassifier(fn func(error) bool) *UseCase {
	if fn == nil {
		fn = sonarr.IsReleaseGone
	}
	u.releaseGoneClassifier = fn
	return u
}

// WithReleaseAlreadyAddedClassifier swaps the Sonarr 500+qBit-409
// detector — tests only. Nil restores the production
// sonarr.IsReleaseAlreadyAdded adapter so a caller can reset the hook
// without having to know the default.
func (u *UseCase) WithReleaseAlreadyAddedClassifier(fn func(error) bool) *UseCase {
	if fn == nil {
		fn = sonarr.IsReleaseAlreadyAdded
	}
	u.releaseAlreadyAddedClassifier = fn
	return u
}

// RunInstance executes one regrab cycle for the named instance.
// Synchronous from this method's perspective: the 039g subscriber owns
// the surrounding goroutine + detached writeCtx. RunInstance is safe to
// invoke concurrently for DIFFERENT instance names; concurrent invocation
// for the SAME instance is the subscriber's contract to prevent (it
// uses a single per-instance ticker, so there's no overlap by design).
func (u *UseCase) RunInstance(ctx context.Context, instanceName domain.InstanceName) (RunResult, error) {
	startedAt := u.now()
	res := RunResult{InstanceName: instanceName, StartedAt: startedAt}

	// Step 1 — settings.
	sett, err := u.settings.Lookup(ctx, instanceName)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			// No settings row → treat as disabled (subscriber should
			// not have called us, but be defensive).
			res.FinishedAt = u.now()
			u.state.StampPartial(instanceName, res.FinishedAt, PollResultSkipped, false, -1)
			return res, nil
		}
		return res, fmt.Errorf("lookup settings: %w", err)
	}
	if !sett.Enabled {
		res.FinishedAt = u.now()
		u.state.StampPartial(instanceName, res.FinishedAt, PollResultSkipped, false, -1)
		return res, nil
	}

	// Step 2 — qBit client + detector.
	client, err := u.qbitFac.NewClient(sett)
	if err != nil {
		u.metrics.IncPollResult(instanceName, "qbit_error")
		u.logger.WarnContext(ctx, "regrab_qbit_client_failed",
			slog.String("instance", string(instanceName)),
			slog.String("error", err.Error()))
		res.QbitError = err
		res.FinishedAt = u.now()
		u.state.StampPartial(instanceName, res.FinishedAt, PollResultQbitError, false, -1)
		return res, nil
	}
	defer func() { _ = client.Close() }()
	if err := client.Login(ctx); err != nil {
		u.metrics.IncPollResult(instanceName, "qbit_error")
		u.logger.WarnContext(ctx, "regrab_qbit_login_failed",
			slog.String("instance", string(instanceName)),
			slog.String("error", err.Error()))
		res.QbitError = err
		res.FinishedAt = u.now()
		u.state.StampPartial(instanceName, res.FinishedAt, PollResultQbitError, false, -1)
		return res, nil
	}
	det := u.detectorFac.NewDetector(client, sett.CustomUnregisteredMsgs)

	// Step 3 — list torrents.
	torrents, err := client.ListTorrents(ctx)
	if err != nil {
		u.metrics.IncPollResult(instanceName, "qbit_error")
		u.logger.WarnContext(ctx, "regrab_list_torrents_failed",
			slog.String("instance", string(instanceName)),
			slog.String("error", err.Error()))
		res.QbitError = err
		res.FinishedAt = u.now()
		u.state.StampPartial(instanceName, res.FinishedAt, PollResultQbitError, false, -1)
		return res, nil
	}
	res.TorrentsSeen = len(torrents)

	if u.logger.Enabled(ctx, slog.LevelDebug) {
		sampleN := min(len(torrents), regrabDebugHashSample)
		sampleHashes := make([]string, 0, sampleN)
		for i := range sampleN {
			sampleHashes = append(sampleHashes, torrents[i].Hash)
		}
		u.logger.DebugContext(ctx, "regrab_torrents_listed",
			slog.String("instance", string(instanceName)),
			slog.Int("count", len(torrents)),
			slog.String("category_filter", sett.Category),
			slog.Any("sample_hashes", sampleHashes))
	}

	// Watched = post-category-filter torrent count. Computed before the
	// outer loop so the value is stable across the iteration and ready
	// for the success-path Stamp call below.
	watched := 0
	for _, t := range torrents {
		if t.Category != "" && sett.Category != "" && t.Category != sett.Category {
			continue
		}
		watched++
	}

	inst, ok := u.instances.Get(string(instanceName))
	if !ok {
		res.FinishedAt = u.now()
		u.state.StampPartial(instanceName, res.FinishedAt, PollResultQbitError, true, watched)
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

		u.logger.DebugContext(ctx, "regrab_iter_torrent",
			slog.String("instance", string(instanceName)),
			slog.String("hash", t.Hash),
			slog.String("category", t.Category),
			slog.String("state", t.State))

		// Step 4 — map qBit hash → grab row.
		origGrab, err := u.grabs.FindLatestSuccessByHash(ctx, strings.ToLower(t.Hash))
		if err != nil {
			if errors.Is(err, ports.ErrNotFound) {
				u.logger.DebugContext(ctx, "regrab_grab_lookup_miss",
					slog.String("instance", string(instanceName)),
					slog.String("hash", t.Hash),
					slog.String("category", t.Category),
					slog.String("name", t.Name))
				continue // D63: untracked torrent, not ours
			}
			u.logger.WarnContext(ctx, "regrab_lookup_grab_failed",
				slog.String("instance", string(instanceName)),
				slog.String("hash", t.Hash),
				slog.String("error", err.Error()))
			continue
		}
		u.logger.DebugContext(ctx, "regrab_grab_lookup_hit",
			slog.String("instance", string(instanceName)),
			slog.String("hash", t.Hash),
			slog.String("grab_id", origGrab.ID.String()),
			slog.Int("series_id", int(origGrab.SeriesID)),
			slog.Int("season", origGrab.SeasonNumber),
			slog.String("status", string(origGrab.Status)))

		// Step 5 — detect.
		verdict, err := det.Detect(ctx, t.Hash)
		if err != nil {
			u.logger.WarnContext(ctx, "regrab_detect_failed",
				slog.String("instance", string(instanceName)),
				slog.String("hash", t.Hash),
				slog.String("error", err.Error()))
			continue
		}
		u.logger.DebugContext(ctx, "regrab_verdict",
			slog.String("instance", string(instanceName)),
			slog.String("hash", t.Hash),
			slog.Bool("unregistered", verdict.Unregistered),
			slog.Bool("tracker_down", verdict.TrackerDown),
			slog.String("tracker_msg", verdict.TrackerMsg),
			slog.String("tracker_url", verdict.TrackerURL))
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
				slog.String("instance", string(instanceName)),
				slog.String("key", cdKey),
				slog.String("error", err.Error()))
			continue
		} else if active {
			u.logger.DebugContext(ctx, "regrab_cooldown_skipped",
				slog.String("instance", string(instanceName)),
				slog.String("key", cdKey),
				slog.Int("series_id", int(origGrab.SeriesID)),
				slog.Int("season", origGrab.SeasonNumber))
			res.SkippedCooldown++
			u.metrics.IncRegrabResult(instanceName, string(OutcomeSkipCooldown))
			continue
		}
		u.logger.DebugContext(ctx, "regrab_cooldown_passed",
			slog.String("instance", string(instanceName)),
			slog.String("key", cdKey),
			slog.Int("series_id", int(origGrab.SeriesID)),
			slog.Int("season", origGrab.SeasonNumber))

		// Step 7 — blacklist gate.
		if _, err := u.blacklist.Find(ctx, sett.InstanceID, origGrab.SeriesID, origGrab.SeasonNumber); err == nil {
			u.logger.DebugContext(ctx, "regrab_blacklist_skipped",
				slog.String("instance", string(instanceName)),
				slog.Int("series_id", int(origGrab.SeriesID)),
				slog.Int("season", origGrab.SeasonNumber))
			res.SkippedBlacklist++
			u.metrics.IncRegrabResult(instanceName, string(OutcomeSkipBlacklist))
			continue
		} else if !errors.Is(err, ports.ErrNotFound) {
			u.logger.WarnContext(ctx, "regrab_blacklist_lookup_failed",
				slog.String("instance", string(instanceName)),
				slog.Int("series_id", int(origGrab.SeriesID)),
				slog.String("error", err.Error()))
			continue
		}
		u.logger.DebugContext(ctx, "regrab_blacklist_passed",
			slog.String("instance", string(instanceName)),
			slog.Int("series_id", int(origGrab.SeriesID)),
			slog.Int("season", origGrab.SeasonNumber))

		// Step 8 — evaluate. Pass the qBit verdict in so the
		// unregistered branch can try a same-GUID replay before
		// falling into the full evaluator search (114).
		outcome, decisionRow, evalErr := u.runEvaluator(ctx, inst, origGrab, verdict)
		if evalErr != nil {
			res.ErrorCount++
			u.metrics.IncRegrabResult(instanceName, string(OutcomeError))
			u.activateCooldown(ctx, cdKey, sett.RegrabCooldown)
			if decisionRow.ID == uuid.Nil {
				// Pre-117 audit-trail behaviour: no decision row was
				// written, so the slog WARN IS the audit trail.
				u.logger.WarnContext(ctx, "regrab_evaluate_failed",
					slog.String("instance", string(instanceName)),
					slog.Int("series_id", int(origGrab.SeriesID)),
					slog.Int("season", origGrab.SeasonNumber),
					slog.String("error", evalErr.Error()))
			} else {
				// 117: decision row WAS written by the replay path.
				// Emit a lower-volume INFO referencing the decision
				// so operators can correlate logs ↔ Activity Feed.
				u.logger.InfoContext(ctx, "regrab_replay_error_persisted",
					slog.String("instance", string(instanceName)),
					slog.Int("series_id", int(origGrab.SeriesID)),
					slog.Int("season", origGrab.SeasonNumber),
					slog.String("decision_id", decisionRow.ID.String()),
					slog.String("error", evalErr.Error()))
			}
			continue
		}
		u.logger.DebugContext(ctx, "regrab_evaluated",
			slog.String("instance", string(instanceName)),
			slog.String("hash", t.Hash),
			slog.Int("series_id", int(origGrab.SeriesID)),
			slog.Int("season", origGrab.SeasonNumber),
			slog.String("outcome", string(outcome)),
			slog.String("decision_id", decisionRow.ID.String()))

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
						slog.String("instance", string(instanceName)),
						slog.String("error", rstErr.Error()))
				}
			}
		case OutcomeNothingBetter, OutcomeFilterDropped:
			// Step 10b — increment counter, maybe escalate to blacklist.
			counter, incErr := u.counter.Increment(ctx, sett.InstanceID, origGrab.SeriesID, origGrab.SeasonNumber, startedAt)
			if incErr != nil {
				u.logger.WarnContext(ctx, "regrab_counter_increment_failed",
					slog.String("instance", string(instanceName)),
					slog.String("error", incErr.Error()))
			} else if counter.HasReachedThreshold(sett.MaxConsecutiveNoBetter) {
				entry, blErr := domainregrab.NewBlacklistEntry(
					sett.InstanceID, origGrab.SeriesID, origGrab.SeasonNumber,
					counter.Consecutive, domainregrab.ReasonConsecutiveNoBetter,
					startedAt)
				if blErr != nil {
					u.logger.WarnContext(ctx, "regrab_blacklist_construct_failed",
						slog.String("instance", string(instanceName)),
						slog.String("error", blErr.Error()))
				} else if wErr := u.blacklist.Upsert(ctx, entry); wErr != nil {
					u.logger.WarnContext(ctx, "regrab_blacklist_write_failed",
						slog.String("instance", string(instanceName)),
						slog.String("error", wErr.Error()))
				} else {
					res.BlacklistedThisCycle = append(res.BlacklistedThisCycle, TripleKey{
						SeriesID:     origGrab.SeriesID,
						SeasonNumber: origGrab.SeasonNumber,
					})
					_ = u.counter.Reset(ctx, sett.InstanceID, origGrab.SeriesID, origGrab.SeasonNumber, startedAt)
					u.logger.InfoContext(ctx, "regrab_blacklisted",
						slog.String("instance", string(instanceName)),
						slog.Int("series_id", int(origGrab.SeriesID)),
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
	u.state.StampPartial(instanceName, res.FinishedAt, PollResultOK, true, watched)
	return res, nil
}

// runEvaluator is the per-triple evaluator invocation.
//
// Two paths live here:
//
//  1. UNREGISTERED short-circuit — the qBit verdict said the tracker
//     rejected the existing torrent. Before searching anew, we try
//     POSTing the same GUID + IndexerID back to Sonarr. Forum-style
//     indexers (rutracker, etc.) often update the .torrent in-place,
//     so the same topic URL returns refreshed content. On 2xx we
//     persist a synthetic Decision row stamped
//     ChosenBecauseWatchdogReplayUnregistered and return
//     OutcomeGrabbed; the caller's existing grab branch fires
//     runGrab → SetReplayOfID untouched.
//
//  2. EVALUATOR search — the default path. Resolves Series via Sonarr,
//     builds the evaluate.Input the way rescan does (D60 pattern), and
//     classifies the decision into a regrab OutcomeReason.
//
// Error classification on path 1 follows operator decision: 404/410
// → fall through to path 2 (the topic is gone, search elsewhere);
// every other error surfaces as OutcomeError so the caller activates
// the cooldown and does NOT silently search.
func (u *UseCase) runEvaluator(
	ctx context.Context,
	inst scan.Instance,
	origGrab domaingrab.Record,
	verdict qbit.DetectionResult,
) (OutcomeReason, decision.Decision, error) {
	// Path 1 — same-GUID replay. Only on the unregistered verdict and
	// only when we have both GUID + IndexerID to re-POST.
	if verdict.Unregistered && origGrab.ReleaseGUID != "" && origGrab.IndexerID > 0 {
		outcome, d, err := u.tryReplayByGUID(ctx, inst, origGrab)
		switch {
		case err == nil:
			return outcome, d, nil
		case u.releaseGoneClassifier(err):
			// 404 / 410 — topic gone from the indexer. Fall through
			// to the existing evaluator search path. Emit one INFO so
			// the transition is visible in logs without ratcheting up
			// volume.
			u.logger.InfoContext(ctx, "regrab_replay_falls_through",
				slog.String("instance", string(origGrab.InstanceName)),
				slog.String("guid", origGrab.ReleaseGUID),
				slog.Int("indexer_id", origGrab.IndexerID),
				slog.Int("series_id", int(origGrab.SeriesID)),
				slog.Int("season", origGrab.SeasonNumber),
				slog.String("reason", "release_gone_on_indexer"))
		default:
			// Real error (5xx, network, ctx-cancel, 4xx other than
			// 404/410). tryReplayByGUID has persisted a SKIP decision
			// row (when u.decisions is wired) with a non-zero ID;
			// surface the error so the caller activates the cooldown
			// but reuses the existing decision row as the audit
			// trail. When u.decisions is nil the Decision is the
			// zero value — the caller's legacy
			// `regrab_evaluate_failed` log path stays the audit
			// trail (back-compat).
			return OutcomeError, d, err
		}
	}

	// Path 2 — evaluator search. Byte-identical to the pre-114 body.
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

	intentDetail := fmt.Sprintf("Watchdog re-grab of grab_%s", origGrab.ID.String())

	runCtx := logger.WithTraceID(context.Background(), origGrab.ID.String())
	d, err := u.evaluate.Execute(runCtx, evaluate.Input{
		ScanRunID:             uuid.New(),
		Instance:              origGrab.InstanceName,
		Sonarr:                inst.Client,
		Series:                seriesRow,
		Season:                season,
		Profile:               profile,
		MinCustomFormatScore:  inst.Config.Search.MinCustomFormatScore,
		RequireAllAired:       inst.Config.Search.RequireAllAired,
		SkipSpecials:          inst.Config.Search.SkipSpecials,
		SkipAnime:             inst.Config.Search.SkipAnime,
		DryRun:                false,
		Now:                   u.now(),
		IgnoreCooldown:        false,
		PreferredDecisionID:   nil,
		IntentBecauseOverride: decision.ChosenBecauseWatchdogBetterOther,
		IntentDetailOverride:  intentDetail,
	})
	if err != nil {
		return OutcomeError, d, err
	}

	if u.decisions != nil {
		if refined, ok := refineWatchdogIntent(d, origGrab); ok {
			if uerr := u.decisions.UpdateIntent(runCtx, d.ID, refined); uerr != nil {
				u.logger.WarnContext(runCtx, "regrab_intent_refine_failed",
					slog.String("decision_id", d.ID.String()),
					slog.String("error", uerr.Error()))
			} else {
				d.Intent = refined
			}
		}
	}

	return classifyOutcome(d), d, nil
}

// tryReplayByGUID POSTs the original grab's GUID + IndexerID back to
// Sonarr. On 2xx, persists a synthetic Decision row stamped
// ChosenBecauseWatchdogReplayUnregistered and returns OutcomeGrabbed
// — the caller's existing grab branch then fires runGrab +
// SetReplayOfID as if the evaluator had picked a fresh candidate.
//
// We synthesise a `release.Scored` from the original grab record so
// `runGrab` has a Selected to feed `grab.UseCase.Execute`. The release
// only needs (GUID, IndexerID) — those are what Sonarr's downstream
// ForceGrab call inside grab.UseCase will reuse. Title / quality
// fields carry the original values for the slog audit trail; the
// actual quality of the replayed torrent comes back on the next
// OnGrab webhook and is captured on the new grab row by the webhook
// handler.
//
// Returns the raw sonarr error on non-2xx so the caller can classify
// 404/410 (fall through) vs other (surface). The error path does NOT
// persist a decision row — the fall-through path's evaluator will.
func (u *UseCase) tryReplayByGUID(
	ctx context.Context,
	inst scan.Instance,
	origGrab domaingrab.Record,
) (OutcomeReason, decision.Decision, error) {
	runCtx := logger.WithTraceID(context.Background(), origGrab.ID.String())

	u.logger.InfoContext(ctx, "regrab_replay_attempt",
		slog.String("instance", string(origGrab.InstanceName)),
		slog.String("guid", origGrab.ReleaseGUID),
		slog.Int("indexer_id", origGrab.IndexerID),
		slog.Int("series_id", int(origGrab.SeriesID)),
		slog.Int("season", origGrab.SeasonNumber),
		slog.String("original_grab_id", origGrab.ID.String()))

	// Warm Sonarr's release cache for this (series, season) so the
	// GUID is recognised by POST /api/v3/release. Sonarr's release
	// cache is per-process and cold after a Sonarr restart; without
	// the warm GET the same-GUID ForceGrab returns 404 "Couldn't
	// find requested release in cache" and the caller would
	// false-positive into release_gone_on_indexer (errors.go:115-124
	// classifies any 404/410 as gone). The warm call's result is
	// discarded — the cache population is the side effect we want;
	// the choice of GUID is already locked in by origGrab.
	warmed, warmErr := inst.Client.SearchReleases(ctx, origGrab.SeriesID, origGrab.SeasonNumber)
	if warmErr != nil {
		// Non-fatal: another path (manual UI search, prior poll, fresh
		// scan) may already have warmed the cache. Proceed to
		// ForceGrab; if the cache is still cold the existing 404
		// fall-through catches it.
		u.logger.WarnContext(ctx, "regrab_replay_warm_failed",
			slog.String("instance", string(origGrab.InstanceName)),
			slog.String("guid", origGrab.ReleaseGUID),
			slog.Int("series_id", int(origGrab.SeriesID)),
			slog.Int("season", origGrab.SeasonNumber),
			slog.String("error", warmErr.Error()))
	} else {
		u.logger.DebugContext(ctx, "regrab_replay_warmed",
			slog.String("instance", string(origGrab.InstanceName)),
			slog.Int("series_id", int(origGrab.SeriesID)),
			slog.Int("season", origGrab.SeasonNumber),
			slog.Int("releases", len(warmed)))
	}

	// Sonarr POST /api/v3/release with the same GUID. ForceGrab returns
	// downloadClientID (or "") on 2xx; we ignore the value because
	// runGrab calls ForceGrab again inside grab.UseCase.Execute on the
	// new row.
	_, forceErr := inst.Client.ForceGrab(ctx, origGrab.ReleaseGUID, origGrab.IndexerID)

	switch {
	case forceErr == nil:
		// Path A — clean 2xx. Existing success behaviour.
		d := u.buildReplayDecision(origGrab,
			decision.ChosenBecauseWatchdogReplayUnregistered,
			fmt.Sprintf(
				"Watchdog re-grab of grab_%s via same GUID (tracker said unregistered)",
				origGrab.ID.String()),
			decision.OutcomeGrab,
			decision.ReasonGrabSelected,
		)
		if u.decisions != nil {
			if err := u.decisions.Save(runCtx, d); err != nil {
				return OutcomeError, decision.Decision{}, fmt.Errorf("persist replay decision: %w", err)
			}
		}
		u.logger.InfoContext(ctx, "regrab_replay_succeeded",
			slog.String("instance", string(origGrab.InstanceName)),
			slog.String("guid", origGrab.ReleaseGUID),
			slog.Int("indexer_id", origGrab.IndexerID),
			slog.String("decision_id", d.ID.String()),
			slog.String("original_grab_id", origGrab.ID.String()))
		return OutcomeGrabbed, d, nil

	case u.releaseAlreadyAddedClassifier(forceErr):
		// Path B — Sonarr 500 wrapping qBit 409. The hash is already
		// in qBit; the replay's intent (have the file in qBit) is
		// realised. Treat as OutcomeGrabbed with a distinct Intent
		// so the operator can tell it apart in the UI.
		d := u.buildReplayDecision(origGrab,
			decision.ChosenBecauseWatchdogReplayAlreadyAdded,
			fmt.Sprintf(
				"Watchdog re-grab of grab_%s: qBit already had the hash (Sonarr 500 wrapping qBit 409)",
				origGrab.ID.String()),
			decision.OutcomeGrab,
			decision.ReasonGrabSelected,
		)
		if u.decisions != nil {
			if err := u.decisions.Save(runCtx, d); err != nil {
				return OutcomeError, decision.Decision{}, fmt.Errorf("persist replay decision: %w", err)
			}
		}
		u.logger.InfoContext(ctx, "regrab_replay_already_added",
			slog.String("instance", string(origGrab.InstanceName)),
			slog.String("guid", origGrab.ReleaseGUID),
			slog.Int("indexer_id", origGrab.IndexerID),
			slog.String("decision_id", d.ID.String()),
			slog.String("original_grab_id", origGrab.ID.String()))
		return OutcomeGrabbed, d, nil

	case u.releaseGoneClassifier(forceErr):
		// Path C — 404/410 release gone. Fall through to evaluator
		// via empty decision (caller branches on releaseGoneClassifier
		// + empty Decision.ID).
		return OutcomeError, decision.Decision{}, forceErr

	default:
		// Path D — any other error. Persist a SKIP decision row so
		// the operator has an audit trail; surface OutcomeError so
		// the caller activates cooldown + counts an error, but the
		// caller MUST detect the populated decision and skip its
		// own `regrab_evaluate_failed` log (avoid double-audit).
		// When the DecisionRepository is unwired (u.decisions == nil)
		// we cannot persist anything, so return an empty Decision and
		// let the caller fall back to the legacy WARN audit trail.
		if u.decisions == nil {
			return OutcomeError, decision.Decision{}, forceErr
		}
		d := u.buildReplayDecision(origGrab,
			decision.ChosenBecauseWatchdogReplayError,
			fmt.Sprintf(
				"Watchdog re-grab of grab_%s failed: %s",
				origGrab.ID.String(), forceErr.Error()),
			decision.OutcomeSkip,
			decision.ReasonReplayError,
		)
		d.ErrorDetail = forceErr.Error()
		if perr := u.decisions.Save(runCtx, d); perr != nil {
			// Best-effort: the decision write failed. Surface the
			// original ForceGrab error with an empty Decision so
			// the caller falls back to the legacy
			// regrab_evaluate_failed log path.
			u.logger.WarnContext(ctx, "regrab_replay_error_persist_failed",
				slog.String("instance", string(origGrab.InstanceName)),
				slog.String("error", perr.Error()))
			return OutcomeError, decision.Decision{}, forceErr
		}
		u.logger.WarnContext(ctx, "regrab_replay_error",
			slog.String("instance", string(origGrab.InstanceName)),
			slog.String("guid", origGrab.ReleaseGUID),
			slog.Int("indexer_id", origGrab.IndexerID),
			slog.String("decision_id", d.ID.String()),
			slog.String("original_grab_id", origGrab.ID.String()),
			slog.String("error", forceErr.Error()))
		return OutcomeError, d, forceErr
	}
}

// buildReplayDecision is the common synthetic-decision constructor
// shared by tryReplayByGUID's success / already-added / error branches.
// Reduces three near-identical decision.Decision literals to one helper
// + per-call (intent, outcome, reason) tuples.
func (u *UseCase) buildReplayDecision(
	origGrab domaingrab.Record,
	because decision.ChosenBecause,
	detail string,
	outcome decision.Outcome,
	reason decision.Reason,
) decision.Decision {
	synthetic := release.Scored{
		Release: release.Release{
			GUID:        origGrab.ReleaseGUID,
			Title:       origGrab.ReleaseTitle,
			IndexerID:   origGrab.IndexerID,
			IndexerName: origGrab.IndexerName,
			QualityName: origGrab.Quality,
		},
	}
	intent := decision.NewIntent(nil, nil, because, detail)
	return decision.Decision{
		ID:              uuid.New(),
		ScanRunID:       uuid.Nil, // 121b §B — persist as NULL; replay has no parent scan
		InstanceName:    origGrab.InstanceName,
		SeriesID:        origGrab.SeriesID,
		SeriesTitle:     origGrab.SeriesTitle,
		SeasonNumber:    origGrab.SeasonNumber,
		Outcome:         outcome,
		Reason:          reason,
		Selected:        &synthetic,
		DryRunWouldGrab: false,
		Intent:          &intent,
		CreatedAt:       u.now(),
	}
}

// refineWatchdogIntent inspects the just-decided Decision against
// the original grab record and reports a refined Intent payload when
// the quality axis is plainly higher. Returns (nil, false) when no
// refinement is warranted (missing data, equal quality, downgrade).
// 091a / F-P2-2.
func refineWatchdogIntent(d decision.Decision, origGrab domaingrab.Record) (*decision.Intent, bool) {
	if d.Outcome != decision.OutcomeGrab || d.Selected == nil || d.Intent == nil {
		return nil, false
	}
	newQ := d.Selected.Release.QualityName
	oldQ := origGrab.Quality
	if newQ == "" || oldQ == "" {
		return nil, false
	}
	newRank := watchdogQualityRank(newQ)
	oldRank := watchdogQualityRank(oldQ)
	if newRank <= oldRank {
		return nil, false
	}
	refined := *d.Intent
	refined.ChosenBecause = decision.ChosenBecauseWatchdogBetterQuality
	refined.ChosenReasonDetail = fmt.Sprintf("%s beats %s (watchdog re-grab of grab_%s)",
		newQ, oldQ, origGrab.ID.String())
	return &refined, true
}

// watchdogQualityRank assigns a numeric resolution rank from a Sonarr
// QualityName string. Matches the read-side derivation in
// domain/grab.qualityRank so the decide-time refine and the
// read-time replay_kind classifier agree on the ordering.
// Unknown / SD / blank → 0. 091a / F-P2-2.
func watchdogQualityRank(q string) int {
	lc := strings.ToLower(q)
	switch {
	case strings.Contains(lc, "2160p"), strings.Contains(lc, "4k"), strings.Contains(lc, "uhd"):
		return 7
	case strings.Contains(lc, "1440p"):
		return 6
	case strings.Contains(lc, "1080p"):
		return 5
	case strings.Contains(lc, "720p"):
		return 4
	case strings.Contains(lc, "576p"):
		return 3
	case strings.Contains(lc, "480p"):
		return 2
	case strings.Contains(lc, "sd"):
		return 1
	}
	return 0
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
		slog.String("instance", string(origGrab.InstanceName)),
		slog.Int("series_id", int(origGrab.SeriesID)),
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
	seriesID domain.SonarrSeriesID
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

// Snapshot returns the latest per-instance state recorded by RunInstance.
// (zero, false) when the instance has never run.
func (u *UseCase) Snapshot(instance domain.InstanceName) (RuntimeState, bool) {
	return u.state.Snapshot(instance)
}

// SnapshotAll returns every instance's state. Used by the aggregate
// rollup handler.
func (u *UseCase) SnapshotAll() map[domain.InstanceName]RuntimeState {
	return u.state.SnapshotAll()
}

// ForgetState drops the per-instance bookkeeping. Called by the
// instance CRUD delete subscriber.
func (u *UseCase) ForgetState(instance domain.InstanceName) {
	u.state.Forget(instance)
}
