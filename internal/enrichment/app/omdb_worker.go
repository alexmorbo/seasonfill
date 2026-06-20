// Package enrichment — Story 213 OMDb worker.
//
// OMDbWorker is the EntityOMDb handler. Workflow per PRD §5.5:
//  1. Load series — bail if no imdb_id.
//  2. Read last sync_log(omdb); skip if outcome=ok AND fresh.
//  3. Reserve a slot from Budget; skip (no error, no journal) if
//     counter is zero.
//  4. omdb.GetByIMDB → omdb.Map → enrichment.OMDbEnrichment.
//  5. ONE tx: upsert series with ONLY the four OMDb-owned columns
//     patched onto the existing canon row.
//  6. Journal sync_log (outcome=ok, attempts=0).
//
// Sentinel errors from the client map to outcomes:
//   - omdb.ErrNotFound   → outcome=not_found (terminal)
//   - omdb.ErrInvalidKey → outcome=auth_failed
//   - omdb.ErrDailyLimit → outcome=auth_failed
//   - everything else    → outcome=error + NextAttemptAt backoff

package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/omdb"
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
// Production impl is *OMDbBudgetGuard. Tests can pass a fake
// that always-reserves / always-denies.
type OMDbBudget interface {
	// Reserve atomically decrements the in-process counter when
	// available, returns true on success. False ⇒ counter==0 (the
	// worker logs + skips, no journal write).
	Reserve() bool
	// Remaining returns the current counter for logging + metric.
	Remaining() int
}

// OMDbWorkerDeps is the dependency surface. Mirrors PersonWorkerDeps
// in style — every field is named, optional fields default in the
// constructor.
type OMDbWorkerDeps struct {
	Client  func() OMDbClient // getter pattern; rebuilt on S-2 reload
	Budget  OMDbBudget
	Tx      Transactor
	Series  SeriesRepo
	SyncLog SyncLogRepo
	Logger  *slog.Logger
	Clock   func() time.Time
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
	if deps.Series == nil || deps.SyncLog == nil {
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

// Handle is the dispatcher-facing entry point. seriesID is a CANON
// series.id. Returns nil on every terminal outcome (ok / not_found /
// auth_failed / retryable error journalled / budget exhaustion).
func (w *OMDbWorker) Handle(ctx context.Context, seriesID domain.SeriesID) error {
	start := w.deps.Clock()
	log := w.deps.Logger.With(
		slog.String("entity_type", string(enrichment.EntityTypeSeries)),
		slog.Int64("entity_id", int64(seriesID)),
		slog.String("source", string(enrichment.SourceOMDb)),
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

	// 2. Staleness short-circuit: outcome=ok + within TTL ⇒ skip.
	last, err := w.deps.SyncLog.GetLastSync(ctx, enrichment.EntityTypeSeries, int64(seriesID), enrichment.SourceOMDb)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		log.WarnContext(ctx, "enrichment.omdb.handle.sync_log_read_failed",
			slog.String("error", err.Error()))
	}
	if last.Outcome == enrichment.OutcomeOK && last.SyncedAt != nil {
		ttl := enrichment.TTL(enrichment.SourceOMDb, enrichment.KindOMDb)
		if ttl > 0 && w.deps.Clock().Sub(*last.SyncedAt) < ttl {
			log.DebugContext(ctx, "enrichment.omdb.handle.fresh_skip",
				slog.Time("synced_at", *last.SyncedAt))
			return nil
		}
	}
	// not_found is terminal — only a manual /refresh requeues. The
	// nightly batch's SQL excludes not_found rows, but a manual
	// enqueue can still reach us.
	if last.Outcome == enrichment.OutcomeNotFound {
		log.DebugContext(ctx, "enrichment.omdb.handle.terminal_not_found_skip")
		return nil
	}

	// 3. Budget guard — counter==0 ⇒ skip (no error, no journal).
	if !w.deps.Budget.Reserve() {
		log.InfoContext(ctx, "enrichment.omdb.handle.budget_exhausted_skip",
			slog.Int("quota_remaining", w.deps.Budget.Remaining()))
		return nil
	}

	// 4. Fetch.
	client := w.deps.Client()
	if client == nil {
		// Reload race — client momentarily unavailable. Journal as
		// retryable so the next sweep tries again.
		return w.handleClientError(ctx, seriesID, "client_nil", errors.New("omdb client unavailable"), last.Attempts, start)
	}
	resp, err := client.GetByIMDB(ctx, imdbID)
	if err != nil {
		return w.handleClientError(ctx, seriesID, "GetByIMDB", err, last.Attempts, start)
	}

	// 5. Map outside the tx.
	mapped := omdb.Map(resp)

	// 6. ONE tx: patch the four OMDb columns onto the canon row.
	err = w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		patched := applyOMDbToCanon(canon, mapped)
		_, err := w.deps.Series.Upsert(txCtx, patched)
		return err
	})
	if err != nil {
		return w.handleClientError(ctx, seriesID, "tx", err, last.Attempts, start)
	}

	// 7. Journal success.
	now := w.deps.Clock()
	durMs := int(now.Sub(start).Milliseconds())
	w.journalOK(ctx, seriesID, now, durMs)

	log.InfoContext(ctx, "enrichment.omdb.handle.ok",
		slog.String("outcome", string(enrichment.OutcomeOK)),
		slog.Int("duration_ms", durMs),
		slog.Int("quota_remaining", w.deps.Budget.Remaining()),
	)
	return nil
}

