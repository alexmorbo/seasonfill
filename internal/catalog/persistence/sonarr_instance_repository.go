package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// instanceTokenSecretName is the secret_name used for the per-instance
// Sonarr API key encrypted blob row in instance_secret.
const instanceTokenSecretName = "token"

type SonarrInstanceRepository struct{ db *gorm.DB }

func NewSonarrInstanceRepository(db *gorm.DB) *SonarrInstanceRepository {
	return &SonarrInstanceRepository{db: db}
}

// List returns every sonarr_instance row converted to a runtime
// snapshot. Issues EXACTLY two SELECT queries regardless of N:
//
//  1. SELECT * FROM sonarr_instance  (parent rows)
//  2. SELECT * FROM sonarr_instance_settings  (joined in memory by name)
//
// And exactly one more SELECT against instance_secret. Total of 3
// SELECTs, independent of N. The repo-level test guarantees this
// stays a constant (see sonarr_instance_repository_test.go N+1 tests
// — counter is set to 3).
//
// Secrets are joined in memory keyed by instance_name. An instance
// row with no matching secret yields a snapshot with empty APIKey.
func (r *SonarrInstanceRepository) List(ctx context.Context, c *crypto.Cipher) ([]runtime.InstanceSnapshot, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	var parents []database.SonarrInstanceModel
	if err := db.Find(&parents).Error; err != nil {
		return nil, fmt.Errorf("list sonarr instances: %w", err)
	}
	if len(parents) == 0 {
		return nil, nil
	}

	var allSettings []database.SonarrInstanceSettingsModel
	if err := db.Find(&allSettings).Error; err != nil {
		return nil, fmt.Errorf("list sonarr instance settings: %w", err)
	}
	settingsByInstance := make(map[string]database.SonarrInstanceSettingsModel, len(allSettings))
	for _, s := range allSettings {
		settingsByInstance[s.InstanceName] = s
	}

	var secrets []database.InstanceSecretModel
	if err := db.Where("secret_name = ?", instanceTokenSecretName).
		Find(&secrets).Error; err != nil {
		return nil, fmt.Errorf("list instance token secrets: %w", err)
	}
	secretsByInstance := make(map[string][]byte, len(secrets))
	for _, s := range secrets {
		secretsByInstance[s.InstanceName] = s.EncryptedValue
	}

	out := make([]runtime.InstanceSnapshot, 0, len(parents))
	for _, p := range parents {
		settings, hasSettings := settingsByInstance[p.Name]
		snap, err := modelToSnapshot(p, settings, hasSettings, secretsByInstance[p.Name], c)
		if err != nil {
			return nil, fmt.Errorf("convert instance %s to snapshot: %w", p.Name, err)
		}
		out = append(out, snap)
	}
	return out, nil
}

// GetByName returns the snapshot for the named instance. Wraps the
// not-found case with both the typed InstanceNotFoundError and the
// sentinel ports.ErrNotFound (errors.Is + errors.As compatible).
func (r *SonarrInstanceRepository) GetByName(ctx context.Context, name string, c *crypto.Cipher) (runtime.InstanceSnapshot, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	var m database.SonarrInstanceModel
	if err := db.Where("name = ?", name).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return runtime.InstanceSnapshot{}, errors.Join(
				&sharedErrors.InstanceNotFoundError{Name: domain.InstanceName(name)},
				ports.ErrNotFound,
			)
		}
		return runtime.InstanceSnapshot{}, fmt.Errorf("get sonarr instance: %w", err)
	}

	var settings database.SonarrInstanceSettingsModel
	settingsErr := db.Where("instance_name = ?", name).Take(&settings).Error
	hasSettings := settingsErr == nil
	if settingsErr != nil && !errors.Is(settingsErr, gorm.ErrRecordNotFound) {
		return runtime.InstanceSnapshot{}, fmt.Errorf("get instance settings: %w", settingsErr)
	}

	var secret database.InstanceSecretModel
	var secretBlob []byte
	secretErr := db.Where("instance_name = ? AND secret_name = ?", name, instanceTokenSecretName).
		Take(&secret).Error
	switch {
	case secretErr == nil:
		secretBlob = secret.EncryptedValue
	case errors.Is(secretErr, gorm.ErrRecordNotFound):
		// no secret yet — fall through with empty blob
	default:
		return runtime.InstanceSnapshot{}, fmt.Errorf("fetch secret: %w", secretErr)
	}

	return modelToSnapshot(m, settings, hasSettings, secretBlob, c)
}

