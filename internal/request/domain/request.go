// Package domain is the request-workflow domain layer (Ф8-U-2, ADR-0020 §D2).
// A Request is a queued add awaiting approval; AddSpec is the replayable
// snapshot of the original add parameters.
package domain

import "time"

// Status enum (app-owned, no DB CHECK — mirrors notification_outbox.status).
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusDenied   = "denied"
)

// MediaType enum.
const (
	MediaTypeTV    = "tv"
	MediaTypeMovie = "movie"
)

// AddSpec is the replayable snapshot of an add request. ExternalID is the
// add-flow content id (TVDB for tv, TMDB for movie). Seasons is a pointer so
// the tv nil (no override) vs empty ([]int{} = monitor nothing) distinction
// survives a round-trip, matching AddRequest.MonitoredSeasons semantics.
type AddSpec struct {
	MediaType           string `json:"media_type"`
	ExternalID          int64  `json:"external_id"`
	InstanceName        string `json:"instance_name"`
	QualityProfileID    int    `json:"quality_profile_id"`
	RootFolderPath      string `json:"root_folder_path"`
	Monitored           bool   `json:"monitored"`
	MonitorMode         string `json:"monitor_mode,omitempty"`
	SearchOnAdd         bool   `json:"search_on_add,omitempty"`
	MinimumAvailability string `json:"minimum_availability,omitempty"` // movie only
	Seasons             *[]int `json:"seasons,omitempty"`              // tv only
}

// Request is the persisted queue row.
type Request struct {
	ID         uint
	UserID     uint
	MediaType  string
	TMDBID     int64 // add-flow content id (see AddSpec.ExternalID)
	Seasons    *[]int
	Spec       AddSpec
	Status     string
	ApproverID *uint
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