// applyOMDbToCanon patches ONLY the four OMDb-owned columns onto the
// existing canon row. Every other field on `base` carries through —
// the merge policy (§5.4) guarantees OMDb never overrides Sonarr or
// TMDB-owned fields. We pre-clear the four fields so a None response
// (mapper returns Enrichment{}) translates to four NULL writes,
// matching the "N/A" → NULL contract for in-place updates.
func applyOMDbToCanon(base series.Canon, m omdb.Enrichment) series.Canon {
	base.IMDBRating = m.IMDBRating
	if m.IMDBVotes != nil {
		v := int(*m.IMDBVotes)
		base.IMDBVotes = &v
	} else {
		base.IMDBVotes = nil
	}
	base.OMDBRated = m.OMDbRated
	base.OMDBAwards = m.OMDbAwards
	return base
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

	// Sentinel: not_found.
	if errors.Is(err, omdb.ErrNotFound) {
		ed := err.Error()
		entry := enrichment.SyncLog{
			EntityType:  enrichment.EntityTypeSeries,
			EntityID:    int64(seriesID),
			Source:      enrichment.SourceOMDb,
			Outcome:     enrichment.OutcomeNotFound,
			ErrorDetail: &ed,
			Attempts:    previousAttempts + 1,
			DurationMs:  &durMs,
		}
		if jerr := w.deps.SyncLog.Upsert(ctx, entry); jerr != nil {
			log.WarnContext(ctx, "enrichment.omdb.handle.journal_failed",
				slog.String("outcome", "not_found"),
				slog.String("error", jerr.Error()))
		}
		log.InfoContext(ctx, "enrichment.omdb.handle.not_found",
			slog.String("outcome", string(enrichment.OutcomeNotFound)),
			slog.Int("duration_ms", durMs),
		)
		return nil
	}
	// Sentinel: auth_failed (invalid key OR daily limit upstream).
	if errors.Is(err, omdb.ErrInvalidKey) || errors.Is(err, omdb.ErrDailyLimit) {
		ed := err.Error()
		entry := enrichment.SyncLog{
			EntityType:  enrichment.EntityTypeSeries,
			EntityID:    int64(seriesID),
			Source:      enrichment.SourceOMDb,
			Outcome:     enrichment.OutcomeError, // domain has no auth_failed enum; we mark error + ed
			ErrorDetail: &ed,
			Attempts:    previousAttempts + 1,
			DurationMs:  &durMs,
		}
		if jerr := w.deps.SyncLog.Upsert(ctx, entry); jerr != nil {
			log.WarnContext(ctx, "enrichment.omdb.handle.journal_failed",
				slog.String("outcome", "auth_failed"),
				slog.String("error", jerr.Error()))
		}
		log.WarnContext(ctx, "enrichment.omdb.handle.auth_failed",
			slog.String("error", err.Error()),
			slog.Int("duration_ms", durMs),
		)
		return nil
	}

	// Retryable — generic error path.
	attempts := previousAttempts + 1
	next := enrichment.NextAttemptAt(attempts, now)
	ed := err.Error()
	entry := enrichment.SyncLog{
		EntityType:    enrichment.EntityTypeSeries,
		EntityID:      int64(seriesID),
		Source:        enrichment.SourceOMDb,
		Outcome:       enrichment.OutcomeError,
		ErrorDetail:   &ed,
		Attempts:      attempts,
		NextAttemptAt: &next,
		DurationMs:    &durMs,
	}
	if jerr := w.deps.SyncLog.Upsert(ctx, entry); jerr != nil {
		log.WarnContext(ctx, "enrichment.omdb.handle.journal_failed",
			slog.String("outcome", "error"),
			slog.String("error", jerr.Error()))
	}
	log.WarnContext(ctx, "enrichment.omdb.handle.failed",
		slog.String("outcome", string(enrichment.OutcomeError)),
		slog.Int("attempts", attempts),
		slog.Time("next_attempt_at", next),
		slog.Int("duration_ms", durMs),
		slog.String("error", err.Error()),
	)
	return nil
}

func (w *OMDbWorker) journalOK(ctx context.Context, seriesID domain.SeriesID, now time.Time, durMs int) {
	entry := enrichment.SyncLog{
		EntityType: enrichment.EntityTypeSeries,
		EntityID:   int64(seriesID),
		Source:     enrichment.SourceOMDb,
		SyncedAt:   &now,
		Outcome:    enrichment.OutcomeOK,
		Attempts:   0,
		DurationMs: &durMs,
	}
	if err := w.deps.SyncLog.Upsert(ctx, entry); err != nil {
		w.deps.Logger.WarnContext(ctx, "enrichment.omdb.handle.journal_ok_failed",
			slog.Int64("entity_id", int64(seriesID)),
			slog.String("error", err.Error()))
	}
}

func (w *OMDbWorker) journalNotFound(ctx context.Context, seriesID domain.SeriesID, msg string, start time.Time) {
	now := w.deps.Clock()
	durMs := int(now.Sub(start).Milliseconds())
	ed := msg
	entry := enrichment.SyncLog{
		EntityType:  enrichment.EntityTypeSeries,
		EntityID:    int64(seriesID),
		Source:      enrichment.SourceOMDb,
		Outcome:     enrichment.OutcomeNotFound,
		ErrorDetail: &ed,
		Attempts:    1,
		DurationMs:  &durMs,
	}
	if err := w.deps.SyncLog.Upsert(ctx, entry); err != nil {
		w.deps.Logger.WarnContext(ctx, "enrichment.omdb.handle.journal_nf_failed",
			slog.Int64("entity_id", int64(seriesID)),
			slog.String("error", err.Error()))
	}
}
