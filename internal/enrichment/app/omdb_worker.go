// Package enrichment — Story 213 OMDb worker.
//
// OMDbWorker is the EntityOMDb handler. Workflow per PRD §5.5:
//  1. Load series — bail if no imdb_id.
//  2. Read canon.EnrichmentOMDBSyncedAt; skip if within TTL.
//  3. Reserve a slot from Budget; skip (no error, no journal) if
//     counter is zero.
//  4. omdb.GetByIMDB → omdb.Map → enrichment.OMDbEnrichment.
//  5. ONE tx: upsert series with ONLY the four OMDb-owned columns
//     patched onto the existing canon row.
//  6. Stamp canon.enrichment_omdb_synced_at via MarkOMDBSynced +
//     clear any outstanding enrichment_errors row.
//
// Sentinel errors from the client map to enrichment_errors writes:
//   - omdb.ErrNotFound   → attempts=terminalAttempts (no retry)
//   - omdb.ErrInvalidKey → attempts++ (retryable; operator action req'd)
//   - omdb.ErrDailyLimit → attempts++ (retryable; daily reset clears)
//   - everything else    → attempts++ + NextAttemptAt backoff

package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/omdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// OMDbClient is the substitution seam for tests. Production impl is
// *omdb.Client. The constructor side accepts a getter (func() OMDbClient)
// so the wiring layer can swap the underlying client on S-2 reload
// without rebuilding the worker.
type OMDbClient interface {
	GetByIMDB(ctx context.Context, imdbID domain.IMDBID) (*omdb.Response, error)
}

// OMDbBudget is the budget-guard surface the worker calls into.
// Production impl is *OMDbBudgetGuard. Tests pass a fake.
//
// W18-9: two lanes over one counter + a Hot floor. Hot (user on-view)
// spends down to 0; Cold (background/enqueue) backs off once remaining
// hits the floor so on-view is never starved by batch/discovery.
type OMDbBudget interface {
	// ReserveHot consumes one slot when any remain (down to 0). Used by
	// the on-view /ratings path. False ⇒ counter==0 (skip, no journal).
	ReserveHot() bool
	// ReserveCold consumes one slot only while remaining stays ABOVE the
	// Hot floor; false ⇒ floor reached (skip, no journal, no decrement).
	// Used by all dispatcher-driven work (daily batch, discovery).
	ReserveCold() bool
	// ColdAvailable reports whether a Cold reservation would currently
	// succeed (remaining ABOVE the Hot floor) WITHOUT consuming a slot.
	// W18-8's imdb_id-gain enqueue uses it as a non-consuming pre-check so
	// a floor-exhausted budget doesn't flood the dispatcher queue with jobs
	// ReserveCold would immediately deny. Advisory only — the real spend
	// still goes through ReserveCold in the worker (no double-spend).
	ColdAvailable() bool
	// Remaining returns the current counter for logging + metric.
	Remaining() int
}

// OMDbWorkerDeps is the dependency surface. Mirrors PersonWorkerDeps
// in style — every field is named, optional fields default in the
// constructor.
type OMDbWorkerDeps struct {
	Client           func() OMDbClient // getter pattern; rebuilt on S-2 reload
	Budget           OMDbBudget
	Tx               Transactor
	Series           SeriesRepo
	EnrichmentErrors EnrichmentErrorRepo
	Logger           *slog.Logger
	Clock            func() time.Time
}

// OMDbWorker is the bound worker. Construct via NewOMDbWorker.
type OMDbWorker struct {
	deps OMDbWorkerDeps
}

// NewOMDbWorker validates every required dependency.
func NewOMDbWorker(deps OMDbWorkerDeps) (*OMDbWorker, error) {
	if deps.Client == nil {
		return nil, errors.New("enrichment.omdb_worker: Client getter required")
	}
	if deps.Budget == nil {
		return nil, errors.New("enrichment.omdb_worker: Budget required")
	}
	if deps.Tx == nil {
		return nil, errors.New("enrichment.omdb_worker: Transactor required")
	}
	if deps.Series == nil || deps.EnrichmentErrors == nil {
		return nil, errors.New("enrichment.omdb_worker: every repository port is required")
	}
	if deps.Logger == nil {
		deps.Logger = sharedports.DomainLogger(slog.Default(), "omdb")
	}
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &OMDbWorker{deps: deps}, nil
}

