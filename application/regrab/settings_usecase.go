package regrab

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

// Sentinels — the HTTP handler maps these to wire codes via
// errors.Is. New sentinel = new wire code, so each one carries an
// explicit `ErrCode...` constant the handler reads.
var (
	ErrValidation          = errors.New("validation failed")
	ErrWebhookNotInstalled = errors.New("OnGrab webhook is not installed in Sonarr")
	ErrWebhookCheckFailed  = errors.New("OnGrab webhook installation check failed")
)

// ValidationError carries a per-field code that the HTTP handler
// surfaces verbatim. Wraps ErrValidation so legacy `errors.Is(err,
// ErrValidation)` callers keep working.
type ValidationError struct {
	Field   string
	Code    string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func (e *ValidationError) Unwrap() error { return ErrValidation }

func newValidationErr(field, code, msg string) *ValidationError {
	return &ValidationError{Field: field, Code: code, Message: msg}
}

// QbitSettingsView is the application-layer projection the HTTP
// handler renders. PasswordSet substitutes for the never-returned
// plaintext. PasswordEncrypted is intentionally absent — the bytes
// stay inside the use case and the repo.
type QbitSettingsView struct {
	ID                     uint
	InstanceID             uint
	InstanceName           string
	Enabled                bool
	URL                    string
	Username               string
	PasswordSet            bool
	Category               string
	PollIntervalMinutes    int
	RegrabCooldownHours    int
	MaxConsecutiveNoBetter int
	CustomUnregisteredMsgs []string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// UpsertInput is the unvalidated, plaintext input the handler hands
// the use case. Password=="" means "keep existing ciphertext on
// update / persist nil on create (anon auth)". The use case is the
// only place the plaintext lives in memory.
type UpsertInput struct {
	Enabled                bool
	URL                    string
	Username               string
	Password               string
	Category               string
	PollIntervalMinutes    int
	RegrabCooldownHours    int
	MaxConsecutiveNoBetter int
	CustomUnregisteredMsgs []string
}

// Field bounds — mirror the parent 039 §Architecture-decisions and
// PRD § Defaults. Picked to reject obvious operator typos without
// being so tight that real-world configurations bounce.
const (
	pollIntervalMinutesMin = 5
	pollIntervalMinutesMax = 1440 // 24h

	regrabCooldownHoursMin = 1
	regrabCooldownHoursMax = 720 // 30d

	maxConsecutiveNoBetterMin = 1
	maxConsecutiveNoBetterMax = 100

	customUnregisteredMsgsMaxCount  = 100
	customUnregisteredMsgsEntryMin  = 3
	customUnregisteredMsgsEntryMax  = 200
	customUnregisteredMsgsAggMaxLen = 16 << 10 // 16 KiB total — sanity cap

	categoryMaxLen = 128
	urlMaxLen      = 512
	usernameMaxLen = 256
)

// UseCase orchestrates the qBit settings CRUD against the repo,
// the encryption helper, the parent-instance lookup, and the
// webhook gate. Construction is intentionally minimal — additional
// dependencies (TestConnection probe, audit logger) can be wired
// via With...() builders in later stories without breaking the
// existing constructor signature.
type UseCase struct {
	settings  ports.QbitSettingsRepository
	instances ports.SonarrInstanceRepository
	cipher    *crypto.Cipher
	webhooks  WebhookChecker
	logger    *slog.Logger
	now       func() time.Time
}

func NewUseCase(
	settings ports.QbitSettingsRepository,
	instances ports.SonarrInstanceRepository,
	cipher *crypto.Cipher,
	logger *slog.Logger,
) *UseCase {
	if logger == nil {
		logger = slog.Default()
	}
	return &UseCase{
		settings:  settings,
		instances: instances,
		cipher:    cipher,
		webhooks:  nullWebhookChecker{},
		logger:    logger,
		now:       time.Now,
	}
}

// WithWebhookChecker swaps the null gate for the production checker
// once 039e/039g land. Returning the use case keeps the cmd/server
// wiring fluent.
func (u *UseCase) WithWebhookChecker(c WebhookChecker) *UseCase {
	if c == nil {
		c = nullWebhookChecker{}
	}
	u.webhooks = c
	return u
}

// WithClock is the test-time hook to fix time.Now without monkey-
// patching. Production callers don't touch this.
func (u *UseCase) WithClock(now func() time.Time) *UseCase {
	if now != nil {
		u.now = now
	}
	return u
}

// GetByInstanceName resolves the parent instance, then reads the
// settings row. ports.ErrNotFound surfaces both "instance not found"
// (handler maps to INSTANCE_NOT_FOUND) and "settings not found"
// (handler maps to QBIT_SETTINGS_NOT_FOUND). The two are
// distinguished by which call returned the error.
func (u *UseCase) GetByInstanceName(ctx context.Context, name string) (QbitSettingsView, error) {
	inst, err := u.instances.GetByName(ctx, name, u.cipher)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return QbitSettingsView{}, fmt.Errorf("instance %q: %w", name, ports.ErrNotFound)
		}
		return QbitSettingsView{}, fmt.Errorf("resolve instance %q: %w", name, err)
	}
	rec, err := u.settings.GetByInstance(ctx, inst.ID)
	if err != nil {
		return QbitSettingsView{}, err
	}
	return recordToView(rec, name), nil
}

