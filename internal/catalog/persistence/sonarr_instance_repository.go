package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

type SonarrInstanceRepository struct{ db *gorm.DB }

func NewSonarrInstanceRepository(db *gorm.DB) *SonarrInstanceRepository {
	return &SonarrInstanceRepository{db: db}
}

// List returns every sonarr_instance row converted to a runtime
// snapshot. Issues EXACTLY two SELECT queries regardless of N:
//
//  1. SELECT * FROM sonarr_instance
//  2. SELECT * FROM instance_secret WHERE secret_name = 'api_key'
//
// The secrets are joined in memory keyed by instance_id. An instance
// row with no matching secret yields a snapshot with empty APIKey
// (same contract as the old per-row Where-First path returning
// gorm.ErrRecordNotFound).
//
// Ordering is left unspecified — `runtime.SortInstances` applied at
// the publish path is the canonical sort.
func (r *SonarrInstanceRepository) List(ctx context.Context, c *crypto.Cipher) ([]runtime.InstanceSnapshot, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	var models []database.SonarrInstanceModel
	if err := db.Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list sonarr instances: %w", err)
	}
	if len(models) == 0 {
		return nil, nil
	}

	var secrets []database.InstanceSecretModel
	if err := db.Where("secret_name = ?", "api_key").Find(&secrets).Error; err != nil {
		return nil, fmt.Errorf("list api_key secrets: %w", err)
	}

	// Index the secrets by instance_id for O(1) lookup.
	secretsByID := make(map[uint][]byte, len(secrets))
	for _, s := range secrets {
		secretsByID[s.InstanceID] = s.Ciphertext
	}

	result := make([]runtime.InstanceSnapshot, 0, len(models))
	for _, m := range models {
		blob := secretsByID[m.ID] // nil if no row — same as old not-found path
		snap, err := modelToSnapshot(m, blob, c)
		if err != nil {
			return nil, fmt.Errorf("convert instance %s to snapshot: %w", m.Name, err)
		}
		result = append(result, snap)
	}
	return result, nil
}

func (r *SonarrInstanceRepository) GetByName(ctx context.Context, name string, c *crypto.Cipher) (runtime.InstanceSnapshot, error) {
	var m database.SonarrInstanceModel
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("name = ?", name).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return runtime.InstanceSnapshot{}, errors.Join(
				&sharedErrors.InstanceNotFoundError{Name: domain.InstanceName(name)},
				ports.ErrNotFound,
			)
		}
		return runtime.InstanceSnapshot{}, fmt.Errorf("get sonarr instance: %w", err)
	}

	var secret database.InstanceSecretModel
	secretErr := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_id = ? AND secret_name = ?", m.ID, "api_key").
		First(&secret).Error

	var secretBlob []byte
	if secretErr == nil {
		secretBlob = secret.Ciphertext
	} else if !errors.Is(secretErr, gorm.ErrRecordNotFound) {
		return runtime.InstanceSnapshot{}, fmt.Errorf("fetch secret: %w", secretErr)
	}

	return modelToSnapshot(m, secretBlob, c)
}