// Create wires the cyclic FK between sonarr_instance and
// instance_secret in a single transaction:
//
//  1. INSERT sonarr_instance (token_secret_id = NULL)
//  2. INSERT sonarr_instance_settings (defaults from snap)
//  3. INSERT instance_secret (secret_name='token', encrypted_value)
//  4. UPDATE sonarr_instance SET token_secret_id = $newSecretID
//
// Steps 3+4 are skipped when inst.APIKey == "" (no secret to write).
// Returns the new secret_id (or 0 when no secret was written) — the
// repo's public contract returns `uint` so the ports.SonarrInstanceRepository
// interface stays unchanged.
func (r *SonarrInstanceRepository) Create(ctx context.Context, inst runtime.InstanceSnapshot, c *crypto.Cipher) (uint, error) {
	var secretID uint
	err := dbFromContext(ctx, r.db).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()

		parent := snapshotToInstanceModel(inst)
		parent.CreatedAt = now
		parent.UpdatedAt = now
		// Step 1 — insert with NULL token_secret_id.
		if err := tx.Create(&parent).Error; err != nil {
			return fmt.Errorf("create sonarr_instance: %w", err)
		}

		// Step 2 — insert settings sibling row.
		settings := snapshotToSettingsModel(inst)
		settings.InstanceName = inst.Name
		settings.UpdatedAt = now
		if err := tx.Create(&settings).Error; err != nil {
			return fmt.Errorf("create sonarr_instance_settings: %w", err)
		}

		if inst.APIKey == "" {
			return nil
		}
		if c == nil {
			return errors.New("create sonarr instance: cipher required to write api_key")
		}
		ct, err := c.Seal([]byte(inst.APIKey))
		if err != nil {
			return fmt.Errorf("seal api key: %w", err)
		}
		// Step 3 — insert secret.
		sec := database.InstanceSecretModel{
			InstanceName:   inst.Name,
			SecretName:     instanceTokenSecretName,
			EncryptedValue: ct,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := tx.Create(&sec).Error; err != nil {
			return fmt.Errorf("create instance_secret: %w", err)
		}
		secretID = sec.ID

		// Step 4 — update sonarr_instance.token_secret_id (cyclic FK).
		// Pass the same `now` so the parent row's updated_at stays
		// in lockstep with the secret row (the
		// TestSonarrInstanceRepository_Create_TimestampsMatch contract).
		if err := tx.Model(&database.SonarrInstanceModel{}).
			Where("name = ?", inst.Name).
			Updates(map[string]any{
				"token_secret_id": sec.ID,
				"updated_at":      now,
			}).Error; err != nil {
			return fmt.Errorf("wire token_secret_id: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return secretID, nil
}

// UpdateWithOptions writes the slim sonarr_instance row + settings row
// inside a single tx and (unless preserveSecret) upserts the
// instance_secret + re-wires sonarr_instance.token_secret_id. A nil
// cipher is allowed only when preserveSecret.
//
// When ifUnmodifiedSince != nil, the stored sonarr_instance_settings.updated_at
// (second-truncated) is compared inside the tx; strictly-newer stored
// → ports.ErrStaleWrite. Settings is the mutable surface (parent row
// stays stable except for token_secret_id and health stamps); IUS
// reads from settings.updated_at.
func (r *SonarrInstanceRepository) UpdateWithOptions(
	ctx context.Context,
	inst runtime.InstanceSnapshot,
	c *crypto.Cipher,
	preserveSecret bool,
	ifUnmodifiedSince *time.Time,
) error {
	return dbFromContext(ctx, r.db).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()

		var existing database.SonarrInstanceModel
		if err := tx.Where("name = ?", inst.Name).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.Join(
					&sharedErrors.InstanceNotFoundError{Name: domain.InstanceName(inst.Name)},
					ports.ErrNotFound,
				)
			}
			return fmt.Errorf("load instance for update: %w", err)
		}

		var existingSettings database.SonarrInstanceSettingsModel
		settingsErr := tx.Where("instance_name = ?", inst.Name).Take(&existingSettings).Error
		hasExistingSettings := settingsErr == nil
		if settingsErr != nil && !errors.Is(settingsErr, gorm.ErrRecordNotFound) {
			return fmt.Errorf("load instance settings for update: %w", settingsErr)
		}

		// IUS precondition — settings is the mutable surface.
		if ifUnmodifiedSince != nil && hasExistingSettings {
			stored := existingSettings.UpdatedAt.Truncate(time.Second)
			provided := ifUnmodifiedSince.Truncate(time.Second)
			if stored.After(provided) {
				return ports.ErrStaleWrite
			}
		}

		// Patch parent row.
		parent := snapshotToInstanceModel(inst)
		parent.CreatedAt = existing.CreatedAt
		parent.UpdatedAt = now
		// Preserve token_secret_id (we'll update it if we rotate below).
		parent.TokenSecretID = existing.TokenSecretID
		// Preserve health stamps — those are owned by the watchdog
		// recheck loop, not the runtime config PUT path.
		parent.Health = existing.Health
		parent.LastCheckAt = existing.LastCheckAt
		parent.TransitionsCount = existing.TransitionsCount
		if err := tx.Save(&parent).Error; err != nil {
			return fmt.Errorf("update sonarr_instance: %w", err)
		}

		// Patch settings row (create-on-missing).
		settings := snapshotToSettingsModel(inst)
		settings.InstanceName = inst.Name
		settings.UpdatedAt = now
		if hasExistingSettings {
			if err := tx.Save(&settings).Error; err != nil {
				return fmt.Errorf("update sonarr_instance_settings: %w", err)
			}
		} else {
			if err := tx.Create(&settings).Error; err != nil {
				return fmt.Errorf("create sonarr_instance_settings: %w", err)
			}
		}

		if preserveSecret || inst.APIKey == "" {
			return nil
		}
		if c == nil {
			return errors.New("update sonarr instance: cipher required to write api_key")
		}
		ct, err := c.Seal([]byte(inst.APIKey))
		if err != nil {
			return fmt.Errorf("seal api key: %w", err)
		}

		// Upsert instance_secret by (instance_name, secret_name=token).
		var existingSecret database.InstanceSecretModel
		secErr := tx.Where("instance_name = ? AND secret_name = ?",
			inst.Name, instanceTokenSecretName).Take(&existingSecret).Error
		switch {
		case secErr == nil:
			existingSecret.EncryptedValue = ct
			existingSecret.UpdatedAt = now
			if err := tx.Save(&existingSecret).Error; err != nil {
				return fmt.Errorf("update instance_secret: %w", err)
			}
			// token_secret_id already pointed here; nothing else to wire.
		case errors.Is(secErr, gorm.ErrRecordNotFound):
			fresh := database.InstanceSecretModel{
				InstanceName:   inst.Name,
				SecretName:     instanceTokenSecretName,
				EncryptedValue: ct,
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			if err := tx.Create(&fresh).Error; err != nil {
				return fmt.Errorf("create instance_secret: %w", err)
			}
			if err := tx.Model(&database.SonarrInstanceModel{}).
				Where("name = ?", inst.Name).
				Update("token_secret_id", fresh.ID).Error; err != nil {
				return fmt.Errorf("wire token_secret_id: %w", err)
			}
		default:
			return fmt.Errorf("load instance_secret: %w", secErr)
		}

		return nil
	})
}

