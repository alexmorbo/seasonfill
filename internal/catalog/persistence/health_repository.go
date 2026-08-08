package persistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// HealthRepository answers the read-only catalog-health "pulse" queries
// backing GET /api/v1/insights/health. Every query is a bounded COUNT +
// top-N drill-down over an EXISTING table; no writes, no migration. SQL
// is dialect-portable (EXISTS / NOT EXISTS / IS NULL / COALESCE / CASE
// only) so the SQLite test lane and the Postgres prod target agree.
type HealthRepository struct {
	db *gorm.DB
}

func NewHealthRepository(db *gorm.DB) *HealthRepository {
	return &HealthRepository{db: db}
}

// nullSyncSentinel sorts NULL enrichment_tmdb_synced_at rows first in
// the stale drill-down — a never-enriched series is the most stale.
// Mirrors PickRefreshCandidates' 1970 sentinel.
var nullSyncSentinel = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

type healthSeriesRow struct {
	SeriesID domain.SeriesID `gorm:"column:series_id"`
	Title    string          `gorm:"column:title"`
}

type healthStaleRow struct {
	SeriesID domain.SeriesID `gorm:"column:series_id"`
	Title    string          `gorm:"column:title"`
	Tier     string          `gorm:"column:tier"`
	SyncedAt *time.Time      `gorm:"column:synced_at"`
}

type healthGrabRow struct {
	ID           string              `gorm:"column:id"`
	InstanceName domain.InstanceName `gorm:"column:instance_name"`
	SeriesTitle  string              `gorm:"column:series_title"`
	SeasonNumber int                 `gorm:"column:season_number"`
	CreatedAt    time.Time           `gorm:"column:created_at"`
}

type healthInboxRow struct {
	ID           int64     `gorm:"column:id"`
	InstanceName string    `gorm:"column:instance_name"`
	EventType    string    `gorm:"column:event_type"`
	Attempts     int       `gorm:"column:attempts"`
	LastError    string    `gorm:"column:last_error"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

// MissingTVDBID — series with tvdb_id IS NULL (drill-down newest id first).
func (r *HealthRepository) MissingTVDBID(ctx context.Context, limit int) (int, []ports.HealthSeriesItem, error) {
	var count int64
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT COUNT(*) FROM series s WHERE s.tvdb_id IS NULL`).
		Scan(&count).Error; err != nil {
		return 0, nil, fmt.Errorf("health missing_tvdb_id count: %w", err)
	}

	var rows []healthSeriesRow
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT s.id AS series_id, COALESCE(s.original_title, '') AS title
		       FROM series s
		      WHERE s.tvdb_id IS NULL
		      ORDER BY s.id DESC
		      LIMIT ?`, limit).
		Scan(&rows).Error; err != nil {
		return 0, nil, fmt.Errorf("health missing_tvdb_id list: %w", err)
	}

	items := make([]ports.HealthSeriesItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, ports.HealthSeriesItem{SeriesID: row.SeriesID, Title: row.Title})
	}
	return int(count), items, nil
}

// MissingPoster — series with NO series_media_texts poster in ANY
// language (F-08 / W18-15 S-E2/E3). A naive single-language
// poster_asset IS NULL is a BLOCKING bug; the ANY-LANG NOT EXISTS below
// is the correct predicate.
func (r *HealthRepository) MissingPoster(ctx context.Context, limit int) (int, []ports.HealthSeriesItem, error) {
	const predicate = `NOT EXISTS (
		SELECT 1 FROM series_media_texts t
		 WHERE t.series_id = s.id
		   AND t.poster_asset IS NOT NULL
		   AND t.poster_asset <> '')`

	var count int64
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT COUNT(*) FROM series s WHERE ` + predicate).
		Scan(&count).Error; err != nil {
		return 0, nil, fmt.Errorf("health missing_poster count: %w", err)
	}

	var rows []healthSeriesRow
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT s.id AS series_id, COALESCE(s.original_title, '') AS title
		       FROM series s
		      WHERE `+predicate+`
		      ORDER BY s.id DESC
		      LIMIT ?`, limit).
		Scan(&rows).Error; err != nil {
		return 0, nil, fmt.Errorf("health missing_poster list: %w", err)
	}

	items := make([]ports.HealthSeriesItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, ports.HealthSeriesItem{SeriesID: row.SeriesID, Title: row.Title})
	}
	return int(count), items, nil
}

// staleWhere is the shared tier-membership + per-tier staleness
// predicate. Reuses the scheduler's exact tier definitions
// (series_refresh_query.go:44-46) and DefaultRefreshTTL cutoffs, WITHOUT
// the W17-1/#1090b/tvdb optimization branches. Three bind params:
// HotBefore, NormalBefore, ColdBefore.
const staleWhere = `s.tmdb_id IS NOT NULL
   AND (
     (    EXISTS (SELECT 1 FROM series_cache sc WHERE sc.series_id = s.id AND sc.deleted_at IS NULL)
       AND (s.enrichment_tmdb_synced_at IS NULL OR s.enrichment_tmdb_synced_at < ?))
     OR
     (NOT EXISTS (SELECT 1 FROM series_cache sc WHERE sc.series_id = s.id AND sc.deleted_at IS NULL)
       AND EXISTS (SELECT 1 FROM discovery_lists dl WHERE dl.series_id = s.id)
       AND (s.enrichment_tmdb_synced_at IS NULL OR s.enrichment_tmdb_synced_at < ?))
     OR
     (NOT EXISTS (SELECT 1 FROM series_cache sc WHERE sc.series_id = s.id AND sc.deleted_at IS NULL)
       AND NOT EXISTS (SELECT 1 FROM discovery_lists dl WHERE dl.series_id = s.id)
       AND (s.enrichment_tmdb_synced_at IS NULL OR s.enrichment_tmdb_synced_at < ?))
   )`

// StaleEnrichment — TMDB-enrichable series overdue for a proactive
// refresh per their tier cutoff. Drill-down: NULL-synced first, then
// oldest synced first (COALESCE sentinel), then id.
func (r *HealthRepository) StaleEnrichment(ctx context.Context, cutoffs ports.StaleCutoffs, limit int) (int, []ports.HealthStaleItem, error) {
	var count int64
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT COUNT(*) FROM series s WHERE `+staleWhere,
			cutoffs.HotBefore, cutoffs.NormalBefore, cutoffs.ColdBefore).
		Scan(&count).Error; err != nil {
		return 0, nil, fmt.Errorf("health stale_enrichment count: %w", err)
	}

	// Tier CASE is pure membership (no cutoff bind) — hot wins over
	// normal wins over cold, matching the WHERE precedence.
	sqlText := `SELECT s.id AS series_id,
	       COALESCE(s.original_title, '') AS title,
	       CASE
	         WHEN EXISTS (SELECT 1 FROM series_cache sc WHERE sc.series_id = s.id AND sc.deleted_at IS NULL) THEN 'hot'
	         WHEN EXISTS (SELECT 1 FROM discovery_lists dl WHERE dl.series_id = s.id) THEN 'normal'
	         ELSE 'cold'
	       END AS tier,
	       s.enrichment_tmdb_synced_at AS synced_at
	  FROM series s
	 WHERE ` + staleWhere + `
	 ORDER BY COALESCE(s.enrichment_tmdb_synced_at, ?) ASC, s.id ASC
	 LIMIT ?`

	var rows []healthStaleRow
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlText,
			cutoffs.HotBefore, cutoffs.NormalBefore, cutoffs.ColdBefore,
			nullSyncSentinel, limit).
		Scan(&rows).Error; err != nil {
		return 0, nil, fmt.Errorf("health stale_enrichment list: %w", err)
	}

	items := make([]ports.HealthStaleItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, ports.HealthStaleItem{
			SeriesID: row.SeriesID,
			Title:    row.Title,
			Tier:     row.Tier,
			SyncedAt: row.SyncedAt,
		})
	}
	return int(count), items, nil
}