// Upsert validates, encrypts (if non-empty password), runs the
// webhook gate (if enabled-transition), and persists. On the dirty-
// bit path (empty password on update), the existing ciphertext is
// preserved verbatim. The returned view is sourced from the
// freshly-persisted row so the handler can echo it back as the PUT
// body.
func (u *UseCase) Upsert(ctx context.Context, name string, in UpsertInput) (QbitSettingsView, error) {
	if err := validate(in); err != nil {
		return QbitSettingsView{}, err
	}
	inst, err := u.instances.GetByName(ctx, name, u.cipher)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return QbitSettingsView{}, fmt.Errorf("instance %q: %w", name, ports.ErrNotFound)
		}
		return QbitSettingsView{}, fmt.Errorf("resolve instance %q: %w", name, err)
	}

	existing, getErr := u.settings.GetByInstance(ctx, inst.ID)
	hasExisting := getErr == nil
	if getErr != nil && !errors.Is(getErr, ports.ErrNotFound) {
		return QbitSettingsView{}, fmt.Errorf("read existing settings: %w", getErr)
	}

	// Webhook gate (parent C-3 / D62). Only run when the operator is
	// flipping enabled true — either on a fresh row or on an
	// existing row that was previously false.
	if in.Enabled {
		needsGate := !hasExisting || !existing.Enabled
		if needsGate {
			installed, werr := u.webhooks.IsInstalled(ctx, name)
			if werr != nil {
				return QbitSettingsView{}, fmt.Errorf("%w: %w", ErrWebhookCheckFailed, werr)
			}
			if !installed {
				return QbitSettingsView{}, ErrWebhookNotInstalled
			}
		}
	}

	// Password encryption — only when plaintext is non-empty.
	// Dirty-bit pattern: empty plaintext on update preserves the
	// existing ciphertext; on create it persists nil (anon auth).
	var encrypted []byte
	switch {
	case in.Password != "":
		blob, err := u.cipher.Seal([]byte(in.Password))
		if err != nil {
			// The plaintext is in `in.Password` here; we never include
			// it in the wrapped error message. The slog line below
			// only carries a stable string.
			u.logger.ErrorContext(ctx, "qbit_settings.encrypt_failed",
				slog.String("instance", name))
			return QbitSettingsView{}, fmt.Errorf("encrypt password: %w", err)
		}
		encrypted = blob
	case hasExisting:
		encrypted = existing.PasswordEncrypted // preserve
	default:
		encrypted = nil // anon
	}

	usernamePtr := normaliseUsername(in.Username)
	msgs := normaliseMsgs(in.CustomUnregisteredMsgs)
	now := u.now().UTC()

	rec := ports.QbitSettingsRecord{
		InstanceID:             inst.ID,
		Enabled:                in.Enabled,
		URL:                    strings.TrimSpace(in.URL),
		Username:               usernamePtr,
		PasswordEncrypted:      encrypted,
		Category:               strings.TrimSpace(in.Category),
		PollIntervalMinutes:    in.PollIntervalMinutes,
		RegrabCooldownHours:    in.RegrabCooldownHours,
		MaxConsecutiveNoBetter: in.MaxConsecutiveNoBetter,
		CustomUnregisteredMsgs: msgs,
		UpdatedAt:              now,
	}
	if hasExisting {
		rec.ID = existing.ID
		rec.CreatedAt = existing.CreatedAt
	} else {
		rec.CreatedAt = now
	}

	if err := u.settings.Upsert(ctx, rec); err != nil {
		return QbitSettingsView{}, fmt.Errorf("upsert settings: %w", err)
	}

	stored, err := u.settings.GetByInstance(ctx, inst.ID)
	if err != nil {
		return QbitSettingsView{}, fmt.Errorf("reload settings: %w", err)
	}
	return recordToView(stored, name), nil
}

