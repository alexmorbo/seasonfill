// Wave 2 (W2-4) — TMDB /tv/changes poller (application layer).
//
// ChangesPoller runs ONE poll tick (plan §4.4): it walks the global
// /tv/changes firehose in chronological ≤14d windows, marks the intersection
// with our tracked series (tmdb_id IN …) via ChangedSeriesMarker, and advances
// a persisted cursor per-window. It rerefreshes NOTHING itself — the existing
// RefreshScheduler picks up marked rows via its tier-0 "changed" branch (W2-5).
// Architecture B, plan §3.2.
//
// This file is the tick body only. The lifecycle ticker (RunForever-style) plus
// env parsing and DI wiring are W2-6.
package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	enrichdomain "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// ChangesMetrics is the narrow metric port for the poller. Production impl:
// observability.TMDBChangesMetrics (plan §9.1). Tests pass a recording fake.
//
// dedup_skipped_total is intentionally NOT on this interface: §9.1/§481 marks it
// optional (it needs a second COUNT query the poller does not run) — deferred.
// changes_pending is a DB-COUNT gauge that belongs to the dashboard story W2-7
// (needs a NOT EXISTS attempts>5 reader the poller has no access to, B-05).
type ChangesMetrics interface {
	// IncPoll records a poll outcome. result ∈
	// {ok, error, skipped_no_client, skipped_inflight, cursor_reset} — exactly
	// ONE Inc per tick (mutually-exclusive label set, plan §9.1).
	IncPoll(result string)
	AddPages(n int)
	AddFirehoseIDs(n int)
	AddMatched(n int64)
	ObservePollDuration(d time.Duration)
	SetCursorLag(d time.Duration)
}

// noopChangesMetrics is the zero-value default so an unconfigured metrics port
// never panics. Mirrors noopRefreshMetrics (refresh_scheduler.go:86-92).
type noopChangesMetrics struct{}

func (noopChangesMetrics) IncPoll(string)                    {}
func (noopChangesMetrics) AddPages(int)                      {}
func (noopChangesMetrics) AddFirehoseIDs(int)                {}
func (noopChangesMetrics) AddMatched(int64)                  {}
func (noopChangesMetrics) ObservePollDuration(time.Duration) {}
func (noopChangesMetrics) SetCursorLag(time.Duration)        {}

// ChangesPollerDeps is the construction surface. Lister / Marker / CursorStore
// are required. Everything else defaults (see NewChangesPoller). The config
// fields are carried here so W2-6 can wire env→them; poll() reads the resolved
// copies stored on the struct.
type ChangesPollerDeps struct {
	Lister      TVChangesLister     // required
	Marker      ChangedSeriesMarker // required
	CursorStore ChangesCursorStore  // required

	Metrics     ChangesMetrics   // default noopChangesMetrics
	ClientReady func() bool      // default: always true. W2-6 wires tmdbHolder.Load()!=nil (ShouldSweep pattern)
	Logger      *slog.Logger     // default DomainLogger(slog.Default(), "enrichment")
	Clock       func() time.Time // default time.Now().UTC

	// Config — W2-6 parses env into these; here we only default them.
	PollInterval time.Duration // default 8h — CARRIED for W2-6's ticker; unused in poll()
	PageCap      int           // default 200 — pagination safety valve (plan §10)
	MarkBatch    int           // default 500 — IN-chunk size (plan §6)
	OverlapDays  int           // default 1 — day-boundary overlap (plan §8)
	LookbackDays int           // default 14 — API window cap / stale-cursor floor
}

// ChangesPoller is the constructed poller. poll() is reentrant-safe via inFlight
// (a slow tick that outlives POLL_INTERVAL cannot overlap the next wake, F18).
type ChangesPoller struct {
	deps     ChangesPollerDeps
	inFlight chan struct{}

	// resolved config, stored for readability in poll()
	pageCap      int
	markBatch    int
	overlapDays  int
	lookbackDays int
}