func (r *SonarrInstanceRepository) Create(ctx context.Context, inst runtime.InstanceSnapshot, c *crypto.Cipher) (uint, error) {
	var newID uint
	err := dbFromContext(ctx, r.db).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()

		m := snapshotToModel(inst)
		m.CreatedAt = now
		m.UpdatedAt = now
		if err := tx.Create(&m).Error; err != nil {
			return fmt.Errorf("create sonarr instance: %w", err)
		}

		if inst.APIKey != "" {
			ct, err := c.Seal([]byte(inst.APIKey))
			if err != nil {
				return fmt.Errorf("seal api key: %w", err)
			}
			secret := database.InstanceSecretModel{
				InstanceID: m.ID,
				SecretName: "api_key",
				Ciphertext: ct,
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			if err := tx.Create(&secret).Error; err != nil {
				return fmt.Errorf("save api key secret: %w", err)
			}
		}

		newID = m.ID
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newID, nil
}

// UpdateWithOptions writes parent + (unless preserveSecret) secret
// inside a single tx. preserveSecret==true is used when the PUT body
// omits api_key. A nil cipher is allowed only when preserveSecret.
// Parent uses Save so zero-value columns persist (PUT is full-replace).
// When ifUnmodifiedSince != nil, the stored row's updated_at
// (second-truncated to match the RFC1123 wire header) is compared
// inside the tx; strictly-newer stored → ports.ErrStaleWrite.
func (r *SonarrInstanceRepository) UpdateWithOptions(
	ctx context.Context,
	inst runtime.InstanceSnapshot,
	c *crypto.Cipher,
	preserveSecret bool,
	ifUnmodifiedSince *time.Time,
) error {
	return dbFromContext(ctx, r.db).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()

		m := snapshotToModel(inst)
		m.UpdatedAt = now

		// Read existing row inside the tx — serves both as the
		// "does it exist" check (→ ErrNotFound on miss) and as the
		// source of CreatedAt for Save (which writes every column).
		var existing database.SonarrInstanceModel
		if err := tx.Select("created_at", "updated_at").
			Where("id = ?", m.ID).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.Join(
					&sharedErrors.InstanceNotFoundError{Name: domain.InstanceName(inst.Name)},
					ports.ErrNotFound,
				)
			}
			return fmt.Errorf("load instance for update: %w", err)
		}

		if ifUnmodifiedSince != nil {
			stored := existing.UpdatedAt.Truncate(time.Second)
			provided := ifUnmodifiedSince.Truncate(time.Second)
			if stored.After(provided) {
				return ports.ErrStaleWrite
			}
		}
		m.CreatedAt = existing.CreatedAt

		if err := tx.Save(&m).Error; err != nil {
			return fmt.Errorf("update sonarr instance: %w", err)
		}

		if preserveSecret || inst.APIKey == "" {
			return nil
		}
		if c == nil {
			return fmt.Errorf("update sonarr instance: cipher required to write api_key")
		}
		ct, err := c.Seal([]byte(inst.APIKey))
		if err != nil {
			return fmt.Errorf("seal api key: %w", err)
		}

		res := tx.Model(&database.InstanceSecretModel{}).
			Where("instance_id = ? AND secret_name = ?", m.ID, "api_key").
			Updates(map[string]any{"ciphertext": ct, "updated_at": now})
		if res.Error != nil {
			return fmt.Errorf("upsert api key secret: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			secret := database.InstanceSecretModel{
				InstanceID: m.ID,
				SecretName: "api_key",
				Ciphertext: ct,
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			if err := tx.Create(&secret).Error; err != nil {
				return fmt.Errorf("create api key secret: %w", err)
			}
		}
		return nil
	})
}

// Delete hard-deletes the sonarr_instance row plus every related row
// keyed by instance name: instance_secret (FK on instance_id),
// series-scope cooldowns, scan_runs, decisions, grab_records, series_cache. All
// deletes happen inside a single transaction so a partial delete
// can't strand orphan history. GUID-scope cooldowns are tracker-
// global and intentionally not purged here.
func (r *SonarrInstanceRepository) Delete(ctx context.Context, name string) error {
	return dbFromContext(ctx, r.db).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m database.SonarrInstanceModel
		if err := tx.Where("name = ?", name).First(&m).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.Join(
					&sharedErrors.InstanceNotFoundError{Name: domain.InstanceName(name)},
					ports.ErrNotFound,
				)
			}
			return fmt.Errorf("find instance: %w", err)
		}

		if err := tx.Where("instance_id = ?", m.ID).
			Delete(&database.InstanceSecretModel{}).Error; err != nil {
			return fmt.Errorf("delete secrets: %w", err)
		}
		if err := tx.Where("scope = ? AND key LIKE ?", "series", name+":%").
			Delete(&database.CooldownModel{}).Error; err != nil {
			return fmt.Errorf("delete cooldowns: %w", err)
		}
		if err := tx.Where("instance_name = ?", name).
			Delete(&database.ScanRunModel{}).Error; err != nil {
			return fmt.Errorf("delete scan_runs: %w", err)
		}
		if err := tx.Where("instance_name = ?", name).
			Delete(&database.DecisionModel{}).Error; err != nil {
			return fmt.Errorf("delete decisions: %w", err)
		}
		if err := tx.Where("instance_name = ?", name).
			Delete(&database.GrabRecordModel{}).Error; err != nil {
			return fmt.Errorf("delete grab_records: %w", err)
		}
		if err := tx.Where("instance_name = ?", name).
			Delete(&database.SeriesCacheModel{}).Error; err != nil {
			return fmt.Errorf("delete series_cache: %w", err)
		}
		if err := tx.Delete(&m).Error; err != nil {
			return fmt.Errorf("delete instance: %w", err)
		}
		return nil
	})
}

