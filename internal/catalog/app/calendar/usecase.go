// Package calendar assembles the read-only release-calendar report backing
// GET /api/v1/calendar. It owns the wall clock (window default + the
// upcoming boundary), fans the flat repo rows into day-grouped events,
// derives each event's single milestone (premiere > finale > return) and
// status state, and applies the only-premieres filter. All DB work lives
// behind the narrow CalendarRepository port.
package calendar

import (
	"context"
	"fmt"
	"sort"
	"time"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// defaultWindowMonths is the ± half-width of the default calendar window
// when the caller supplies neither from nor to.
const defaultWindowMonths = 3

// eventRowCap is a generous SAFETY cap on the flat (episode × instance) row
// count. A ±3-month window over a personal library is bounded; the cap only
// guards a pathological payload.
const eventRowCap = 5000

// returnGap is the hiatus threshold — an episode airing more than this after
// the series' previous aired episode is a "return" milestone (грилл: 35 days).
const returnGap = 35 * 24 * time.Hour

// Event is one assembled calendar entry (one episode, instances aggregated).
type Event struct {
	SeriesID           domain.SeriesID
	TMDBID             *int64
	Title              string
	Season             int
	Episode            int
	AirDate            time.Time
	State              string   // downloaded|missing|upcoming|followed_not_in_library|""
	InLibraryInstances []string // distinct, sorted
	Poster             *string
	SeasonPremiere     bool
	Milestone          *string // premiere|finale|return|nil
	MediaType          string  // always "tv" in Ф3
}

// Day groups the events that air on one calendar date (UTC YYYY-MM-DD).
type Day struct {
	Date   string
	Events []Event
}

// Report is the assembled calendar. Days are chronological; From/To echo
// the resolved (defaulted) window bounds.
type Report struct {
	GeneratedAt time.Time
	From        time.Time
	To          time.Time
	Days        []Day
}

// Query is the parsed handler request. Zero From/To → default ±3-month
// window. Scope is normalized to library|followed|all (default all).
type Query struct {
	From          time.Time
	To            time.Time
	Lang          string
	Scope         string
	Instance      string
	OnlyPremieres bool
}

// UseCase builds the Report from the CalendarRepository port.
type UseCase struct {
	repo  ports.CalendarRepository
	clock func() time.Time
}

// NewUseCase wires the read-only calendar usecase (clock defaults to time.Now().UTC).
func NewUseCase(repo ports.CalendarRepository) *UseCase {
	return &UseCase{repo: repo, clock: func() time.Time { return time.Now().UTC() }}
}

// WithClock swaps the clock for deterministic tests.
func (uc *UseCase) WithClock(clock func() time.Time) *UseCase {
	uc.clock = clock
	return uc
}

// Build resolves the window, runs the query, and assembles the day-grouped report.
func (uc *UseCase) Build(ctx context.Context, q Query) (Report, error) {
	now := uc.clock()
	from, to := q.From, q.To
	if from.IsZero() {
		from = now.AddDate(0, -defaultWindowMonths, 0)
	}
	if to.IsZero() {
		to = now.AddDate(0, defaultWindowMonths, 0)
	}
	scope := normalizeScope(q.Scope)

	rows, err := uc.repo.Events(ctx, ports.CalendarQuery{
		From:     from,
		To:       to,
		Lang:     q.Lang,
		Scope:    scope,
		Instance: q.Instance,
		Limit:    eventRowCap,
	})
	if err != nil {
		return Report{}, fmt.Errorf("calendar build: %w", err)
	}

	events := assembleEvents(rows, now, q.OnlyPremieres)
	return Report{GeneratedAt: now, From: from, To: to, Days: groupByDay(events)}, nil
}

// normalizeScope collapses anything but library|followed to "all".
func normalizeScope(s string) string {
	switch s {
	case "library", "followed":
		return s
	default:
		return "all"
	}
}

type eventAcc struct {
	base         ports.CalendarEventRow
	instances    []string
	anyHasFile   bool
	anyMonitored bool
}

// assembleEvents groups the flat rows by episode (first-seen air_date order),
// aggregates instance membership, derives milestone + state, and drops
// non-premiere events when onlyPremieres is set.
func assembleEvents(rows []ports.CalendarEventRow, now time.Time, onlyPremieres bool) []Event {
	order := make([]domain.EpisodeID, 0, len(rows))
	accs := make(map[domain.EpisodeID]*eventAcc, len(rows))
	for _, r := range rows {
		a, ok := accs[r.EpisodeID]
		if !ok {
			a = &eventAcc{base: r}
			accs[r.EpisodeID] = a
			order = append(order, r.EpisodeID)
		}
		if r.InstanceName != nil && *r.InstanceName != "" {
			a.instances = append(a.instances, *r.InstanceName)
		}
		if r.HasFile != nil && *r.HasFile {
			a.anyHasFile = true
		}
		if r.Monitored != nil && *r.Monitored {
			a.anyMonitored = true
		}
	}

	out := make([]Event, 0, len(order))
	for _, id := range order {
		a := accs[id]
		insts := dedupSorted(a.instances)
		ms := deriveMilestone(a.base, now)
		if onlyPremieres && (ms == nil || *ms != "premiere") {
			continue
		}
		out = append(out, Event{
			SeriesID:           a.base.SeriesID,
			TMDBID:             a.base.TMDBID,
			Title:              a.base.Title,
			Season:             a.base.SeasonNumber,
			Episode:            a.base.EpisodeNumber,
			AirDate:            a.base.AirDate,
			State:              deriveState(a.base.AirDate, now, a.anyHasFile, a.anyMonitored, len(insts) > 0, a.base.Followed),
			InLibraryInstances: insts,
			Poster:             a.base.Poster,
			SeasonPremiere:     a.base.IsPremiere,
			Milestone:          ms,
			MediaType:          "tv",
		})
	}
	return out
}

// deriveMilestone applies the premiere > finale > return precedence.
func deriveMilestone(r ports.CalendarEventRow, _ time.Time) *string {
	if r.IsPremiere {
		return new("premiere")
	}
	if r.IsFinale {
		return new("finale")
	}
	if r.PrevAirDate != nil && r.AirDate.Sub(*r.PrevAirDate) > returnGap {
		return new("return")
	}
	return nil
}

// deriveState applies the §0 status ladder. "" = untracked edge case.
func deriveState(air, now time.Time, hasFile, monitored, inLibrary, followed bool) string {
	if air.After(now) {
		return "upcoming"
	}
	if hasFile {
		return "downloaded"
	}
	if inLibrary && monitored {
		return "missing"
	}
	if followed && !inLibrary {
		return "followed_not_in_library"
	}
	return ""
}

// groupByDay buckets events (already air_date-ASC from the repo) into
// chronological day groups keyed by the UTC calendar date.
func groupByDay(events []Event) []Day {
	days := make([]Day, 0)
	idx := make(map[string]int)
	for _, e := range events {
		key := e.AirDate.UTC().Format("2006-01-02")
		i, ok := idx[key]
		if !ok {
			days = append(days, Day{Date: key})
			i = len(days) - 1
			idx[key] = i
		}
		days[i].Events = append(days[i].Events, e)
	}
	return days
}

func dedupSorted(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