// Delete removes the settings row. ports.ErrNotFound flows through
// when there is no row.
func (u *UseCase) Delete(ctx context.Context, name string) error {
	inst, err := u.instances.GetByName(ctx, name, u.cipher)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return fmt.Errorf("instance %q: %w", name, ports.ErrNotFound)
		}
		return fmt.Errorf("resolve instance %q: %w", name, err)
	}
	if err := u.settings.DeleteByInstance(ctx, inst.ID); err != nil {
		return err
	}
	return nil
}

// DecryptPassword is the read-side helper the future regrab loop
// (039f) uses to feed the qBit client. It is NOT exposed through
// any HTTP handler — the handler never asks for plaintext. Returns
// ("", nil) for rows with nil/empty ciphertext (anon auth) and
// surfaces the cipher.Open error verbatim otherwise.
//
// Kept on the use case (rather than a free function) so the cipher
// reference is encapsulated.
func (u *UseCase) DecryptPassword(rec ports.QbitSettingsRecord) (string, error) {
	if len(rec.PasswordEncrypted) == 0 {
		return "", nil
	}
	pt, err := u.cipher.Open(rec.PasswordEncrypted)
	if err != nil {
		return "", fmt.Errorf("decrypt qbit password: %w", err)
	}
	return string(pt), nil
}

// recordToView projects the repo record into the masked view the
// HTTP handler returns. Username is rendered as "" when the stored
// pointer is nil so the JSON omitempty path collapses it. PasswordSet
// is sourced from the ciphertext length so an explicit Seal of empty
// bytes is reported as "not set" (the Seal path is gated on len>0
// anyway; this is defence-in-depth).
func recordToView(rec ports.QbitSettingsRecord, name string) QbitSettingsView {
	username := ""
	if rec.Username != nil {
		username = *rec.Username
	}
	msgs := rec.CustomUnregisteredMsgs
	if msgs == nil {
		msgs = []string{}
	}
	return QbitSettingsView{
		ID:                     rec.ID,
		InstanceID:             rec.InstanceID,
		InstanceName:           name,
		Enabled:                rec.Enabled,
		URL:                    rec.URL,
		Username:               username,
		PasswordSet:            len(rec.PasswordEncrypted) > 0,
		Category:               rec.Category,
		PollIntervalMinutes:    rec.PollIntervalMinutes,
		RegrabCooldownHours:    rec.RegrabCooldownHours,
		MaxConsecutiveNoBetter: rec.MaxConsecutiveNoBetter,
		CustomUnregisteredMsgs: msgs,
		CreatedAt:              rec.CreatedAt,
		UpdatedAt:              rec.UpdatedAt,
	}
}