// GetUpdatedAt is the lightweight read used by the HTTP layer for
// Last-Modified / If-Unmodified-Since. Returns ErrNotFound for
// missing names so the handler can map to 404 cleanly.
func (r *SonarrInstanceRepository) GetUpdatedAt(ctx context.Context, name string) (time.Time, error) {
	var m database.SonarrInstanceModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Select("updated_at").Where("name = ?", name).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return time.Time{}, errors.Join(
				&sharedErrors.InstanceNotFoundError{Name: domain.InstanceName(name)},
				ports.ErrNotFound,
			)
		}
		return time.Time{}, fmt.Errorf("get instance updated_at: %w", err)
	}
	return m.UpdatedAt, nil
}

func modelToSnapshot(m database.SonarrInstanceModel, secretBlob []byte, c *crypto.Cipher) (runtime.InstanceSnapshot, error) {
	var apiKey string
	if len(secretBlob) > 0 && c != nil {
		plaintext, err := c.Open(secretBlob)
		if err != nil {
			return runtime.InstanceSnapshot{}, fmt.Errorf("decrypt api key: %w", err)
		}
		apiKey = string(plaintext)
	}

	var tagsInclude, tagsExclude []string
	_ = json.Unmarshal([]byte(m.TagsInclude), &tagsInclude)
	_ = json.Unmarshal([]byte(m.TagsExclude), &tagsExclude)

	return runtime.InstanceSnapshot{
		ID:            m.ID,
		Name:          m.Name,
		URL:           m.URL,
		APIKey:        apiKey,
		Mode:          m.Mode,
		Timeout:       time.Duration(m.TimeoutSeconds) * time.Second,
		SearchTimeout: time.Duration(m.SearchTimeoutSeconds) * time.Second,
		DryRun:        m.DryRun,
		Tags: runtime.TagsSnapshot{
			Mode:    m.TagsMode,
			Include: tagsInclude,
			Exclude: tagsExclude,
		},
		Search: runtime.SearchSnapshot{
			RequireAllAired:      m.SearchRequireAllAired,
			SkipSpecials:         m.SearchSkipSpecials,
			SkipAnime:            m.SearchSkipAnime,
			MinCustomFormatScore: m.SearchMinCustomFormatScore,
		},
		Ranking: runtime.RankingSnapshot{
			IndexerPriorityEnabled: m.RankingIndexerPriorityEnabled,
			OriginBonus:            m.RankingOriginBonus,
		},
		Limits: runtime.LimitsSnapshot{
			ScanMaxSeries:   m.LimitsScanMaxSeries,
			MaxGrabsPerScan: m.LimitsMaxGrabsPerScan,
		},
		RateLimit: runtime.RateLimitSnapshot{
			RPM:   m.RateLimitRPM,
			Burst: m.RateLimitBurst,
		},
		Cooldown: runtime.CooldownSnapshot{
			Mode:                  m.CooldownMode,
			SeriesAfterGrab:       time.Duration(m.CooldownSeriesAfterGrabSec) * time.Second,
			GUIDAfterFailedGrab:   time.Duration(m.CooldownGUIDFailedGrabSec) * time.Second,
			GUIDAfterFailedImport: time.Duration(m.CooldownGUIDFailedImportSec) * time.Second,
		},
		Retry: runtime.RetrySnapshot{
			MaxAttempts:    m.RetryMaxAttempts,
			InitialBackoff: time.Duration(m.RetryInitialBackoffSec) * time.Second,
			MaxBackoff:     time.Duration(m.RetryMaxBackoffSec) * time.Second,
		},
		HealthCheck: runtime.HealthCheckSnapshot{
			RecheckAuth:    time.Duration(m.HealthCheckRecheckAuthSec) * time.Second,
			RecheckNetwork: time.Duration(m.HealthCheckRecheckNetSec) * time.Second,
		},
		PublicURL:              m.PublicURL,
		WebhookInstallEnabled:  m.WebhookInstallEnabled,
		WebhookURLOverride:     m.WebhookURLOverride,
		ParseOnGrabEnabled:     m.ParseOnGrabEnabled,
		ScanSkipHandledSeasons: m.ScanSkipHandledSeasons,
	}, nil
}