// Delete hard-deletes sonarr_instance + every app-managed sibling
// keyed by instance name. Explicit DELETEs cover instance_secret,
// sonarr_instance_settings, scan_runs, grab_records, series_cache
// — explicit rather than FK-only because SQLite test fixtures
// don't always enable PRAGMA foreign_keys=ON; the explicit DELETE
// keeps the cascade contract identical across backends.
//
// The legacy `cooldowns`/`decisions`/`origin_releases` tables are not
// in the D-1 schema (D-6 owns their successors); their cascade
// branches will land alongside the D-6 grab+watchdog rewrite.
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

		if err := tx.Where("instance_name = ?", name).
			Delete(&database.ScanRunModel{}).Error; err != nil {
			return fmt.Errorf("delete scan_runs: %w", err)
		}
		if err := tx.Where("instance_name = ?", name).
			Delete(&database.GrabRecordModel{}).Error; err != nil {
			return fmt.Errorf("delete grab_records: %w", err)
		}
		// Hard-delete series_cache rows for this instance. Bypass
		// gorm's soft-delete behaviour because the parent instance is
		// going — there is no point keeping ghost projections.
		if err := tx.Unscoped().Where("instance_name = ?", name).
			Delete(&database.SeriesCacheModel{}).Error; err != nil {
			return fmt.Errorf("delete series_cache: %w", err)
		}
		// Break the cyclic FK before dropping siblings. Postgres
		// rejects the parent DELETE if the SET NULL on token_secret_id
		// races the CASCADE on instance_secret.instance_name; clearing
		// token_secret_id up-front makes the ordering deterministic.
		if err := tx.Model(&database.SonarrInstanceModel{}).
			Where("name = ?", name).
			Update("token_secret_id", nil).Error; err != nil {
			return fmt.Errorf("clear token_secret_id: %w", err)
		}
		if err := tx.Where("instance_name = ?", name).
			Delete(&database.InstanceSecretModel{}).Error; err != nil {
			return fmt.Errorf("delete instance_secret: %w", err)
		}
		if err := tx.Where("instance_name = ?", name).
			Delete(&database.SonarrInstanceSettingsModel{}).Error; err != nil {
			return fmt.Errorf("delete sonarr_instance_settings: %w", err)
		}
		if err := tx.Delete(&m).Error; err != nil {
			return fmt.Errorf("delete instance: %w", err)
		}
		return nil
	})
}