func normaliseUsername(s string) *string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// normaliseMsgs trims + lowercases each entry, drops empties post-
// trim, dedups while preserving order, and caps the slice. Bounds
// enforcement happens in validate() before this is called.
func normaliseMsgs(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		s := strings.ToLower(strings.TrimSpace(raw))
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// validate runs all input bounds before any DB read. Order: URL
// first (cheapest failure), category, intervals, then the custom
// messages slice. Each branch returns a typed ValidationError the
// handler maps verbatim to wire BAD_REQUEST.
func validate(in UpsertInput) error {
	if err := validateURL(in.URL); err != nil {
		return err
	}
	if err := validateCategory(in.Category); err != nil {
		return err
	}
	if err := validateUsername(in.Username); err != nil {
		return err
	}
	if err := boundInt("poll_interval_minutes", "INVALID_POLL_INTERVAL",
		in.PollIntervalMinutes, pollIntervalMinutesMin, pollIntervalMinutesMax); err != nil {
		return err
	}
	if err := boundInt("regrab_cooldown_hours", "INVALID_REGRAB_COOLDOWN",
		in.RegrabCooldownHours, regrabCooldownHoursMin, regrabCooldownHoursMax); err != nil {
		return err
	}
	if err := boundInt("max_consecutive_no_better", "INVALID_MAX_CONSECUTIVE",
		in.MaxConsecutiveNoBetter, maxConsecutiveNoBetterMin, maxConsecutiveNoBetterMax); err != nil {
		return err
	}
	if err := validateCustomMsgs(in.CustomUnregisteredMsgs); err != nil {
		return err
	}
	return nil
}

func validateURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return newValidationErr("url", "INVALID_QBIT_URL", "url is required")
	}
	if len(trimmed) > urlMaxLen {
		return newValidationErr("url", "INVALID_QBIT_URL",
			fmt.Sprintf("must be <= %d chars", urlMaxLen))
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return newValidationErr("url", "INVALID_QBIT_URL", "malformed url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return newValidationErr("url", "INVALID_QBIT_URL",
			"scheme must be http or https")
	}
	if u.Host == "" {
		return newValidationErr("url", "INVALID_QBIT_URL", "host is required")
	}
	if u.User != nil {
		return newValidationErr("url", "INVALID_QBIT_URL",
			"userinfo not allowed in url")
	}
	return nil
}

func validateCategory(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return newValidationErr("category", "INVALID_QBIT_CATEGORY",
			"category is required")
	}
	if len(trimmed) > categoryMaxLen {
		return newValidationErr("category", "INVALID_QBIT_CATEGORY",
			fmt.Sprintf("must be <= %d chars", categoryMaxLen))
	}
	return nil
}

func validateUsername(raw string) error {
	if len(strings.TrimSpace(raw)) > usernameMaxLen {
		return newValidationErr("username", "INVALID_QBIT_USERNAME",
			fmt.Sprintf("must be <= %d chars", usernameMaxLen))
	}
	return nil
}

func validateCustomMsgs(msgs []string) error {
	if len(msgs) > customUnregisteredMsgsMaxCount {
		return newValidationErr("custom_unregistered_msgs",
			"INVALID_CUSTOM_MSGS",
			fmt.Sprintf("max %d entries", customUnregisteredMsgsMaxCount))
	}
	total := 0
	for i, raw := range msgs {
		trimmed := strings.TrimSpace(raw)
		// Skip empty entries (they'll be dropped by normaliseMsgs)
		if len(trimmed) == 0 {
			continue
		}
		if len(trimmed) < customUnregisteredMsgsEntryMin ||
			len(trimmed) > customUnregisteredMsgsEntryMax {
			return newValidationErr("custom_unregistered_msgs",
				"INVALID_CUSTOM_MSGS",
				fmt.Sprintf("entry %d length must be in [%d,%d]",
					i, customUnregisteredMsgsEntryMin,
					customUnregisteredMsgsEntryMax))
		}
		total += len(trimmed)
		if total > customUnregisteredMsgsAggMaxLen {
			return newValidationErr("custom_unregistered_msgs",
				"INVALID_CUSTOM_MSGS",
				fmt.Sprintf("aggregate length must be <= %d bytes",
					customUnregisteredMsgsAggMaxLen))
		}
	}
	return nil
}

func boundInt(field, code string, v, min, max int) error {
	if v < min || v > max {
		return newValidationErr(field, code,
			fmt.Sprintf("must be between %d and %d", min, max))
	}
	return nil
}

// Ensure the runtime + crypto imports stay referenced even if a
// future refactor drops one of the bounds-check call sites — the
// runtime import is used by the validator's URL shape, and crypto
// by the cipher field. (Compile-time guard.)
var _ = runtime.SortInstances
var _ = (&crypto.Cipher{})
