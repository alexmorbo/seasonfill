package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// Transactor is the narrow tx port the producers need to make the
// notified_events marker + the outbox row atomic. Any concrete Transactor
// (scanBundle.Txr, etc.) satisfies it structurally.
type Transactor interface {
	Transaction(ctx context.Context, fn func(ctx context.Context) error) error
}

// CalendarEvent is the slim projection the producers consume from the S2
// calendar usecase. The wiring adapter flattens calendar.Report → []CalendarEvent.
type CalendarEvent struct {
	SeriesID  int64
	Title     string
	Season    int
	Episode   int
	AirDate   time.Time
	Milestone string // "premiere" | "finale" | "return" | ""
}

// CalendarPort is the reused S2 calendar read seam (internal/catalog/app/calendar).
// Production: a wiring adapter over *calendar.UseCase. Scope is library|followed|all;
// onlyPremieres restricts to season-premiere milestones at the repo layer.
type CalendarPort interface {
	Upcoming(ctx context.Context, from, to time.Time, scope string, onlyPremieres bool) ([]CalendarEvent, error)
}

func startOfUTCDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// -----------------------------------------------------------------------------
// season.premiere — daily followed-series premiere scan.
// -----------------------------------------------------------------------------

// PremiereProducer emits one season.premiere outbox row per (followed series,
// season) whose season-premiere episode airs in [startOfToday, +48h). The
// notified_events marker (key "<series_id>:<season>") makes it fire exactly
// once ever, across the overlapping daily windows.
type PremiereProducer struct {
	cal    CalendarPort
	outbox ports.OutboxEmitter
	marks  ports.NotifiedEventsRepository
	tx     Transactor
	clock  func() time.Time
	logger *slog.Logger
}

func NewPremiereProducer(cal CalendarPort, outbox ports.OutboxEmitter, marks ports.NotifiedEventsRepository, tx Transactor, logger *slog.Logger) *PremiereProducer {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "notification")
	}
	return &PremiereProducer{
		cal: cal, outbox: outbox, marks: marks, tx: tx,
		clock:  func() time.Time { return time.Now().UTC() },
		logger: logger,
	}
}

// WithClock swaps the clock for deterministic tests.
func (p *PremiereProducer) WithClock(c func() time.Time) *PremiereProducer { p.clock = c; return p }

// Run is the daily cron entrypoint (registered "notify-premiere-scan", "0 8 * * *").
func (p *PremiereProducer) Run(ctx context.Context) {
	now := p.clock()
	from := startOfUTCDay(now)
	to := from.Add(48 * time.Hour) // today + tomorrow; dedup collapses the overlap.

	events, err := p.cal.Upcoming(ctx, from, to, "followed", true)
	if err != nil {
		p.logger.WarnContext(ctx, "notify.premiere.scan_failed", slog.String("error", err.Error()))
		return
	}
	enqueued := 0
	for _, e := range events {
		if e.Milestone != "premiere" {
			continue
		}
		key := fmt.Sprintf("%d:%d", e.SeriesID, e.Season)
		fired, err := p.emit(ctx, key, e)
		if err != nil {
			p.logger.WarnContext(ctx, "notify.premiere.emit_failed",
				slog.String("entity_key", key), slog.String("error", err.Error()))
			continue
		}
		if fired {
			enqueued++
		}
	}
	p.logger.InfoContext(ctx, "notify.premiere.scan_ok",
		slog.Int("candidates", len(events)), slog.Int("enqueued", enqueued))
}

func (p *PremiereProducer) emit(ctx context.Context, key string, e CalendarEvent) (bool, error) {
	payload, _ := json.Marshal(map[string]any{
		"series_id":    e.SeriesID,
		"series_title": e.Title,
		"season":       e.Season,
		"air_date":     e.AirDate.UTC().Format("2006-01-02"),
	})
	fired := false
	work := func(txCtx context.Context) error {
		created, err := p.marks.MarkIfNew(txCtx, "season.premiere", key, p.clock())
		if err != nil {
			return err
		}
		if !created {
			return nil // already announced on a prior scan
		}
		if err := p.outbox.Insert(txCtx, ports.OutboxRow{EventType: "season.premiere", Payload: payload}); err != nil {
			return err
		}
		fired = true
		return nil
	}
	var err error
	if p.tx != nil {
		err = p.tx.Transaction(ctx, work)
	} else {
		err = work(ctx)
	}
	if err != nil {
		fired = false
	}
	return fired, err
}

