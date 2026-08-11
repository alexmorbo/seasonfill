package dto

import "time"

// dto_movie_calendar.go — Ф6-R-6a wire shapes for GET /api/v1/movies/calendar.

type MovieCalendarDTO struct {
	GeneratedAt time.Time             `json:"generated_at"`
	From        time.Time             `json:"from"`
	To          time.Time             `json:"to"`
	Days        []MovieCalendarDayDTO `json:"days"`
}

type MovieCalendarDayDTO struct {
	Date   string                  `json:"date"`
	Events []MovieCalendarEventDTO `json:"events"`
}

type MovieCalendarEventDTO struct {
	MovieID   int64     `json:"movie_id"`
	TMDBID    *int64    `json:"tmdb_id"`
	Title     string    `json:"title"`
	Poster    *string   `json:"poster"`
	Milestone string    `json:"milestone"`
	Date      time.Time `json:"date"`
}
