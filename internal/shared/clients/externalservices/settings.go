// Package externalservices owns the runtime config + HTTP client
// factory for the three enrichment sources (TMDB, OMDb, TVDB). The
// data model after D-5 (466c) is one row per service in
// external_service_config + two app_secret rows per service
// (api_key + proxy_pass, both AES-GCM encrypted BYTEA, referenced
// via nullable FK ids). Proxy URL + proxy user are plaintext
// columns (PRD §10.4 threat-model review — a proxy URL without
// creds is not a secret). The factory turns a decrypted Settings
// into a fully wired *http.Client that the Phase C/D clients
// consume via constructor injection — there is no package-global
// state, no init().
package externalservices

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// Service is the enum of supported enrichment sources.
type Service string

const (
	ServiceTMDB Service = "tmdb"
	ServiceOMDB Service = "omdb"
	ServiceTVDB Service = "tvdb"
)

// AllServices is the canonical iteration order used by the snapshot
// build path and the Settings UI list endpoint.
var AllServices = []Service{ServiceTMDB, ServiceOMDB, ServiceTVDB}

// Valid reports whether s is one of the three supported services.
func (s Service) Valid() bool {
	switch s {
	case ServiceTMDB, ServiceOMDB, ServiceTVDB:
		return true
	}
	return false
}

// Outcome classifies the result of a Test() call per PRD §10.4.7.
// The set is closed — callers can switch on it exhaustively.
type Outcome string

const (
	OutcomeOK          Outcome = "ok"
	OutcomeAuthFailed  Outcome = "auth_failed"
	OutcomeNetwork     Outcome = "network"
	OutcomeTimeout     Outcome = "timeout"
	OutcomeProxyFailed Outcome = "proxy_failed"
	OutcomeDNSBlocked  Outcome = "dns_blocked"
)

// Settings is the decrypted runtime view of one service row. All
// optional pointer fields are nil when the source column is NULL or
// the env override didn't supply a value. The factory consumes this
// shape; the repo produces it via Decrypt.
//
// D-5 (466c) — LastTestAt/LastTestOutcome/LastTestMessage are NO
// LONGER persisted in the DB (the legacy `last_test_*` columns were
// dropped per ADR Decision B). The application use case keeps the
// last test result in a sync.Map for the pod lifetime; the fields
// stay on this struct so the use case's `mask()` projection and the
// reload subscriber's plaintext hand-off do not change shape.
type Settings struct {
	Service         Service
	Enabled         bool
	APIKey          string // plaintext, never logged
	APIKeyLast4     string // last 4 chars of APIKey (or env value)
	ProxyURL        string // full URL with scheme; empty = no proxy
	ProxyUsername   string // optional auth user
	ProxyPassword   string // optional auth pass; never logged
	LastTestAt      *time.Time
	LastTestOutcome Outcome
	LastTestMessage string
}

// ErrInvalidService is returned when a Service value falls outside AllServices.
var ErrInvalidService = errors.New("externalservices: invalid service")

// proxyScheme returns the lowercased scheme of ProxyURL ("http",
// "https", "socks4", "socks5"), or "" when ProxyURL is unset.
func (s Settings) proxyScheme() string {
	if s.ProxyURL == "" {
		return ""
	}
	idx := strings.Index(s.ProxyURL, "://")
	if idx <= 0 {
		return ""
	}
	return strings.ToLower(s.ProxyURL[:idx])
}

// Last4 derives the masked-display suffix from a plaintext key. Keys
// shorter than 4 chars yield the whole string — the masked field is
// purely cosmetic, never a security boundary.
func Last4(plaintext string) string {
	if len(plaintext) <= 4 {
		return plaintext
	}
	return plaintext[len(plaintext)-4:]
}

// EnvLookup is the function shape the use case passes into Merge so
// tests can inject a fixture without touching os.Getenv. The real
// implementation is os.Getenv.
type EnvLookup func(name string) string

// FieldSource records the priority origin of one field resolved by
// MergeWithSource. The empty value means the field stayed empty.
type FieldSource string

const (
	// FieldSourceNone means neither env nor db supplied a value.
	FieldSourceNone FieldSource = ""
	// FieldSourceDB means the DB row supplied the final value and no
	// env override was set.
	FieldSourceDB FieldSource = "db"
	// FieldSourceEnv means env supplied the final value (overriding db
	// when both were non-empty, or providing the only value when db
	// was empty — the operator-facing log distinguishes the two via
	// db_was_empty in the future if needed).
	FieldSourceEnv FieldSource = "env"
)

