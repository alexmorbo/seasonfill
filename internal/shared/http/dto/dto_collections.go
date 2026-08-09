package dto

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// CollectionsReportDTO — body of GET /api/v1/insights/collections. Curated
// read-only theme/collection buckets per Sonarr instance. Instances holds one
// element per instance (single element when ?instance= is given).
type CollectionsReportDTO struct {
	GeneratedAt time.Time                `json:"generated_at"`
	Instances   []CollectionsInstanceDTO `json:"instances"`
}

// CollectionsInstanceDTO — the visible collections for one instance. Empty
// (0-owned) collections are omitted; the rest are ordered by owned_count DESC.
type CollectionsInstanceDTO struct {
	InstanceName string          `json:"instance_name" example:"homelab"`
	Collections  []CollectionDTO `json:"collections"`
}

// CollectionDTO — one curated bucket. Slug is stable (the FE localizes by it);
// Title is a machine-stable English fallback label. OwnedCount is the EXACT
// COUNT(DISTINCT series_id) total; Series is the bounded (top-50, title-ordered)
// slice. IsFranchise marks true-franchise showcases (e.g. MCU).
type CollectionDTO struct {
	Slug        string                `json:"slug" example:"books"`
	Title       string                `json:"title" example:"Based on books"`
	OwnedCount  int                   `json:"owned_count" example:"40"`
	IsFranchise bool                  `json:"is_franchise" example:"false"`
	Series      []CollectionSeriesDTO `json:"series"`
}

// CollectionSeriesDTO — one owned series in a collection.
type CollectionSeriesDTO struct {
	SeriesID domain.SeriesID       `json:"series_id" example:"42"`
	SonarrID domain.SonarrSeriesID `json:"sonarr_id" example:"31"`
	Title    string                `json:"title" example:"The Expanse"`
}
