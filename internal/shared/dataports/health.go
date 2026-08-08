package dataports

import (
	"context"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// HealthSeriesItem is one series drill-down row for the tvdb / poster
// signals. Title is series.original_title (COALESCE'd to "").
type HealthSeriesItem struct {
	SeriesID domain.SeriesID
	Title    string
}

// HealthStaleItem is one drill-down row for the stale-enrichment signal.
// Tier is "hot"|"normal"|"cold"; SyncedAt is nil when never enriched.
type HealthStaleItem struct {
	SeriesID domain.SeriesID
	Title    string
	Tier     string
	SyncedAt *time.Time
}

// HealthGrabItem is one drill-down row for the stuck-grab signal. ID is
// the grab_records uuid string.
type HealthGrabItem struct {
	ID           string
	InstanceName domain.InstanceName
	SeriesTitle  string
	SeasonNumber int
	CreatedAt    time.Time
}

// HealthInboxItem is one drill-down row for the dead-letter signal.
type HealthInboxItem struct {
	ID           int64
	InstanceName string
	EventType    string
	Attempts     int
	LastError    string
	CreatedAt    time.Time
}

// StaleCutoffs carries the three per-tier "older-than" bounds the
// usecase derives from enrichment.DefaultRefreshTTL(). A series is stale
// when its enrichment_tmdb_synced_at IS NULL OR < the cutoff for its
// tier. Passed as plain time bounds so dataports stays free of the
// enrichment domain TTL type.
type StaleCutoffs struct {
	HotBefore    time.Time
	NormalBefore time.Time
	ColdBefore   time.Time
}

// HealthRepository surfaces the read-only catalog-health "pulse"
// queries backing GET /api/v1/insights/health. Every method returns a
// full COUNT over the predicate plus a bounded top-N drill-down slice.
// SQL dialect divergence (there is none today — all queries are
// portable) is contained in the implementation.
type HealthRepository interface {
	// MissingTVDBID counts + lists series with tvdb_id IS NULL.
	MissingTVDBID(ctx context.Context, limit int) (int, []HealthSeriesItem, error)
	// MissingPoster counts + lists series with NO series_media_texts row
	// carrying a non-empty poster_asset in ANY language (F-08).
	MissingPoster(ctx context.Context, limit int) (int, []HealthSeriesItem, error)
	// StaleEnrichment counts + lists TMDB-enrichable series overdue for a
	// proactive refresh per their tier's cutoff.
	StaleEnrichment(ctx context.Context, cutoffs StaleCutoffs, limit int) (int, []HealthStaleItem, error)
	// StuckGrabs counts + lists grab_records stuck in non-terminal
	// 'grabbed' with created_at < olderThan.
	StuckGrabs(ctx context.Context, olderThan time.Time, limit int) (int, []HealthGrabItem, error)
	// DeadLetters counts + lists webhook_inbox rows with status='dead'.
	DeadLetters(ctx context.Context, limit int) (int, []HealthInboxItem, error)
}