func snapshotToModel(s runtime.InstanceSnapshot) database.SonarrInstanceModel {
	tagsInclude, _ := json.Marshal(s.Tags.Include)
	tagsExclude, _ := json.Marshal(s.Tags.Exclude)

	return database.SonarrInstanceModel{
		ID:                            s.ID,
		Name:                          s.Name,
		URL:                           s.URL,
		Mode:                          s.Mode,
		TimeoutSeconds:                int(s.Timeout / time.Second),
		SearchTimeoutSeconds:          int(s.SearchTimeout / time.Second),
		DryRun:                        s.DryRun,
		TagsMode:                      s.Tags.Mode,
		TagsInclude:                   string(tagsInclude),
		TagsExclude:                   string(tagsExclude),
		SearchRequireAllAired:         s.Search.RequireAllAired,
		SearchSkipSpecials:            s.Search.SkipSpecials,
		SearchSkipAnime:               s.Search.SkipAnime,
		SearchMinCustomFormatScore:    s.Search.MinCustomFormatScore,
		RankingIndexerPriorityEnabled: s.Ranking.IndexerPriorityEnabled,
		RankingOriginBonus:            s.Ranking.OriginBonus,
		LimitsScanMaxSeries:           s.Limits.ScanMaxSeries,
		LimitsMaxGrabsPerScan:         s.Limits.MaxGrabsPerScan,
		RateLimitRPM:                  s.RateLimit.RPM,
		RateLimitBurst:                s.RateLimit.Burst,
		CooldownMode:                  s.Cooldown.Mode,
		CooldownSeriesAfterGrabSec:    int(s.Cooldown.SeriesAfterGrab / time.Second),
		CooldownGUIDFailedGrabSec:     int(s.Cooldown.GUIDAfterFailedGrab / time.Second),
		CooldownGUIDFailedImportSec:   int(s.Cooldown.GUIDAfterFailedImport / time.Second),
		RetryMaxAttempts:              s.Retry.MaxAttempts,
		RetryInitialBackoffSec:        int(s.Retry.InitialBackoff / time.Second),
		RetryMaxBackoffSec:            int(s.Retry.MaxBackoff / time.Second),
		HealthCheckRecheckAuthSec:     int(s.HealthCheck.RecheckAuth / time.Second),
		HealthCheckRecheckNetSec:      int(s.HealthCheck.RecheckNetwork / time.Second),
		PublicURL:                     s.PublicURL,
		WebhookInstallEnabled:         s.WebhookInstallEnabled,
		WebhookURLOverride:            s.WebhookURLOverride,
		ParseOnGrabEnabled:            s.ParseOnGrabEnabled,
		ScanSkipHandledSeasons:        s.ScanSkipHandledSeasons,
	}
}

var _ ports.SonarrInstanceRepository = (*SonarrInstanceRepository)(nil)
