package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

type SonarrInstanceRepository struct{ db *gorm.DB }

func NewSonarrInstanceRepository(db *gorm.DB) *SonarrInstanceRepository {
	return &SonarrInstanceRepository{db: db}
}

func (r *SonarrInstanceRepository) List(ctx context.Context, c *crypto.Cipher) ([]runtime.InstanceSnapshot, error) {
	var models []database.SonarrInstanceModel
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list sonarr instances: %w", err)
	}

	var result []runtime.InstanceSnapshot
	for _, m := range models {
		var secret database.InstanceSecretModel
		secretErr := dbFromContext(ctx, r.db).WithContext(ctx).
			Where("instance_id = ? AND secret_name = ?", m.ID, "api_key").
			First(&secret).Error

		var secretBlob []byte
		if secretErr == nil {
			secretBlob = secret.Ciphertext
		} else if !errors.Is(secretErr, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("fetch secret for instance %d: %w", m.ID, secretErr)
		}

		snap, err := modelToSnapshot(m, secretBlob, c)
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
			return runtime.InstanceSnapshot{}, ports.ErrNotFound
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
	m := snapshotToModel(inst)
	m.CreatedAt = time.Now().UTC()
	m.UpdatedAt = m.CreatedAt

	db := dbFromContext(ctx, r.db).WithContext(ctx)
	if err := db.Create(&m).Error; err != nil {
		return 0, fmt.Errorf("create sonarr instance: %w", err)
	}

	if inst.APIKey != "" {
		ct, err := c.Seal([]byte(inst.APIKey))
		if err != nil {
			return 0, fmt.Errorf("seal api key: %w", err)
		}
		secret := database.InstanceSecretModel{
			InstanceID: m.ID,
			SecretName: "api_key",
			Ciphertext: ct,
			CreatedAt:  m.CreatedAt,
			UpdatedAt:  m.UpdatedAt,
		}
		if err := db.Create(&secret).Error; err != nil {
			return 0, fmt.Errorf("save api key secret: %w", err)
		}
	}

	return m.ID, nil
}

func (r *SonarrInstanceRepository) Update(ctx context.Context, inst runtime.InstanceSnapshot, c *crypto.Cipher) error {
	m := snapshotToModel(inst)
	m.UpdatedAt = time.Now().UTC()

	db := dbFromContext(ctx, r.db).WithContext(ctx)

	if err := db.Model(&m).Updates(&m).Error; err != nil {
		return fmt.Errorf("update sonarr instance: %w", err)
	}

	if inst.APIKey != "" {
		ct, err := c.Seal([]byte(inst.APIKey))
		if err != nil {
			return fmt.Errorf("seal api key: %w", err)
		}
		if err := db.Model(&database.InstanceSecretModel{}).
			Where("instance_id = ? AND secret_name = ?", m.ID, "api_key").
			Updates(map[string]interface{}{"ciphertext": ct, "updated_at": m.UpdatedAt}).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				secret := database.InstanceSecretModel{
					InstanceID: m.ID,
					SecretName: "api_key",
					Ciphertext: ct,
					CreatedAt:  time.Now().UTC(),
					UpdatedAt:  m.UpdatedAt,
				}
				if err := db.Create(&secret).Error; err != nil {
					return fmt.Errorf("create api key secret: %w", err)
				}
			} else {
				return fmt.Errorf("upsert api key secret: %w", err)
			}
		}
	}

	return nil
}

func (r *SonarrInstanceRepository) Delete(ctx context.Context, name string) error {
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	var m database.SonarrInstanceModel
	if err := db.Where("name = ?", name).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ports.ErrNotFound
		}
		return fmt.Errorf("find instance: %w", err)
	}

	if err := db.Where("instance_id = ?", m.ID).Delete(&database.InstanceSecretModel{}).Error; err != nil {
		return fmt.Errorf("delete secrets: %w", err)
	}

	if err := db.Delete(&m).Error; err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}

	return nil
}

func (r *SonarrInstanceRepository) Count(ctx context.Context) (int, error) {
	var count int64
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.SonarrInstanceModel{}).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count instances: %w", err)
	}
	return int(count), nil
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
	}, nil
}

func snapshotToModel(s runtime.InstanceSnapshot) database.SonarrInstanceModel {
	tagsInclude, _ := json.Marshal(s.Tags.Include)
	tagsExclude, _ := json.Marshal(s.Tags.Exclude)

	return database.SonarrInstanceModel{
		ID:                              s.ID,
		Name:                            s.Name,
		URL:                             s.URL,
		Mode:                            s.Mode,
		TimeoutSeconds:                  int(s.Timeout / time.Second),
		SearchTimeoutSeconds:            int(s.SearchTimeout / time.Second),
		DryRun:                          s.DryRun,
		TagsMode:                        s.Tags.Mode,
		TagsInclude:                     string(tagsInclude),
		TagsExclude:                     string(tagsExclude),
		SearchRequireAllAired:           s.Search.RequireAllAired,
		SearchSkipSpecials:              s.Search.SkipSpecials,
		SearchSkipAnime:                 s.Search.SkipAnime,
		SearchMinCustomFormatScore:      s.Search.MinCustomFormatScore,
		RankingIndexerPriorityEnabled:   s.Ranking.IndexerPriorityEnabled,
		RankingOriginBonus:              s.Ranking.OriginBonus,
		LimitsScanMaxSeries:             s.Limits.ScanMaxSeries,
		LimitsMaxGrabsPerScan:           s.Limits.MaxGrabsPerScan,
		RateLimitRPM:                    s.RateLimit.RPM,
		RateLimitBurst:                  s.RateLimit.Burst,
		CooldownMode:                    s.Cooldown.Mode,
		CooldownSeriesAfterGrabSec:      int(s.Cooldown.SeriesAfterGrab / time.Second),
		CooldownGUIDFailedGrabSec:       int(s.Cooldown.GUIDAfterFailedGrab / time.Second),
		CooldownGUIDFailedImportSec:     int(s.Cooldown.GUIDAfterFailedImport / time.Second),
		RetryMaxAttempts:                s.Retry.MaxAttempts,
		RetryInitialBackoffSec:          int(s.Retry.InitialBackoff / time.Second),
		RetryMaxBackoffSec:              int(s.Retry.MaxBackoff / time.Second),
		HealthCheckRecheckAuthSec:       int(s.HealthCheck.RecheckAuth / time.Second),
		HealthCheckRecheckNetSec:        int(s.HealthCheck.RecheckNetwork / time.Second),
	}
}

var _ ports.SonarrInstanceRepository = (*SonarrInstanceRepository)(nil)
