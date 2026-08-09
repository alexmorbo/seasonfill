package dto

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SmartListsReportDTO — body of GET /api/v1/insights/lists. Curated read-only
// shelves ("smart lists") per Sonarr instance. Instances holds one element per
// instance (single element when ?instance= is given).
type SmartListsReportDTO struct {
	GeneratedAt time.Time               `json:"generated_at"`
	Instances   []SmartListsInstanceDTO `json:"instances"`
}

// SmartListsInstanceDTO — the fixed shelf set for one instance. All three
// shelves are always present (possibly count 0 / empty series).
type SmartListsInstanceDTO struct {
	InstanceName string          `json:"instance_name" example:"main"`
	Shelves      []SmartShelfDTO `json:"shelves"`
}

// SmartShelfDTO — one named shelf. Count is the EXACT matching total; Series is
// the bounded (top-50) slice. Key is stable; Title is a machine-stable English
// label (the FE localizes by Key).
type SmartShelfDTO struct {
	Key    string               `json:"key" example:"ended_incomplete"`
	Title  string               `json:"title" example:"Ended with gaps"`
	Count  int                  `json:"count" example:"7"`
	Series []SmartListSeriesDTO `json:"series"`
}

// SmartListSeriesDTO — one series on a shelf. Exactly one of the optional
// metric fields is set, per the owning shelf: missing_count (ended_incomplete),
// next_air_date (returning_soon), last_aired_at (hiatus).
type SmartListSeriesDTO struct {
	SeriesID     domain.SeriesID       `json:"series_id" example:"42"`
	SonarrID     domain.SonarrSeriesID `json:"sonarr_id" example:"31"`
	Title        string                `json:"title" example:"The Expanse"`
	MissingCount *int                  `json:"missing_count,omitempty" example:"5"`
	NextAirDate  *time.Time            `json:"next_air_date,omitempty"`
	LastAiredAt  *time.Time            `json:"last_aired_at,omitempty"`
}
