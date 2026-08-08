package dto

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// HealthDashboardDTO — body of GET /api/v1/insights/health. A per-signal
// operator "pulse": each signal carries a COUNT and a bounded (top-50)
// drill-down list; rate_limit_pressure is a deferred envelope.
type HealthDashboardDTO struct {
	GeneratedAt       time.Time               `json:"generated_at"`
	MissingTVDBID     HealthSeriesSignalDTO   `json:"missing_tvdb_id"`
	MissingPoster     HealthSeriesSignalDTO   `json:"missing_poster"`
	StaleEnrichment   HealthStaleSignalDTO    `json:"stale_enrichment"`
	StuckGrabs        HealthGrabSignalDTO     `json:"stuck_grabs"`
	DeadLetters       HealthInboxSignalDTO    `json:"dead_letters"`
	RateLimitPressure HealthDeferredSignalDTO `json:"rate_limit_pressure"`
}

// HealthSeriesSignalDTO — count + series drill-down (tvdb / poster).
type HealthSeriesSignalDTO struct {
	Count int                   `json:"count" example:"3"`
	Items []HealthSeriesItemDTO `json:"items"`
}

// HealthSeriesItemDTO — one series drill-down row.
type HealthSeriesItemDTO struct {
	SeriesID domain.SeriesID `json:"series_id" example:"42"`
	Title    string          `json:"title" example:"The Expanse"`
}

// HealthStaleSignalDTO — count + stale-enrichment drill-down.
type HealthStaleSignalDTO struct {
	Count int                  `json:"count" example:"7"`
	Items []HealthStaleItemDTO `json:"items"`
}

// HealthStaleItemDTO — one stale series with its tier + freshness clock.
type HealthStaleItemDTO struct {
	SeriesID domain.SeriesID `json:"series_id" example:"42"`
	Title    string          `json:"title" example:"The Expanse"`
	Tier     string          `json:"tier" example:"hot" enums:"hot,normal,cold"`
	SyncedAt *time.Time      `json:"synced_at"`
}

// HealthGrabSignalDTO — count + stuck-grab drill-down. Note
// disambiguates this DB signal from seasonfill_webhook_orphan_total.
type HealthGrabSignalDTO struct {
	Count int                 `json:"count" example:"1"`
	Note  string              `json:"note"`
	Items []HealthGrabItemDTO `json:"items"`
}

// HealthGrabItemDTO — one stuck grab record.
type HealthGrabItemDTO struct {
	ID           string              `json:"id" example:"a1b2c3d4-0000-0000-0000-000000000000"`
	InstanceName domain.InstanceName `json:"instance_name" example:"main"`
	SeriesTitle  string              `json:"series_title" example:"Hijack"`
	SeasonNumber int                 `json:"season_number" example:"2"`
	CreatedAt    time.Time           `json:"created_at"`
}

// HealthInboxSignalDTO — count + dead-letter drill-down.
type HealthInboxSignalDTO struct {
	Count int                  `json:"count" example:"0"`
	Items []HealthInboxItemDTO `json:"items"`
}

// HealthInboxItemDTO — one webhook_inbox dead-letter row.
type HealthInboxItemDTO struct {
	ID           int64     `json:"id" example:"1001"`
	InstanceName string    `json:"instance_name" example:"main"`
	EventType    string    `json:"event_type" example:"Download"`
	Attempts     int       `json:"attempts" example:"6"`
	LastError    string    `json:"last_error" example:"sonarr 500"`
	CreatedAt    time.Time `json:"created_at"`
}

// HealthDeferredSignalDTO — a signal intentionally not computed here,
// with a pointer to where the operator can currently see it.
type HealthDeferredSignalDTO struct {
	Deferred bool   `json:"deferred" example:"true"`
	Reason   string `json:"reason"`
	Metric   string `json:"metric" example:"seasonfill_sonarr_rate_oversubscribed"`
}
