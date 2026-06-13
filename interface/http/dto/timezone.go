package dto

// TimezoneResponse is the wire shape of GET /settings/timezone.
// Source is one of "db" | "env" | "default" — see tz.Source.
//
// RequiresRestart is true when the in-memory resolver has been
// PATCH'd since boot: cron schedulers built at boot will continue
// using the previous location until pod restart. Frontend can
// surface a banner.
type TimezoneResponse struct {
	Timezone        string `json:"timezone"          example:"Europe/Moscow"`
	Source          string `json:"source"            example:"db"`
	RequiresRestart bool   `json:"requires_restart"  example:"false"`
}

// TimezonePatchRequest is the body of PATCH /settings/timezone.
// Empty string clears the DB override (env / default resumes).
type TimezonePatchRequest struct {
	Timezone string `json:"timezone" example:"Europe/Moscow"`
}