// SourceMap is the per-field origin of one merged Settings. Returned
// alongside Settings by MergeWithSource so the boot subscriber can log
// the resolved priority without re-running the merge or leaking
// plaintext into the record. All four fields default to FieldSourceNone.
type SourceMap struct {
	APIKey        FieldSource
	ProxyURL      FieldSource
	ProxyUsername FieldSource
	ProxyPassword FieldSource
}

// MergeWithSource mirrors Merge but additionally returns the resolved
// priority origin per field. Pure helper — no logging, no plaintext
// crosses the boundary.
//
// Precedence per field (PRD §10.4.4):
//   - env non-empty               → FieldSourceEnv (covers both override
//     and fresh-install fallback paths)
//   - env empty, db non-empty     → FieldSourceDB
//   - both empty                  → FieldSourceNone
func MergeWithSource(svc Service, db Settings, env EnvLookup) (Settings, SourceMap) {
	if env == nil {
		env = func(string) string { return "" }
	}
	out := db
	out.Service = svc
	src := SourceMap{}

	prefix := "SEASONFILL_" + strings.ToUpper(string(svc)) + "_"

	switch token := env(prefix + "TOKEN"); {
	case token != "":
		out.APIKey = token
		out.APIKeyLast4 = Last4(token)
		out.Enabled = true
		src.APIKey = FieldSourceEnv
	case db.APIKey != "":
		src.APIKey = FieldSourceDB
	}

	switch v := env(prefix + "PROXY_URL"); {
	case v != "":
		out.ProxyURL = v
		src.ProxyURL = FieldSourceEnv
	case db.ProxyURL != "":
		src.ProxyURL = FieldSourceDB
	}

	switch v := env(prefix + "PROXY_USER"); {
	case v != "":
		out.ProxyUsername = v
		src.ProxyUsername = FieldSourceEnv
	case db.ProxyUsername != "":
		src.ProxyUsername = FieldSourceDB
	}

	switch v := env(prefix + "PROXY_PASS"); {
	case v != "":
		out.ProxyPassword = v
		src.ProxyPassword = FieldSourceEnv
	case db.ProxyPassword != "":
		src.ProxyPassword = FieldSourceDB
	}

	return out, src
}

// Merge applies the runtime priority rules from PRD §10.4.4: env > DB
// per field. Returns the effective Settings the factory should
// consume. Discards the source map — see MergeWithSource for the
// observability-aware variant. db may be a zero Settings (no row
// present); env may be nil (test fixture).
func Merge(svc Service, db Settings, env EnvLookup) Settings {
	s, _ := MergeWithSource(svc, db, env)
	return s
}

// String implements fmt.Stringer with a redacted shape so accidental
// %v in logs cannot leak secrets. Stable formatter for tests.
func (s Settings) String() string {
	return fmt.Sprintf("Settings{service=%s enabled=%t last4=%q proxy_scheme=%q proxy_host=%q outcome=%s}",
		s.Service, s.Enabled, s.APIKeyLast4, s.proxyScheme(), proxyHost(s.ProxyURL), s.LastTestOutcome)
}

// proxyHost strips credentials and returns "host:port" (or the host
// alone). Returned to slog and the test endpoint response — never the
// full URL with creds.
func proxyHost(raw string) string {
	if raw == "" {
		return ""
	}
	rest := raw
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		rest = rest[i+1:]
	}
	if i := strings.IndexAny(rest, "/?"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// Repository persists external_service_config rows + their two
// app_secret FK-referenced secrets (api_key, proxy_pass). Plaintext
// never crosses this boundary — callers hand in Settings; the repo
// encrypts the two secret fields with cipher.Seal before write and
// decrypts them on read. cipher is required (a nil cipher would
// store plaintext, defeating the threat model).
//
// D-5 (466c) — column shape changed: secrets live in app_secret
// (id BIGSERIAL PK, secret_name TEXT UNIQUE) keyed by the
// "${service}_api_key" / "${service}_proxy_pass" convention;
// external_service_config holds nullable FK ids alongside plaintext
// proxy_url + proxy_user. The last_test_* observability columns
// were dropped per ADR Decision B; MarkTest is now a no-op (the
// use case tracks last-test state in a sync.Map for the pod
// lifetime).
type Repository struct {
	db     *gorm.DB
	cipher *crypto.Cipher
}

func NewRepository(db *gorm.DB, cipher *crypto.Cipher) *Repository {
	return &Repository{db: db, cipher: cipher}
}

// secretNameAPIKey + secretNameProxyPass derive the canonical
// app_secret.secret_name keys per service. Centralised here so the
// Get / List / Upsert paths all agree on the convention.
func secretNameAPIKey(svc Service) string    { return string(svc) + "_api_key" }
func secretNameProxyPass(svc Service) string { return string(svc) + "_proxy_pass" }

// Get returns the row for svc, or ports.ErrNotFound when no
// external_service_config row exists. Decryption errors are wrapped
// — the caller treats them as a fatal config error (master key was
// rotated without a re-encrypt).
func (r *Repository) Get(ctx context.Context, svc Service) (Settings, error) {
	if !svc.Valid() {
		return Settings{}, ErrInvalidService
	}
	var m database.ExternalServiceConfigModel
	err := r.db.WithContext(ctx).
		Where("service_name = ?", string(svc)).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Settings{}, ports.ErrNotFound
		}
		return Settings{}, fmt.Errorf("get external_service_config %s: %w", svc, err)
	}
	out := Settings{
		Service: svc,
		Enabled: m.Enabled,
	}
	if m.ProxyURL != nil {
		out.ProxyURL = *m.ProxyURL
	}
	if m.ProxyUser != nil {
		out.ProxyUsername = *m.ProxyUser
	}
	if m.Last4 != nil {
		out.APIKeyLast4 = *m.Last4
	}
	if m.APIKeySecretID != nil {
		plain, err := r.decryptSecret(ctx, *m.APIKeySecretID)
		if err != nil {
			return Settings{}, fmt.Errorf("decrypt api_key %s: %w", svc, err)
		}
		out.APIKey = plain
	}
	if m.ProxyPassSecretID != nil {
		plain, err := r.decryptSecret(ctx, *m.ProxyPassSecretID)
		if err != nil {
			return Settings{}, fmt.Errorf("decrypt proxy_pass %s: %w", svc, err)
		}
		out.ProxyPassword = plain
	}
	return out, nil
}