// GetUpdatedAt is the lightweight read used by the HTTP layer for
// Last-Modified / If-Unmodified-Since. Returns the settings.updated_at
// (the mutable surface). Returns ErrNotFound when the instance does
// not exist.
func (r *SonarrInstanceRepository) GetUpdatedAt(ctx context.Context, name string) (time.Time, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	// First confirm the instance exists (so we surface
	// InstanceNotFoundError, not a confusing "no settings row" error).
	var m database.SonarrInstanceModel
	if err := db.Select("name", "updated_at").
		Where("name = ?", name).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return time.Time{}, errors.Join(
				&sharedErrors.InstanceNotFoundError{Name: domain.InstanceName(name)},
				ports.ErrNotFound,
			)
		}
		return time.Time{}, fmt.Errorf("get instance updated_at: %w", err)
	}
	var settings database.SonarrInstanceSettingsModel
	if err := db.Select("instance_name", "updated_at").
		Where("instance_name = ?", name).Take(&settings).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Fall back to the parent row's updated_at — happens for
			// instances inserted directly without a settings row
			// (test fixtures).
			return m.UpdatedAt, nil
		}
		return time.Time{}, fmt.Errorf("get settings updated_at: %w", err)
	}
	return settings.UpdatedAt, nil
}

// ---------------------------------------------------------------------
// model ↔ snapshot conversion helpers
// ---------------------------------------------------------------------

func snapshotToInstanceModel(s runtime.InstanceSnapshot) database.SonarrInstanceModel {
	return database.SonarrInstanceModel{
		Name: s.Name,
		URL:  s.URL,
		Mode: s.Mode,
	}
}

func snapshotToSettingsModel(s runtime.InstanceSnapshot) database.SonarrInstanceSettingsModel {
	tagsInclude, _ := json.Marshal(s.Tags.Include)
	tagsExclude, _ := json.Marshal(s.Tags.Exclude)
	return database.SonarrInstanceSettingsModel{
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
		HealthcheckRecheckAuthSec:     int(s.HealthCheck.RecheckAuth / time.Second),
		HealthcheckRecheckNetSec:      int(s.HealthCheck.RecheckNetwork / time.Second),
		PublicURL:                     s.PublicURL,
		WebhookInstallEnabled:         s.WebhookInstallEnabled,
		WebhookURLOverride:            s.WebhookURLOverride,
		ParseOnGrabEnabled:            s.ParseOnGrabEnabled,
		ScanSkipHandledSeasons:        s.ScanSkipHandledSeasons,
	}
}

