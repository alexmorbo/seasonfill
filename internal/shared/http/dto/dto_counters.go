package dto

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// CounterBucketDTO — one bucket in a counters response. BucketStart is
// the UTC start of the bucket (hourly for 24h, daily otherwise). The
// `date` JSON key name is preserved from the PRD's sample wire shape
// so the SPA wrapper stays unchanged across the daily/hourly variants.
type CounterBucketDTO struct {
	Date    time.Time `json:"date"`
	Grabs   int       `json:"grabs"`
	Imports int       `json:"imports"`
	Fails   int       `json:"fails"`
}

// CounterTotals — window-wide sums. Sum across the bucket slice MUST
// equal these values; clients render them next to the sparkline.
type CounterTotals struct {
	Grabs   int `json:"grabs"`
	Imports int `json:"imports"`
	Fails   int `json:"fails"`
}

// InstanceCountersDTO — body of
// GET /api/v1/instances/{name}/counters?window=...
//
// AvgGrabs7d is the daily average over the 7 days ending at midnight
// UTC today (exclusive of today). Used by the Dashboard's
// above/below-average copy. Float emitted at full precision; the SPA
// formats to 1 decimal place.
type InstanceCountersDTO struct {
	InstanceName domain.InstanceName `json:"instance_name" example:"homelab"`
	Window       string              `json:"window"        example:"24h" enums:"24h,7d,30d"`
	Totals       CounterTotals       `json:"totals"`
	Sparkline    []CounterBucketDTO  `json:"sparkline"`
	AvgGrabs7d   float64             `json:"avg_grabs_7d"  example:"9.5"`
}

// CountersAggregateDTO — body of GET /api/v1/counters?window=...
// Aggregates one InstanceCountersDTO per known instance.
type CountersAggregateDTO struct {
	Items []InstanceCountersDTO `json:"items"`
}