// List returns every row in AllServices order. Missing services land
// as zero-value Settings with the Service field populated so the UI
// list endpoint renders all three. The implementation batches secret
// reads (one SELECT * FROM external_service_config + one SELECT * FROM
// app_secret WHERE secret_name IN (...)), eliminating the N+1 join.
func (r *Repository) List(ctx context.Context) ([]Settings, error) {
	var models []database.ExternalServiceConfigModel
	if err := r.db.WithContext(ctx).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list external_service_config: %w", err)
	}
	byService := make(map[Service]database.ExternalServiceConfigModel, len(models))
	wantSecretIDs := make([]uint, 0, 2*len(models))
	for _, m := range models {
		byService[Service(m.ServiceName)] = m
		if m.APIKeySecretID != nil {
			wantSecretIDs = append(wantSecretIDs, *m.APIKeySecretID)
		}
		if m.ProxyPassSecretID != nil {
			wantSecretIDs = append(wantSecretIDs, *m.ProxyPassSecretID)
		}
	}

	// Batch-fetch every needed secret row in a single round-trip.
	secretsByID := make(map[uint][]byte, len(wantSecretIDs))
	if len(wantSecretIDs) > 0 {
		var secrets []database.AppSecretModel
		if err := r.db.WithContext(ctx).
			Where("id IN ?", wantSecretIDs).
			Find(&secrets).Error; err != nil {
			return nil, fmt.Errorf("list app_secret: %w", err)
		}
		for _, s := range secrets {
			secretsByID[s.ID] = s.EncryptedValue
		}
	}

	out := make([]Settings, 0, len(AllServices))
	for _, svc := range AllServices {
		m, ok := byService[svc]
		if !ok {
			out = append(out, Settings{Service: svc})
			continue
		}
		s := Settings{
			Service: svc,
			Enabled: m.Enabled,
		}
		if m.ProxyURL != nil {
			s.ProxyURL = *m.ProxyURL
		}
		if m.ProxyUser != nil {
			s.ProxyUsername = *m.ProxyUser
		}
		if m.Last4 != nil {
			s.APIKeyLast4 = *m.Last4
		}
		if m.APIKeySecretID != nil {
			ct, ok := secretsByID[*m.APIKeySecretID]
			if ok && len(ct) > 0 {
				plain, err := r.cipher.Open(ct)
				if err != nil {
					return nil, fmt.Errorf("decrypt api_key %s: %w", svc, err)
				}
				s.APIKey = string(plain)
			}
		}
		if m.ProxyPassSecretID != nil {
			ct, ok := secretsByID[*m.ProxyPassSecretID]
			if ok && len(ct) > 0 {
				plain, err := r.cipher.Open(ct)
				if err != nil {
					return nil, fmt.Errorf("decrypt proxy_pass %s: %w", svc, err)
				}
				s.ProxyPassword = string(plain)
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// Upsert writes s in a single transaction that touches up to 2
// app_secret rows (api_key + proxy_pass) plus 1 external_service_config
// row. Empty plaintext deletes the matching app_secret row and leaves
// the FK NULL — operator clearing the value through the UI lands the
// same state as a fresh row.
func (r *Repository) Upsert(ctx context.Context, s Settings) error {
	if !s.Service.Valid() {
		return ErrInvalidService
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		apiKeyID, err := r.upsertSecret(tx, secretNameAPIKey(s.Service), s.APIKey)
		if err != nil {
			return fmt.Errorf("upsert api_key secret %s: %w", s.Service, err)
		}
		proxyPassID, err := r.upsertSecret(tx, secretNameProxyPass(s.Service), s.ProxyPassword)
		if err != nil {
			return fmt.Errorf("upsert proxy_pass secret %s: %w", s.Service, err)
		}

		var last4Ptr *string
		if s.APIKeyLast4 != "" {
			v := s.APIKeyLast4
			last4Ptr = &v
		}
		var proxyURLPtr, proxyUserPtr *string
		if s.ProxyURL != "" {
			v := s.ProxyURL
			proxyURLPtr = &v
		}
		if s.ProxyUsername != "" {
			v := s.ProxyUsername
			proxyUserPtr = &v
		}
		m := database.ExternalServiceConfigModel{
			ServiceName:       string(s.Service),
			APIKeySecretID:    apiKeyID,
			Enabled:           s.Enabled,
			ProxyURL:          proxyURLPtr,
			ProxyUser:         proxyUserPtr,
			ProxyPassSecretID: proxyPassID,
			Last4:             last4Ptr,
			UpdatedAt:         time.Now().UTC(),
		}
		// Always emit every mutable column to DoUpdates so an empty
		// plaintext (cleared secret) lands NULL on the FK columns +
		// drops the last4 cosmetic suffix.
		res := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "service_name"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"api_key_secret_id", "enabled", "proxy_url",
				"proxy_user", "proxy_pass_secret_id", "last4",
				"updated_at",
			}),
		}).Create(&m)
		if res.Error != nil {
			return fmt.Errorf("upsert external_service_config %s: %w", s.Service, res.Error)
		}
		return nil
	})
}