func modelToSnapshot(
	m database.SonarrInstanceModel,
	settings database.SonarrInstanceSettingsModel,
	hasSettings bool,
	secretBlob []byte,
	c *crypto.Cipher,
) (runtime.InstanceSnapshot, error) {
	var apiKey string
	if len(secretBlob) > 0 && c != nil {
		plaintext, err := c.Open(secretBlob)
		if err != nil {
			return runtime.InstanceSnapshot{}, fmt.Errorf("decrypt api key: %w", err)
		}
		apiKey = string(plaintext)
	}

	snap := runtime.InstanceSnapshot{
		Name:   m.Name,
		URL:    m.URL,
		APIKey: apiKey,
		Mode:   m.Mode,
	}
	if !hasSettings {
		// Apply runtime defaults so callers get a usable snapshot
		// (e.g. fresh instance whose settings row didn't materialise
		// in a test fixture).
		runtime.ApplyInstanceDefaults(&snap)
		return snap, nil
	}

	var tagsInclude, tagsExclude []string
	_ = json.Unmarshal([]byte(settings.TagsInclude), &tagsInclude)
	_ = json.Unmarshal([]byte(settings.TagsExclude), &tagsExclude)

	snap.Timeout = time.Duration(settings.TimeoutSeconds) * time.Second
	snap.SearchTimeout = time.Duration(settings.SearchTimeoutSeconds) * time.Second
	snap.DryRun = settings.DryRun
	snap.Tags = runtime.TagsSnapshot{
		Mode:    settings.TagsMode,
		Include: tagsInclude,
		Exclude: tagsExclude,
	}
	snap.Search = runtime.SearchSnapshot{
		RequireAllAired:      settings.SearchRequireAllAired,
		SkipSpecials:         settings.SearchSkipSpecials,
		SkipAnime:            settings.SearchSkipAnime,
		MinCustomFormatScore: settings.SearchMinCustomFormatScore,
	}
	snap.Ranking = runtime.RankingSnapshot{
		IndexerPriorityEnabled: settings.RankingIndexerPriorityEnabled,
		OriginBonus:            settings.RankingOriginBonus,
	}
	snap.Limits = runtime.LimitsSnapshot{
		ScanMaxSeries:   settings.LimitsScanMaxSeries,
		MaxGrabsPerScan: settings.LimitsMaxGrabsPerScan,
	}
	snap.RateLimit = runtime.RateLimitSnapshot{
		RPM:   settings.RateLimitRPM,
		Burst: settings.RateLimitBurst,
	}
	snap.Cooldown = runtime.CooldownSnapshot{
		Mode:                  settings.CooldownMode,
		SeriesAfterGrab:       time.Duration(settings.CooldownSeriesAfterGrabSec) * time.Second,
		GUIDAfterFailedGrab:   time.Duration(settings.CooldownGUIDFailedGrabSec) * time.Second,
		GUIDAfterFailedImport: time.Duration(settings.CooldownGUIDFailedImportSec) * time.Second,
	}
	snap.Retry = runtime.RetrySnapshot{
		MaxAttempts:    settings.RetryMaxAttempts,
		InitialBackoff: time.Duration(settings.RetryInitialBackoffSec) * time.Second,
		MaxBackoff:     time.Duration(settings.RetryMaxBackoffSec) * time.Second,
	}
	snap.HealthCheck = runtime.HealthCheckSnapshot{
		RecheckAuth:    time.Duration(settings.HealthcheckRecheckAuthSec) * time.Second,
		RecheckNetwork: time.Duration(settings.HealthcheckRecheckNetSec) * time.Second,
	}
	snap.PublicURL = settings.PublicURL
	snap.WebhookInstallEnabled = settings.WebhookInstallEnabled
	snap.WebhookURLOverride = settings.WebhookURLOverride
	snap.ParseOnGrabEnabled = settings.ParseOnGrabEnabled
	snap.ScanSkipHandledSeasons = settings.ScanSkipHandledSeasons
	return snap, nil
}

var _ ports.SonarrInstanceRepository = (*SonarrInstanceRepository)(nil)