// pollResult is the tick summary — consumed by tests and by the exported Poll
// wrapper. Result carries the terminal poll_total label.
type pollResult struct {
	Result   string // one of the IncPoll labels
	Pages    int
	Firehose int
	Matched  int64
}

// NewChangesPoller validates the three required ports and applies defaults.
// Returns an error rather than panicking so the boot wirer (W2-6) can log a
// "changes poller disabled" line when a prerequisite is missing — mirrors
// NewRefreshScheduler (refresh_scheduler.go:119).
//
// OverlapDays defaults on <= 0 to 1 (house <=0 idiom). W2-6 clamps env to
// [0..7] with default 1, so this never masks a legitimate explicit value in
// production; a caller wanting an explicit 0-day overlap must set it via a
// dedicated path — out of scope here.
func NewChangesPoller(deps ChangesPollerDeps) (*ChangesPoller, error) {
	if deps.Lister == nil {
		return nil, errors.New("changes poller: Lister is required")
	}
	if deps.Marker == nil {
		return nil, errors.New("changes poller: Marker is required")
	}
	if deps.CursorStore == nil {
		return nil, errors.New("changes poller: CursorStore is required")
	}
	if deps.Metrics == nil {
		deps.Metrics = noopChangesMetrics{}
	}
	if deps.ClientReady == nil {
		deps.ClientReady = func() bool { return true }
	}
	if deps.Logger == nil {
		deps.Logger = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	if deps.PollInterval <= 0 {
		deps.PollInterval = 8 * time.Hour
	}
	if deps.PageCap <= 0 {
		deps.PageCap = 200
	}
	if deps.MarkBatch <= 0 {
		deps.MarkBatch = 500
	}
	if deps.OverlapDays <= 0 {
		deps.OverlapDays = 1
	}
	if deps.LookbackDays <= 0 {
		deps.LookbackDays = 14
	}
	return &ChangesPoller{
		deps:         deps,
		inFlight:     make(chan struct{}, 1),
		pageCap:      deps.PageCap,
		markBatch:    deps.MarkBatch,
		overlapDays:  deps.OverlapDays,
		lookbackDays: deps.LookbackDays,
	}, nil
}

// Poll is the exported tick entry point W2-6's ticker calls. It is NOT a loop —
// one call = one poll tick. Errors surface the failing-window/cursor error;
// skips and cursor resets return nil.
func (p *ChangesPoller) Poll(ctx context.Context) error {
	_, err := p.poll(ctx)
	return err
}

// poll runs ONE firehose poll tick (plan §4.4). See file header + the numbered
// steps below.
func (p *ChangesPoller) poll(ctx context.Context) (pollResult, error) {
	// (1) inFlight guard — F18. A second concurrent tick returns immediately.
	select {
	case p.inFlight <- struct{}{}:
		defer func() { <-p.inFlight }()
	default:
		p.deps.Logger.InfoContext(ctx, "tmdb.changes.poll.skipped",
			slog.String("reason", "in_flight"),
		)
		p.deps.Metrics.IncPoll("skipped_inflight")
		return pollResult{Result: "skipped_inflight"}, nil
	}

	// (2) timer.
	start := p.deps.Clock()
	defer func() { p.deps.Metrics.ObservePollDuration(p.deps.Clock().Sub(start)) }()

	// (3) no TMDB client wired → skip cleanly (F15). W2-6 wires ClientReady to
	// tmdbHolder.Load()!=nil (ShouldSweep pattern, wiring/enrichment.go:685).
	if !p.deps.ClientReady() {
		p.deps.Logger.InfoContext(ctx, "tmdb.changes.poll.skipped",
			slog.String("reason", "no_client"),
		)
		p.deps.Metrics.IncPoll("skipped_no_client")
		return pollResult{Result: "skipped_no_client"}, nil
	}

	now := p.deps.Clock()

	// (4) load cursor. ErrNotFound = first run → empty cursor.
	cursor, err := p.deps.CursorStore.Get(ctx)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			cursor = enrichdomain.ChangeCursor{}
		} else {
			p.deps.Logger.WarnContext(ctx, "tmdb.changes.cursor.get_failed",
				slog.String("error", err.Error()),
			)
			p.deps.Metrics.IncPoll("error")
			return pollResult{Result: "error"}, fmt.Errorf("changes poller: load cursor: %w", err)
		}
	}

	// (5) cursor validation (plan §8) — DELIBERATE one-Inc-per-tick design.
	// A future or >lookback-stale cursor is reset to an EMPTY cursor (LastWindowEnd
	// zeroed) and we return cursor_reset NOW; the cold-start fetch happens on the
	// NEXT tick. Rationale: keeps poll_total{result} mutually exclusive (one Inc
	// per tick); PlanWindows remains the safety net; a rare >14d/corrupt cursor
	// costs one extra interval — negligible vs the TTL-sweep fallback.
	lookbackDuration := time.Duration(p.lookbackDays) * 24 * time.Hour
	if !cursor.LastWindowEnd.IsZero() &&
		(now.Before(cursor.LastWindowEnd) || now.Sub(cursor.LastWindowEnd) > lookbackDuration) {
		reason := "stale"
		if now.Before(cursor.LastWindowEnd) {
			reason = "future"
		}
		gapDays := int(now.Sub(cursor.LastWindowEnd).Hours() / 24)
		p.deps.Logger.WarnContext(ctx, "tmdb.changes.cursor.reset",
			slog.String("reason", reason),
			slog.Int("gap_days", gapDays),
		)
		reset := enrichdomain.ChangeCursor{SchemaVersion: cursor.SchemaVersion, LastPollAt: now}
		if err := p.deps.CursorStore.Save(ctx, reset); err != nil {
			p.deps.Logger.WarnContext(ctx, "tmdb.changes.cursor.reset_save_failed",
				slog.String("error", err.Error()),
			)
			p.deps.Metrics.IncPoll("error")
			return pollResult{Result: "error"}, fmt.Errorf("changes poller: save reset cursor: %w", err)
		}
		p.deps.Metrics.IncPoll("cursor_reset")
		return pollResult{Result: "cursor_reset"}, nil
	}

	// (6) R-A01: dedupBoundary = cursor.LastWindowEnd, read ONCE before the window
	// loop; NOT w.Start. Constant for the whole poll (plan §0-G7 / ADR-0004).
	dedupBoundary := cursor.LastWindowEnd

	// (7) plan windows.
	windows := enrichdomain.PlanWindows(cursor, now, p.overlapDays, p.lookbackDays)

	cursorLagDays := 0
	if !cursor.LastWindowEnd.IsZero() {
		cursorLagDays = int(now.Sub(cursor.LastWindowEnd).Hours() / 24)
	}
	p.deps.Logger.InfoContext(ctx, "tmdb.changes.poll.start",
		slog.Int("windows", len(windows)),
		slog.Int("cursor_lag_days", cursorLagDays),
	)

	res := pollResult{}
	// (8) per-window: paginate → dedup → mark in chunks → advance cursor.
	for _, w := range windows {
		idSet := map[int64]struct{}{} // in-poll dedup between pages
		winPages := 0

		for page := 1; ; page++ {
			resp, err := p.deps.Lister.GetTVChangesPage(ctx, w.Start, w.End, page)
			if err != nil {
				p.deps.Logger.WarnContext(ctx, "tmdb.changes.window.failed",
					slog.Time("start", w.Start),
					slog.Time("end", w.End),
					slog.Int("page", page),
					slog.String("error", err.Error()),
				)
				p.deps.Metrics.IncPoll("error")
				res.Result = "error"
				// Cursor NOT advanced for this window; prior windows already Saved stay.
				return res, fmt.Errorf("changes poller: fetch window [%s..%s] page %d: %w",
					w.Start.Format("2006-01-02"), w.End.Format("2006-01-02"), page, err)
			}
			winPages++
			p.deps.Metrics.AddPages(1)
			res.Pages++
			for _, id := range resp.IDs {
				idSet[id] = struct{}{}
			}
			if page >= resp.TotalPages || page >= p.pageCap {
				if page >= p.pageCap && page < resp.TotalPages {
					// pageCap hit before TotalPages: window still SUCCEEDS; the
					// truncated tail is re-covered by the next day's overlap.
					p.deps.Logger.WarnContext(ctx, "tmdb.changes.page_cap",
						slog.Int("cap", p.pageCap),
						slog.Time("start", w.Start),
						slog.Time("end", w.End),
					)
				}
				break
			}
		}

		firehose := len(idSet)
		p.deps.Metrics.AddFirehoseIDs(firehose)
		res.Firehose += firehose

		// stable, sorted ids for deterministic chunking / test assertions.
		ids := make([]int64, 0, firehose)
		for id := range idSet {
			ids = append(ids, id)
		}
		slices.Sort(ids)

		var winMatched int64
		for _, chunk := range chunkIDs(ids, p.markBatch) {
			// R-A01: dedupBoundary is the constant read in (6), NOT w.Start.
			n, err := p.deps.Marker.MarkChangedByTMDBIDs(ctx, chunk, now, dedupBoundary)
			if err != nil {
				p.deps.Logger.WarnContext(ctx, "tmdb.changes.window.failed",
					slog.Time("start", w.Start),
					slog.Time("end", w.End),
					slog.Int("marked_ids", len(chunk)),
					slog.String("error", err.Error()),
				)
				p.deps.Metrics.IncPoll("error")
				res.Result = "error"
				// Cursor NOT advanced; already-marked chunks stay (idempotent, F3).
				return res, fmt.Errorf("changes poller: mark window [%s..%s]: %w",
					w.Start.Format("2006-01-02"), w.End.Format("2006-01-02"), err)
			}
			winMatched += n
			p.deps.Metrics.AddMatched(n)
			res.Matched += n
		}

		// advance cursor PER-WINDOW (plan §4.4).
		saved := enrichdomain.ChangeCursor{
			SchemaVersion: cursor.SchemaVersion,
			LastWindowEnd: w.End,
			LastPollAt:    now,
			LastMatched:   int(winMatched),
			LastFirehose:  firehose,
		}
		if err := p.deps.CursorStore.Save(ctx, saved); err != nil {
			p.deps.Logger.WarnContext(ctx, "tmdb.changes.cursor.save_failed",
				slog.Time("end", w.End),
				slog.String("error", err.Error()),
			)
			p.deps.Metrics.IncPoll("error")
			res.Result = "error"
			return res, fmt.Errorf("changes poller: save cursor at %s: %w",
				w.End.Format("2006-01-02"), err)
		}
		p.deps.Logger.InfoContext(ctx, "tmdb.changes.window.done",
			slog.Time("start", w.Start),
			slog.Time("end", w.End),
			slog.Int("pages", winPages),
			slog.Int("firehose", firehose),
			slog.Int64("matched", winMatched),
		)
	}

	// (9) success.
	var finalLag time.Duration
	if len(windows) > 0 {
		finalLag = now.Sub(windows[len(windows)-1].End)
	}
	p.deps.Metrics.SetCursorLag(finalLag)
	p.deps.Metrics.IncPoll("ok")
	res.Result = "ok"
	p.deps.Logger.InfoContext(ctx, "tmdb.changes.poll.done",
		slog.Int64("duration_ms", p.deps.Clock().Sub(start).Milliseconds()),
		slog.Int64("total_matched", res.Matched),
	)
	return res, nil
}

// chunkIDs splits ids into batches of at most size. size <= 0 → one chunk.
// Empty input → nil (no Mark call — the valid empty-firehose path F1).
func chunkIDs(ids []int64, size int) [][]int64 {
	if size <= 0 {
		if len(ids) == 0 {
			return nil
		}
		return [][]int64{ids}
	}
	var out [][]int64
	for i := 0; i < len(ids); i += size {
		end := min(i+size, len(ids))
		out = append(out, ids[i:end])
	}
	return out
}