// MarkTest is a no-op under the D-5 schema — the last_test_*
// observability columns were dropped per ADR Decision B. Returns
// nil unconditionally; the use case tracks the last test outcome
// in-process for the pod lifetime via its own sync.Map.
func (r *Repository) MarkTest(ctx context.Context, svc Service, _ time.Time, _ Outcome, _ string) error {
	_ = ctx
	if !svc.Valid() {
		return ErrInvalidService
	}
	return nil
}

// decryptSecret loads + Opens a single app_secret row by id. Used by
// Get; List batches the same logic inline to avoid N+1.
func (r *Repository) decryptSecret(ctx context.Context, id uint) (string, error) {
	var s database.AppSecretModel
	if err := r.db.WithContext(ctx).
		Where("id = ?", id).
		First(&s).Error; err != nil {
		return "", err
	}
	if len(s.EncryptedValue) == 0 {
		return "", nil
	}
	b, err := r.cipher.Open(s.EncryptedValue)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// upsertSecret writes (or deletes) the app_secret row for secretName.
// Empty plaintext → row deleted; nil FK id returned (caller sets the
// config column to NULL). Non-empty plaintext → upserted; id returned.
//
// Uses Find→Save/Create instead of clause.OnConflict because the
// unique key here is `secret_name`, not the surrogate PK — and
// ON CONFLICT on a non-PK unique target requires post-create re-read
// on SQLite to surface m.ID. The Find→Save/Create path is portable
// across both backends and produces the same end state.
func (r *Repository) upsertSecret(tx *gorm.DB, secretName, plaintext string) (*uint, error) {
	if plaintext == "" {
		// Operator cleared the value — delete the row so the FK lands NULL.
		if err := tx.Where("secret_name = ?", secretName).
			Delete(&database.AppSecretModel{}).Error; err != nil {
			return nil, fmt.Errorf("delete app_secret %s: %w", secretName, err)
		}
		return nil, nil
	}
	ct, err := r.cipher.Seal([]byte(plaintext))
	if err != nil {
		return nil, fmt.Errorf("seal app_secret %s: %w", secretName, err)
	}
	now := time.Now().UTC()
	var existing database.AppSecretModel
	err = tx.Where("secret_name = ?", secretName).First(&existing).Error
	switch {
	case err == nil:
		existing.EncryptedValue = ct
		existing.UpdatedAt = now
		if err := tx.Save(&existing).Error; err != nil {
			return nil, fmt.Errorf("save app_secret %s: %w", secretName, err)
		}
		id := existing.ID
		return &id, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		row := database.AppSecretModel{
			SecretName:     secretName,
			EncryptedValue: ct,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return nil, fmt.Errorf("create app_secret %s: %w", secretName, err)
		}
		id := row.ID
		return &id, nil
	default:
		return nil, fmt.Errorf("lookup app_secret %s: %w", secretName, err)
	}
}