// StuckGrabs — grab_records stuck in non-terminal 'grabbed' older than
// olderThan. Drill-down oldest first (most-stuck first).
func (r *HealthRepository) StuckGrabs(ctx context.Context, olderThan time.Time, limit int) (int, []ports.HealthGrabItem, error) {
	grabbed := string(grab.StatusGrabbed)

	var count int64
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT COUNT(*) FROM grab_records
		      WHERE status = ? AND created_at < ?`, grabbed, olderThan).
		Scan(&count).Error; err != nil {
		return 0, nil, fmt.Errorf("health stuck_grabs count: %w", err)
	}

	var rows []healthGrabRow
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT id, instance_name, series_title, season_number, created_at
		       FROM grab_records
		      WHERE status = ? AND created_at < ?
		      ORDER BY created_at ASC
		      LIMIT ?`, grabbed, olderThan, limit).
		Scan(&rows).Error; err != nil {
		return 0, nil, fmt.Errorf("health stuck_grabs list: %w", err)
	}

	items := make([]ports.HealthGrabItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, ports.HealthGrabItem{
			ID:           row.ID,
			InstanceName: row.InstanceName,
			SeriesTitle:  row.SeriesTitle,
			SeasonNumber: row.SeasonNumber,
			CreatedAt:    row.CreatedAt,
		})
	}
	return int(count), items, nil
}

// DeadLetters — webhook_inbox rows the drainer promoted to status='dead'
// at the attempt ceiling. Drill-down newest first.
func (r *HealthRepository) DeadLetters(ctx context.Context, limit int) (int, []ports.HealthInboxItem, error) {
	dead := ports.WebhookInboxStatusDead

	var count int64
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT COUNT(*) FROM webhook_inbox WHERE status = ?`, dead).
		Scan(&count).Error; err != nil {
		return 0, nil, fmt.Errorf("health dead_letters count: %w", err)
	}

	var rows []healthInboxRow
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT id, instance_name, event_type, attempts, last_error, created_at
		       FROM webhook_inbox
		      WHERE status = ?
		      ORDER BY created_at DESC
		      LIMIT ?`, dead, limit).
		Scan(&rows).Error; err != nil {
		return 0, nil, fmt.Errorf("health dead_letters list: %w", err)
	}

	items := make([]ports.HealthInboxItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, ports.HealthInboxItem{
			ID:           row.ID,
			InstanceName: row.InstanceName,
			EventType:    row.EventType,
			Attempts:     row.Attempts,
			LastError:    row.LastError,
			CreatedAt:    row.CreatedAt,
		})
	}
	return int(count), items, nil
}

// Ensure interface compliance at compile time.
var _ ports.HealthRepository = (*HealthRepository)(nil)