// quotaLane selects which budget reservation an OMDb fetch draws from.
type quotaLane int

const (
	laneCold quotaLane = iota // background/enqueue-driven — backs off at the Hot floor
	laneHot                   // user on-view — spends into the floor
)

func (l quotaLane) String() string {
	if l == laneHot {
		return "hot"
	}
	return "cold"
}

// HandleHot is the on-view (user-initiated) entry point. Reserves from
// the Hot lane (allowed down to 0). Satisfies seriesdetail.OMDbRatingRefresher.
func (w *OMDbWorker) HandleHot(ctx context.Context, seriesID domain.SeriesID) error {
	return w.handle(ctx, seriesID, laneHot)
}

// HandleCold is the background/enqueue-driven entry point (daily batch,
// discovery W18-8). Reserves from the Cold lane (backs off at the Hot
// floor). Cold is the natural default for all dispatcher-driven work.
func (w *OMDbWorker) HandleCold(ctx context.Context, seriesID domain.SeriesID) error {
	return w.handle(ctx, seriesID, laneCold)
}

// handle is the shared worker body. seriesID is a CANON series.id. lane
// selects the budget reservation. Returns nil on every terminal outcome
// (ok / not_found / auth_failed / retryable journalled / budget skip).
func (w *OMDbWorker) handle(ctx context.Context, seriesID domain.SeriesID, lane quotaLane) error {
	start := w.deps.Clock()
	log := w.deps.Logger.With(
		slog.String("entity_type", string(enrichment.EntityTypeSeries)),
		slog.Int64("entity_id", int64(seriesID)),
		slog.String("source", string(enrichment.SourceOMDb)),
		slog.String("lane", lane.String()),
	)

	// 1. Load series — need imdb_id.
	canon, err := w.deps.Series.Get(ctx, seriesID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			log.WarnContext(ctx, "enrichment.omdb.handle.series_missing")
			return nil
		}
		return fmt.Errorf("omdb worker: load series: %w", err)
	}
	if canon.IMDBID == nil || *canon.IMDBID == "" {
		w.journalNotFound(ctx, seriesID, "no imdb_id on canon", start)
		return nil
	}
	imdbID := *canon.IMDBID
	log = log.With(slog.String("imdb_id", string(imdbID)))

	// 2. Staleness short-circuit — read canon.EnrichmentOMDBSyncedAt
	//    directly. nil = never enriched (proceed); within the age-based
	//    TTL (W18-5 curve B.1) = skip. The Kind is classified from the
	//    canon's lifecycle + air-date age — mirrors the SQL age-CASE in
	//    ListLibraryWithIMDBStale so batch selection and in-band guard
	//    agree on freshness.
	if canon.EnrichmentOMDBSyncedAt != nil {
		kind := classifyOMDbKind(canon, w.deps.Clock())
		ttl := enrichment.TTL(enrichment.SourceOMDb, kind)
		if ttl > 0 && w.deps.Clock().Sub(*canon.EnrichmentOMDBSyncedAt) < ttl {
			log.DebugContext(ctx, "enrichment.omdb.handle.fresh_skip",
				slog.String("ttl_kind", string(kind)),
				slog.Duration("ttl", ttl),
				slog.Time("synced_at", *canon.EnrichmentOMDBSyncedAt))
			return nil
		}
	}

	// Load any current error row (for attempts counter on retry path).
	prevAttempts := 0
	terminalRow := false
	if errRow, errErr := w.deps.EnrichmentErrors.GetByEntitySource(ctx,
		enrichment.EntityTypeSeries, int64(seriesID), enrichment.SourceOMDb); errErr == nil {
		prevAttempts = errRow.Attempts
		// not_found terminal — only a manual /refresh requeues (and that
		// clears the row first). The dispatcher's nightly batch's WHERE
		// excludes attempts>5 rows; a manual enqueue can still reach us.
		if errRow.Attempts >= terminalAttempts {
			terminalRow = true
		}
	} else if !errors.Is(errErr, ports.ErrNotFound) {
		log.WarnContext(ctx, "enrichment.omdb.handle.error_row_read_failed",
			slog.String("error", errErr.Error()))
	}
	if terminalRow {
		log.DebugContext(ctx, "enrichment.omdb.handle.terminal_not_found_skip")
		return nil
	}

	// 3. Budget guard — lane-aware. Hot spends into the floor; Cold backs
	//    off at the floor. false ⇒ skip (no error, no journal). Each lane
	//    draws exactly one reservation (no double-spend).
	var reserved bool
	if lane == laneCold {
		reserved = w.deps.Budget.ReserveCold()
	} else {
		reserved = w.deps.Budget.ReserveHot()
	}
	if !reserved {
		log.InfoContext(ctx, "enrichment.omdb.handle.budget_exhausted_skip",
			slog.Int("quota_remaining", w.deps.Budget.Remaining()))
		return nil
	}

	// 4. Fetch.
	client := w.deps.Client()
	if client == nil {
		// Reload race — client momentarily unavailable. Journal as
		// retryable so the next sweep tries again.
		return w.handleClientError(ctx, seriesID, "client_nil", errors.New("omdb client unavailable"), prevAttempts, start)
	}
	resp, err := client.GetByIMDB(ctx, imdbID)
	if err != nil {
		return w.handleClientError(ctx, seriesID, "GetByIMDB", err, prevAttempts, start)
	}

	// 5. Map outside the tx.
	mapped := omdb.Map(resp)

	// 6. ONE tx: plain-assign the four OMDb-owned columns onto the canon row.
	//    UpdateOMDbColumns writes NULL for any nil pointer (unlike the COALESCE
	//    Upsert path), so an OMDb "N/A" response actively CLEARS a previously-
	//    stored rating — M-1 fix. The worker always supplies all four values
	//    (mapper yields value-or-nil per field), so the blanket assign is safe.
	var votes *int
	if mapped.IMDBVotes != nil {
		v := int(*mapped.IMDBVotes)
		votes = &v
	}
	err = w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		return w.deps.Series.UpdateOMDbColumns(txCtx, seriesID,
			mapped.IMDBRating, votes, mapped.OMDbRated, mapped.OMDbAwards)
	})
	if err != nil {
		return w.handleClientError(ctx, seriesID, "tx", err, prevAttempts, start)
	}

	// 7. Journal success — stamp canon column + clear any pending error row.
	now := w.deps.Clock()
	durMs := int(now.Sub(start).Milliseconds())
	w.journalOK(ctx, seriesID, now)

	log.InfoContext(ctx, "enrichment.omdb.handle.ok",
		slog.Int("duration_ms", durMs),
		slog.Int("quota_remaining", w.deps.Budget.Remaining()),
	)
	return nil
}

