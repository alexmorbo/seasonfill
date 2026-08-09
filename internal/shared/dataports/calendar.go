package dataports

import (
	"context"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// CalendarQuery is the bound read window for the release-calendar query.
// From/To are the inclusive air_date bounds computed in Go by the usecase
// (never SQL now()/CURRENT_DATE, so the SQLite test lane and the Postgres
// prod target agree). Scope is one of "library" | "followed" | "all".
// Instance (optional) narrows the library-membership EXISTS. Lang (optional)
// is the requested BCP-47 tier-0 for the any-lang title/poster fallback.
// Limit is a generous safety cap on the flat (episode × instance) row count.
type CalendarQuery struct {
	From     time.Time
	To       time.Time
	Lang     string
	Scope    string
	Instance string
	Limit    int
}

// CalendarEventRow is ONE flat (episode × matching episode_states instance)
// row of the calendar read. An episode present in N instances yields N rows
// (InstanceName/HasFile/Monitored per instance); an episode in NO library
// instance (a followed-not-in-library series) yields ONE row with a nil
// InstanceName. The usecase groups rows by EpisodeID, aggregates instances,
// and derives the milestone + state. IsPremiere/IsFinale come from SQL flags;
// PrevAirDate feeds the Go 35-day return-gap test.
type CalendarEventRow struct {
	EpisodeID     domain.EpisodeID
	SeriesID      domain.SeriesID
	TMDBID        *int64
	SeasonNumber  int
	EpisodeNumber int
	AirDate       time.Time
	Title         string
	Poster        *string
	IsPremiere    bool
	IsFinale      bool
	PrevAirDate   *time.Time
	Followed      bool
	InstanceName  *string
	HasFile       *bool
	Monitored     *bool
}

// CalendarRepository surfaces the read-only release-calendar query backing
// GET /api/v1/calendar. Every bound (window, now) is computed in Go and
// passed as a bind param; the SQL is dialect-portable (EXISTS / COALESCE /
// CASE / correlated MAX() / LIMIT only — no window functions, no NOW(),
// no cast). episode_states rows are filtered deleted_at IS NULL.
type CalendarRepository interface {
	Events(ctx context.Context, q CalendarQuery) ([]CalendarEventRow, error)
}
