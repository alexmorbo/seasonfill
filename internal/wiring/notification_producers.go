package wiring

import (
	"context"
	"log/slog"
	"time"

	"gorm.io/gorm"

	calendar "github.com/alexmorbo/seasonfill/internal/catalog/app/calendar"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	notifapp "github.com/alexmorbo/seasonfill/internal/notification/app"
	notifpersistence "github.com/alexmorbo/seasonfill/internal/notification/persistence"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// calendarProducerAdapter adapts the S2 *calendar.UseCase to notifapp.CalendarPort
// — flattening the day-grouped Report into the flat CalendarEvent slice the
// producers consume. Reuses the S2 query verbatim (no duplicated SQL).
type calendarProducerAdapter struct{ uc *calendar.UseCase }

func (a calendarProducerAdapter) Upcoming(ctx context.Context, from, to time.Time, scope string, onlyPremieres bool) ([]notifapp.CalendarEvent, error) {
	rep, err := a.uc.Build(ctx, calendar.Query{
		From:          from,
		To:            to,
		Scope:         scope,
		OnlyPremieres: onlyPremieres,
	})
	if err != nil {
		return nil, err
	}
	var out []notifapp.CalendarEvent
	for _, day := range rep.Days {
		for _, e := range day.Events {
			ms := ""
			if e.Milestone != nil {
				ms = *e.Milestone
			}
			out = append(out, notifapp.CalendarEvent{
				SeriesID:  int64(e.SeriesID),
				Title:     e.Title,
				Season:    e.Season,
				Episode:   e.Episode,
				AirDate:   e.AirDate,
				Milestone: ms,
			})
		}
	}
	return out, nil
}

// NotificationProducersBundle groups the Ф4 N3 producers built at boot.
type NotificationProducersBundle struct {
	Premiere *notifapp.PremiereProducer
	Digest   *notifapp.DigestProducer
	AirDate  *notifapp.AirDateAnnouncer
}

// BuildNotificationProducers constructs the calendar-event producers + the
// air_date announcer over the shared DB, outbox emitter, and Transactor.
func BuildNotificationProducers(db *gorm.DB, outbox ports.OutboxEmitter, tx notifapp.Transactor, log *slog.Logger) *NotificationProducersBundle {
	logn := sharedports.DomainLogger(log, "notification")
	adapter := calendarProducerAdapter{uc: calendar.NewUseCase(catalogpersistence.NewCalendarRepository(db))}
	marks := notifpersistence.NewNotifiedEventsRepository(db)
	return &NotificationProducersBundle{
		Premiere: notifapp.NewPremiereProducer(adapter, outbox, marks, tx, logn),
		Digest:   notifapp.NewDigestProducer(adapter, outbox, marks, tx, logn),
		AirDate:  notifapp.NewAirDateAnnouncer(outbox, marks, tx, logn),
	}
}
