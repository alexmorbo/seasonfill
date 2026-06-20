// Package externalservices owns the runtime config + HTTP client
// factory for the three enrichment sources (TMDB, OMDb, TVDB). The
// data model is one row per service in external_service_settings
// (AES-GCM encrypted secrets). The factory turns a decrypted Settings
// into a fully wired *http.Client that the future Phase C/D clients
// will consume via constructor injection — there is no
// package-global state, no init().
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

// Repository persists external_service_settings rows. Plaintext never
// crosses this boundary — callers hand in Settings; the repo encrypts
// the four *_enc fields with cipher.Seal before write and decrypts
// them on read. cipher is required (Settings without a cipher would
// store plaintext, which would defeat the threat model).
type Repository struct {
	db     *gorm.DB
	cipher *crypto.Cipher
}

func NewRepository(db *gorm.DB, cipher *crypto.Cipher) *Repository {
	return &Repository{db: db, cipher: cipher}
}

// Get returns the row for svc, or ports.ErrNotFound when no row
// exists. Decryption errors are wrapped — the caller is expected to
// treat a decryption failure as a fatal config error (master key was
// rotated without a re-write).
func (r *Repository) Get(ctx context.Context, svc Service) (Settings, error) {
	if !svc.Valid() {
		return Settings{}, ErrInvalidService
	}
	var m database.ExternalServiceSettingsModel
	err := r.db.WithContext(ctx).Where("service = ?", string(svc)).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Settings{}, ports.ErrNotFound
		}
		return Settings{}, fmt.Errorf("get external service %s: %w", svc, err)
	}
	return r.decrypt(m)
}

// List returns every row in the table in AllServices order. Missing
// services are returned as a zero-value Settings with the Service
// field populated, so callers can render the UI list without a second
// trip.
func (r *Repository) List(ctx context.Context) ([]Settings, error) {
	var models []database.ExternalServiceSettingsModel
	if err := r.db.WithContext(ctx).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list external services: %w", err)
	}
	byService := make(map[Service]database.ExternalServiceSettingsModel, len(models))
	for _, m := range models {
		byService[Service(m.Service)] = m
	}
	out := make([]Settings, 0, len(AllServices))
	for _, svc := range AllServices {
		m, ok := byService[svc]
		if !ok {
			out = append(out, Settings{Service: svc})
			continue
		}
		s, err := r.decrypt(m)
		if err != nil {
			return nil, fmt.Errorf("list external services: %w", err)
		}
		out = append(out, s)
	}
	return out, nil
}

// Upsert writes s to the DB. The four secret fields are encrypted in
// place. created_at is preserved on update via Clauses(DoUpdates).
func (r *Repository) Upsert(ctx context.Context, s Settings) error {
	if !s.Service.Valid() {
		return ErrInvalidService
	}
	apiKeyEnc, err := r.sealOptional(s.APIKey)
	if err != nil {
		return fmt.Errorf("encrypt api_key: %w", err)
	}
	proxyURLEnc, err := r.sealOptional(s.ProxyURL)
	if err != nil {
		return fmt.Errorf("encrypt proxy_url: %w", err)
	}
	proxyUserEnc, err := r.sealOptional(s.ProxyUsername)
	if err != nil {
		return fmt.Errorf("encrypt proxy_username: %w", err)
	}
	proxyPassEnc, err := r.sealOptional(s.ProxyPassword)
	if err != nil {
		return fmt.Errorf("encrypt proxy_password: %w", err)
	}
	now := time.Now().UTC()
	var last4Ptr *string
	if s.APIKeyLast4 != "" {
		v := s.APIKeyLast4
		last4Ptr = &v
	}
	var outcomePtr, msgPtr *string
	if s.LastTestOutcome != "" {
		o := string(s.LastTestOutcome)
		outcomePtr = &o
	}
	if s.LastTestMessage != "" {
		m := s.LastTestMessage
		msgPtr = &m
	}
	model := database.ExternalServiceSettingsModel{
		Service:          string(s.Service),
		Enabled:          s.Enabled,
		APIKeyEnc:        apiKeyEnc,
		APIKeyLast4:      last4Ptr,
		ProxyURLEnc:      proxyURLEnc,
		ProxyUsernameEnc: proxyUserEnc,
		ProxyPasswordEnc: proxyPassEnc,
		LastTestAt:       s.LastTestAt,
		LastTestOutcome:  outcomePtr,
		LastTestMessage:  msgPtr,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "service"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"enabled", "api_key_enc", "api_key_last4",
			"proxy_url_enc", "proxy_username_enc", "proxy_password_enc",
			"last_test_at", "last_test_outcome", "last_test_message",
			"updated_at",
		}),
	}).Create(&model)
	if res.Error != nil {
		return fmt.Errorf("upsert external service %s: %w", s.Service, res.Error)
	}
	return nil
}

// MarkTest persists the outcome of a Test() run without touching the
// secret/proxy columns. Used by the /test endpoint to avoid round-
// tripping plaintext through Upsert.
func (r *Repository) MarkTest(ctx context.Context, svc Service, at time.Time, outcome Outcome, message string) error {
	if !svc.Valid() {
		return ErrInvalidService
	}
	updates := map[string]any{
		"last_test_at":      at.UTC(),
		"last_test_outcome": string(outcome),
		"last_test_message": message,
		"updated_at":        time.Now().UTC(),
	}
	res := r.db.WithContext(ctx).
		Model(&database.ExternalServiceSettingsModel{}).
		Where("service = ?", string(svc)).
		Updates(updates)
	if res.Error != nil {
		return fmt.Errorf("mark test %s: %w", svc, res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

func (r *Repository) sealOptional(plaintext string) ([]byte, error) {
	if plaintext == "" {
		return nil, nil
	}
	return r.cipher.Seal([]byte(plaintext))
}

func (r *Repository) openOptional(ct []byte) (string, error) {
	if len(ct) == 0 {
		return "", nil
	}
	b, err := r.cipher.Open(ct)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *Repository) decrypt(m database.ExternalServiceSettingsModel) (Settings, error) {
	apiKey, err := r.openOptional(m.APIKeyEnc)
	if err != nil {
		return Settings{}, fmt.Errorf("decrypt api_key: %w", err)
	}
	proxyURL, err := r.openOptional(m.ProxyURLEnc)
	if err != nil {
		return Settings{}, fmt.Errorf("decrypt proxy_url: %w", err)
	}
	proxyUser, err := r.openOptional(m.ProxyUsernameEnc)
	if err != nil {
		return Settings{}, fmt.Errorf("decrypt proxy_username: %w", err)
	}
	proxyPass, err := r.openOptional(m.ProxyPasswordEnc)
	if err != nil {
		return Settings{}, fmt.Errorf("decrypt proxy_password: %w", err)
	}
	s := Settings{
		Service:       Service(m.Service),
		Enabled:       m.Enabled,
		APIKey:        apiKey,
		ProxyURL:      proxyURL,
		ProxyUsername: proxyUser,
		ProxyPassword: proxyPass,
		LastTestAt:    m.LastTestAt,
	}
	if m.APIKeyLast4 != nil {
		s.APIKeyLast4 = *m.APIKeyLast4
	}
	if m.LastTestOutcome != nil {
		s.LastTestOutcome = Outcome(*m.LastTestOutcome)
	}
	if m.LastTestMessage != nil {
		s.LastTestMessage = *m.LastTestMessage
	}
	return s, nil
}
