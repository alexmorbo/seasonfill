package values

import "time"

// This file exists solely to give swaggo a struct it can introspect for the
// four *object* value objects (Rating, Title, Tagline, NextEpisodeCanon). Each
// VO is a struct with unexported fields plus a custom MarshalJSON, so swaggo's
// struct introspection sees an empty schema (Record<string, never> in the
// generated web/src/api/schema.ts). The `.swaggo` overrides file at repo root
// maps each object VO to the matching *Wire struct here via a `replace`
// directive; swaggo then emits the correct object schema.
//
// INVARIANT: every field name + json tag + wire type below MUST mirror the
// corresponding VO's MarshalJSON output exactly. swagger_wire_test.go asserts
// this shape parity so the mirrors can never silently drift. These structs are
// NOT used by any runtime code path — do not wire them into DTOs.

// RatingWire mirrors values.Rating.MarshalJSON (helper ratingJSON):
// {"score": <number|null>, "votes": <int>}.
type RatingWire struct {
	Score float64 `json:"score"`
	Votes int     `json:"votes"`
}

// TitleWire mirrors values.Title.MarshalJSON (helper titleJSON):
// {"value": <string>, "lang": <string BCP-47>}.
type TitleWire struct {
	Value string `json:"value"`
	Lang  string `json:"lang"`
}

// TaglineWire mirrors values.Tagline.MarshalJSON (helper taglineJSON):
// {"value": <string>, "lang": <string BCP-47>}.
type TaglineWire struct {
	Value string `json:"value"`
	Lang  string `json:"lang"`
}

// NextEpisodeCanonWire mirrors values.NextEpisodeCanon.MarshalJSON (helper
// nextEpisodeCanonJSON): season_number/episode_number ints, a nested title
// object, an RFC3339 air_date, and a computed days_until int.
type NextEpisodeCanonWire struct {
	SeasonNumber  int       `json:"season_number"`
	EpisodeNumber int       `json:"episode_number"`
	Title         TitleWire `json:"title"`
	AirDate       time.Time `json:"air_date"`
	DaysUntil     int       `json:"days_until"`
}