// classifyOMDbKind picks the progressive OMDb TTL bucket (W18-5
// curve B.1) from the canon's lifecycle + air-date age. It MUST
// stay byte-for-byte equivalent to the SQL age-CASE in
// SeriesRepository.ListLibraryWithIMDBStale so the daily batch's
// set-selection and this in-band guard never disagree:
//
//	in_production OR continuing status → InProduction (2d)
//	both dates NULL                    → Mid (30d, unknown-age)
//	last_air (fallback first_air) < 1y → Recent (7d)
//	1y–3y                              → Mid (30d)
//	3y–8y                              → Old (90d)
//	> 8y                               → Ancient (180d)
//
// Boundaries are strict (`After`): a series exactly 1y old is NOT
// "< 1y" and falls into the older tier — same as SQL `>`.
// continuing status-list mirrors classifyKind (series_worker.go).
func classifyOMDbKind(c series.Canon, now time.Time) enrichment.Kind {
	if c.InProduction {
		return enrichment.KindOMDbInProduction
	}
	if c.Status != nil {
		switch *c.Status {
		case "Returning Series", "In Production", "Pilot", "Planned", "continuing":
			return enrichment.KindOMDbInProduction
		}
	}
	metric := c.LastAirDate
	if metric == nil {
		metric = c.FirstAirDate
	}
	if metric == nil {
		return enrichment.KindOMDbMid // unknown age → conservative 30d
	}
	switch {
	case metric.After(now.AddDate(-1, 0, 0)):
		return enrichment.KindOMDbRecent
	case metric.After(now.AddDate(-3, 0, 0)):
		return enrichment.KindOMDbMid
	case metric.After(now.AddDate(-8, 0, 0)):
		return enrichment.KindOMDbOld
	default:
		return enrichment.KindOMDbAncient
	}
}

// ---- error handling + journal helpers ------------------------------

