package dto

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// CalendarDTO — body of GET /api/v1/calendar. Release calendar over
// episodes.air_date (TMDB-truth): milestone-first (premiere/finale/return)
// with per-episode library status. Days are chronological; each groups the
// events that air on one UTC calendar date. From/To echo the resolved window.
type CalendarDTO struct {
	GeneratedAt time.Time        `json:"generated_at"`
	From        time.Time        `json:"from"`
	To          time.Time        `json:"to"`
	Days        []CalendarDayDTO `json:"days"`
}

// CalendarDayDTO — the events airing on one calendar date (YYYY-MM-DD, UTC).
type CalendarDayDTO struct {
	Date   string             `json:"date" example:"2026-08-09"`
	Events []CalendarEventDTO `json:"events"`
}

// CalendarEventDTO — one episode airing. media_type is "tv" in Ф3 (F-11
// forward-compat discriminator for Ф6 movie events). milestone is one of
// premiere|finale|return, or omitted. state is one of downloaded|missing|
// upcoming|followed_not_in_library, or omitted (untracked edge case).
type CalendarEventDTO struct {
	SeriesID           domain.SeriesID `json:"series_id" example:"42"`
	TMDBID             *int64          `json:"tmdb_id,omitempty" example:"1399"`
	Title              string          `json:"title" example:"The Expanse"`
	Season             int             `json:"season" example:"2"`
	Episode            int             `json:"episode" example:"1"`
	AirDate            time.Time       `json:"air_date"`
	State              string          `json:"state,omitempty" example:"downloaded"`
	InLibraryInstances []string        `json:"in_library_instances"`
	Poster             *string         `json:"poster,omitempty" example:"abc123"`
	SeasonPremiere     bool            `json:"season_premiere" example:"true"`
	Milestone          *string         `json:"milestone,omitempty" example:"premiere"`
	MediaType          string          `json:"media_type" example:"tv"`
}
