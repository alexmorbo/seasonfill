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

// AirDateAnnouncer emits air_date.announced when a series' next_air_date gains
// or shifts to a FUTURE date. It is the concrete implementation the enrichment
// series-worker calls post-tx via the AirDateAnnouncerPort seam (mirrors the
// maybeEnqueueOMDbOnIMDBGain old→new comparison). Dedup on (series_id,new-date)
// collapses a Changes-storm; a shift to a genuinely new date mints a new marker
// and re-fires.
type AirDateAnnouncer struct {
	outbox    ports.OutboxEmitter
	marks     ports.NotifiedEventsRepository
	followers ports.SeriesFollowerLister
	tx        Transactor
	clock     func() time.Time
	logger    *slog.Logger
}

func NewAirDateAnnouncer(outbox ports.OutboxEmitter, marks ports.NotifiedEventsRepository, followers ports.SeriesFollowerLister, tx Transactor, logger *slog.Logger) *AirDateAnnouncer {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "notification")
	}
	return &AirDateAnnouncer{
		outbox: outbox, marks: marks, followers: followers, tx: tx,
		clock:  func() time.Time { return time.Now().UTC() },
		logger: logger,
	}
}

func (a *AirDateAnnouncer) WithClock(c func() time.Time) *AirDateAnnouncer { a.clock = c; return a }

// MaybeAnnounce is invoked once per Handle by the series-worker, post-tx.
// oldNext = the Handle-start next_air_date; newNext = the post-merge value.
// No-op unless newNext is a future date DIFFERENT from oldNext. Best-effort:
// errors are logged, never bubbled (a missed ping self-heals on the next
// refresh that still finds an unmarked future date).
func (a *AirDateAnnouncer) MaybeAnnounce(ctx context.Context, seriesID int64, title string, oldNext, newNext *time.Time) {
	if newNext == nil {
		return
	}
	now := a.clock()
	nn := newNext.UTC()
	if !nn.After(now) {
		return // past / today — not a forward announcement
	}
	if oldNext != nil && oldNext.UTC().Truncate(24*time.Hour).Equal(nn.Truncate(24*time.Hour)) {
		return // unchanged date — no delta
	}
	followers, err := a.followers.FollowersOf(ctx, seriesID)
	if err != nil {
		a.logger.WarnContext(ctx, "notify.air_date.list_followers_failed",
			slog.Int64("series_id", seriesID), slog.String("error", err.Error()))
		return
	}
	if len(followers) == 0 {
		return // nobody follows this series
	}
	key := fmt.Sprintf("%d:%s", seriesID, nn.Format("2006-01-02"))
	payload, _ := json.Marshal(map[string]any{
		"series_id":    seriesID,
		"series_title": title,
		"air_date":     nn.Format("2006-01-02"),
	})
	fired := 0
	work := func(txCtx context.Context) error {
		for _, uid := range followers {
			created, err := a.marks.MarkIfNew(txCtx, uid, "air_date.announced", key, a.clock())
			if err != nil {
				return err
			}
			if !created {
				continue // this follower already notified for this date
			}
			if err := a.outbox.Insert(txCtx, ports.OutboxRow{
				UserID: uid, EventType: "air_date.announced", Payload: payload,
			}); err != nil {
				return err
			}
			fired++
		}
		return nil
	}
	if a.tx != nil {
		err = a.tx.Transaction(ctx, work)
	} else {
		err = work(ctx)
	}
	if err != nil {
		a.logger.WarnContext(ctx, "notify.air_date.emit_failed",
			slog.Int64("series_id", seriesID), slog.String("error", err.Error()))
		return
	}
	a.logger.InfoContext(ctx, "notify.air_date.announced",
		slog.Int64("series_id", seriesID), slog.String("air_date", nn.Format("2006-01-02")),
		slog.Int("followers_notified", fired))
}