func (w *OMDbWorker) handleClientError(ctx context.Context, seriesID domain.SeriesID, op string, err error, previousAttempts int, start time.Time) error {
	now := w.deps.Clock()
	durMs := int(now.Sub(start).Milliseconds())
	log := w.deps.Logger.With(
		slog.String("entity_type", string(enrichment.EntityTypeSeries)),
		slog.Int64("entity_id", int64(seriesID)),
		slog.String("source", string(enrichment.SourceOMDb)),
		slog.String("op", op),
	)

	// Sentinel: not_found — terminal, no retry.
	if errors.Is(err, omdb.ErrNotFound) {
		w.recordOMDbError(ctx, seriesID, err, terminalAttempts, nil, log)
		log.InfoContext(ctx, "enrichment.omdb.handle.not_found",
			slog.Int("duration_ms", durMs),
		)
		return nil
	}
	// Sentinel: auth_failed (invalid key OR daily limit upstream).
	if errors.Is(err, omdb.ErrInvalidKey) || errors.Is(err, omdb.ErrDailyLimit) {
		attempts := previousAttempts + 1
		w.recordOMDbError(ctx, seriesID, err, attempts, nil, log)
		log.WarnContext(ctx, "enrichment.omdb.handle.auth_failed",
			slog.String("error", err.Error()),
			slog.Int("duration_ms", durMs),
		)
		return nil
	}

	// Retryable — generic error path.
	attempts := previousAttempts + 1
	next := enrichment.NextAttemptAt(attempts, now)
	w.recordOMDbError(ctx, seriesID, err, attempts, &next, log)
	log.WarnContext(ctx, "enrichment.omdb.handle.failed",
		slog.Int("attempts", attempts),
		slog.Time("next_attempt_at", next),
		slog.Int("duration_ms", durMs),
		slog.String("error", err.Error()),
	)
	return nil
}

// recordOMDbError writes the (series, omdb) enrichment_errors row.
func (w *OMDbWorker) recordOMDbError(
	ctx context.Context,
	seriesID domain.SeriesID,
	cause error,
	attempts int,
	nextAttemptAt *time.Time,
	log *slog.Logger,
) {
	now := w.deps.Clock()
	rec := enrichment.EnrichmentError{
		EntityType:    enrichment.EntityTypeSeries,
		EntityID:      int64(seriesID),
		Source:        enrichment.SourceOMDb,
		LastError:     cause.Error(),
		Attempts:      attempts,
		LastSeenAt:    now,
		NextAttemptAt: nextAttemptAt,
	}
	if err := w.deps.EnrichmentErrors.RecordFailure(ctx, rec); err != nil {
		log.WarnContext(ctx, "enrichment.omdb.handle.record_failure_failed",
			slog.String("error", err.Error()))
	}
}

// journalOK stamps series.enrichment_omdb_synced_at = now and clears
// any outstanding enrichment_errors row for (series, omdb).
func (w *OMDbWorker) journalOK(ctx context.Context, seriesID domain.SeriesID, now time.Time) {
	if err := w.deps.Series.MarkOMDBSynced(ctx, seriesID, now); err != nil {
		w.deps.Logger.WarnContext(ctx, "enrichment.omdb.handle.mark_synced_failed",
			slog.Int64("entity_id", int64(seriesID)),
			slog.String("error", err.Error()))
	}
	if err := w.deps.EnrichmentErrors.ClearOnSuccess(ctx,
		enrichment.EntityTypeSeries, int64(seriesID), enrichment.SourceOMDb); err != nil {
		w.deps.Logger.WarnContext(ctx, "enrichment.omdb.handle.clear_error_failed",
			slog.Int64("entity_id", int64(seriesID)),
			slog.String("error", err.Error()))
	}
}

func (w *OMDbWorker) journalNotFound(ctx context.Context, seriesID domain.SeriesID, msg string, start time.Time) {
	now := w.deps.Clock()
	durMs := int(now.Sub(start).Milliseconds())
	log := w.deps.Logger.With(
		slog.String("entity_type", string(enrichment.EntityTypeSeries)),
		slog.Int64("entity_id", int64(seriesID)),
		slog.String("source", string(enrichment.SourceOMDb)),
	)
	w.recordOMDbError(ctx, seriesID, errors.New(msg), terminalAttempts, nil, log)
	log.InfoContext(ctx, "enrichment.omdb.handle.not_found",
		slog.String("reason", msg),
		slog.Int("duration_ms", durMs),
	)
}