// -----------------------------------------------------------------------------
// digest.weekly — Sunday aggregate of the coming 7 days (followed + library).
// -----------------------------------------------------------------------------

type digestItem struct {
	SeriesID int64  `json:"series_id"`
	Title    string `json:"series_title"`
	Season   int    `json:"season"`
	Episode  int    `json:"episode"`
	AirDate  string `json:"air_date"`
}

// DigestProducer builds ONE aggregated digest.weekly outbox row from the coming
// 7 days of premieres + finales (scope=all → followed ∪ library). An ISO-week
// marker ("<year>-W<week>") guards against a double-send if two pods tick.
type DigestProducer struct {
	cal    CalendarPort
	outbox ports.OutboxEmitter
	marks  ports.NotifiedEventsRepository
	tx     Transactor
	clock  func() time.Time
	logger *slog.Logger
}

func NewDigestProducer(cal CalendarPort, outbox ports.OutboxEmitter, marks ports.NotifiedEventsRepository, tx Transactor, logger *slog.Logger) *DigestProducer {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "notification")
	}
	return &DigestProducer{
		cal: cal, outbox: outbox, marks: marks, tx: tx,
		clock:  func() time.Time { return time.Now().UTC() },
		logger: logger,
	}
}

func (d *DigestProducer) WithClock(c func() time.Time) *DigestProducer { d.clock = c; return d }

// Run is the weekly cron entrypoint (registered "notify-weekly-digest", "0 9 * * 0").
func (d *DigestProducer) Run(ctx context.Context) {
	now := d.clock()
	from := startOfUTCDay(now)
	to := from.AddDate(0, 0, 7)

	events, err := d.cal.Upcoming(ctx, from, to, "all", false)
	if err != nil {
		d.logger.WarnContext(ctx, "notify.digest.scan_failed", slog.String("error", err.Error()))
		return
	}
	var premieres, finales []digestItem
	for _, e := range events {
		it := digestItem{
			SeriesID: e.SeriesID, Title: e.Title, Season: e.Season,
			Episode: e.Episode, AirDate: e.AirDate.UTC().Format("2006-01-02"),
		}
		switch e.Milestone {
		case "premiere":
			premieres = append(premieres, it)
		case "finale":
			finales = append(finales, it)
		}
	}
	if len(premieres)+len(finales) == 0 {
		d.logger.InfoContext(ctx, "notify.digest.empty",
			slog.String("from", from.Format("2006-01-02")), slog.String("to", to.Format("2006-01-02")))
		return
	}

	payload, _ := json.Marshal(map[string]any{
		"from":           from.Format("2006-01-02"),
		"to":             to.Format("2006-01-02"),
		"premiere_count": len(premieres),
		"finale_count":   len(finales),
		"premieres":      premieres,
		"finales":        finales,
	})
	year, week := now.ISOWeek()
	key := fmt.Sprintf("%04d-W%02d", year, week)

	work := func(txCtx context.Context) error {
		created, err := d.marks.MarkIfNew(txCtx, "digest.weekly", key, d.clock())
		if err != nil {
			return err
		}
		if !created {
			d.logger.InfoContext(txCtx, "notify.digest.already_sent", slog.String("iso_week", key))
			return nil
		}
		return d.outbox.Insert(txCtx, ports.OutboxRow{EventType: "digest.weekly", Payload: payload})
	}
	var werr error
	if d.tx != nil {
		werr = d.tx.Transaction(ctx, work)
	} else {
		werr = work(ctx)
	}
	if werr != nil {
		d.logger.WarnContext(ctx, "notify.digest.emit_failed", slog.String("error", werr.Error()))
		return
	}
	d.logger.InfoContext(ctx, "notify.digest.ok",
		slog.Int("premieres", len(premieres)), slog.Int("finales", len(finales)),
		slog.String("iso_week", key))
}
