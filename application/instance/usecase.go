// Package instance is the application-layer orchestrator for the
// HTTP CRUD on sonarr_instance rows. It glues the repo + the cipher
// + the reload bus into a single set of methods the HTTP handler
// can call without leaking infrastructure types.
package instance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

var (
	ErrValidation    = errors.New("validation failed")
	ErrDuplicateName = errors.New("instance name already exists")
	ErrNameImmutable = errors.New("renaming an instance is not supported")
	ErrStaleWrite    = errors.New("instance was modified by another client")
	ErrNotFound      = ports.ErrNotFound
)

// ValidationError carries a per-field sentinel code for the HTTP
// layer. It wraps ErrValidation so legacy `errors.Is(err,
// ErrValidation)` callers keep working unchanged.
type ValidationError struct {
	Field   string
	Code    string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: %s", e.Field, e.Message)
	}
	return e.Message
}

func (e *ValidationError) Unwrap() error { return ErrValidation }

func newValidationErr(field, code, msg string) *ValidationError {
	return &ValidationError{Field: field, Code: code, Message: msg}
}

// Field bounds for sonarr_instance rows. Same inclusive-on-both-ends
// convention as application/runtimeconfig. Bounds picked to match
// realistic Sonarr deployments (a 300s API timeout is already very
// generous; a 600s search timeout covers the worst-case Prowlarr
// fan-out).
const (
	instanceTimeoutMin       = 1 * time.Second
	instanceTimeoutMax       = 300 * time.Second
	instanceSearchTimeoutMin = 1 * time.Second
	instanceSearchTimeoutMax = 600 * time.Second

	instanceCooldownMin = time.Duration(0)
	instanceCooldownMax = 168 * time.Hour // 7d

	instanceRetryMaxAttemptsMin = 0
	instanceRetryMaxAttemptsMax = 10
	instanceRetryBackoffMin     = time.Duration(0)
	instanceRetryBackoffMax     = 1 * time.Hour

	instanceHealthIntervalMin = 10 * time.Second
	instanceHealthIntervalMax = 24 * time.Hour

	instanceRateLimitRPMMin   = 0
	instanceRateLimitRPMMax   = 10000
	instanceRateLimitBurstMin = 0
	instanceRateLimitBurstMax = 10000

	instanceMinCustomFormatScoreMin = -1000
	instanceMinCustomFormatScoreMax = 1000
	instanceScanMaxSeriesMin        = 0
	instanceScanMaxSeriesMax        = 100000
	instanceMaxGrabsPerScanMin      = 0
	instanceMaxGrabsPerScanMax      = 100
	instanceOriginBonusMin          = -100.0
	instanceOriginBonusMax          = 100.0

	// instanceURLMaxLen mirrors the GORM size:512 column on
	// SonarrInstanceModel.URL — reject longer at the application
	// layer so the DB driver never has to truncate.
	instanceURLMaxLen = 512
)

var nameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

var reservedNames = map[string]bool{
	"test": true,
}

type UseCase struct {
	instances ports.SonarrInstanceRepository
	runtimes  ports.RuntimeConfigRepository
	cipher    *crypto.Cipher
	bus       *runtime.Bus
	logger    *slog.Logger
	now       func() time.Time

	// webhookReconciler + webhookCache are nilable so tests not
	// concerned with webhook side effects can construct the use case
	// without wiring infra. Production main.go installs both.
	webhookReconciler WebhookReconciler
	webhookCache      WebhookCacheCleanup
}

// WebhookReconciler is the narrow subset of
// application/webhookinstall.Reconciler the use case needs. Defined
// as an interface here (returning `any` instead of Status) so the
// use case has zero direct import of webhookinstall — keeps the
// package graph hub-and-spoke (webhookinstall depends on sonarr;
// instance must NOT).
type WebhookReconciler interface {
	Reconcile(ctx context.Context, instanceName domain.InstanceName) (any, error)
	HandleInstanceDeleted(ctx context.Context, instanceName domain.InstanceName)
}

// WebhookCacheCleanup is the narrow subset of StatusCache used on
// instance delete. Currently unused (HandleInstanceDeleted already
// purges) — kept for forward compat with 041d / 041h.
type WebhookCacheCleanup interface {
	Delete(name string)
}

// WithWebhookReconciler — production wiring setter.
func (u *UseCase) WithWebhookReconciler(r WebhookReconciler) *UseCase {
	u.webhookReconciler = r
	return u
}

// WithWebhookStatusCache — production wiring setter.
func (u *UseCase) WithWebhookStatusCache(c WebhookCacheCleanup) *UseCase {
	u.webhookCache = c
	return u
}

func New(
	instances ports.SonarrInstanceRepository,
	runtimes ports.RuntimeConfigRepository,
	cipher *crypto.Cipher,
	bus *runtime.Bus,
	logger *slog.Logger,
) *UseCase {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "admin")
	}
	return &UseCase{
		instances: instances, runtimes: runtimes,
		cipher: cipher, bus: bus, logger: logger, now: time.Now,
	}
}

// Get returns the masked detail and the row's updated_at. The
// returned snapshot's APIKey is wiped to "***" before being handed
// back so the caller can never accidentally serialize the plaintext.
func (u *UseCase) Get(ctx context.Context, name string) (runtime.InstanceSnapshot, time.Time, error) {
	snap, err := u.instances.GetByName(ctx, name, u.cipher)
	if err != nil {
		return runtime.InstanceSnapshot{}, time.Time{}, err
	}
	ts, err := u.instances.GetUpdatedAt(ctx, name)
	if err != nil {
		return runtime.InstanceSnapshot{}, time.Time{}, err
	}
	snap.APIKey = "***"
	return snap, ts.Truncate(time.Second), nil
}

// Create persists a new instance row + secret, then republishes a
// fresh Snapshot. Returns ErrDuplicateName if name is already taken,
// ErrValidation (typed) for bad inputs.
//
// Defaults are applied BEFORE validation so a DTO that omits optional
// blocks (e.g. health_check) goes through validation with the
// default-filled values. Bound checks only reject EXPLICIT out-of-
// range inputs — they never reject "zero means use the default".
func (u *UseCase) Create(ctx context.Context, snap runtime.InstanceSnapshot) error {
	runtime.ApplyInstanceDefaults(&snap)
	if err := validate(snap, true); err != nil {
		return err
	}
	if _, err := u.instances.GetByName(ctx, snap.Name, u.cipher); err == nil {
		return ErrDuplicateName
	} else if !errors.Is(err, ports.ErrNotFound) {
		return fmt.Errorf("check duplicate name: %w", err)
	}
	if _, err := u.instances.Create(ctx, snap, u.cipher); err != nil {
		return fmt.Errorf("create instance: %w", err)
	}
	if err := u.publish(ctx); err != nil {
		return err
	}
	u.tryReconcileWebhook(ctx, snap.Name)
	return nil
}

// Update applies changes to an existing row, optionally preserving
// the stored api_key when newSnap.APIKey is empty OR matches a UI
// placeholder shape (defense-in-depth against a frontend regression
// that leaks a masked value back to the server). ifUnmodifiedSince
// (nil = ignore) implements optimistic concurrency.
//
// Defaults are applied BEFORE validation (see Create godoc for the
// rationale). Bound checks always run against default-filled values.
func (u *UseCase) Update(
	ctx context.Context,
	name string,
	newSnap runtime.InstanceSnapshot,
	ifUnmodifiedSince *time.Time,
) error {
	if newSnap.Name != name {
		return ErrNameImmutable
	}
	runtime.ApplyInstanceDefaults(&newSnap)
	if err := validate(newSnap, false); err != nil {
		return err
	}
	existing, err := u.instances.GetByName(ctx, name, u.cipher)
	if err != nil {
		return err
	}
	newSnap.ID = existing.ID
	trimmed := strings.TrimSpace(newSnap.APIKey)
	preserveSecret := trimmed == ""
	if !preserveSecret && isPlaceholderAPIKey(trimmed) {
		// Real Sonarr API keys are 32 lowercase hex characters. Any
		// shorter input or any input composed entirely of mask glyphs
		// is the masked GET response leaking back through a buggy
		// client. Treat as preserve and emit a structured warning so
		// the regression is visible in logs without breaking the
		// user's save.
		u.logger.WarnContext(ctx, "instance.update.suspicious_api_key_preserved",
			slog.String("instance", name),
			slog.Int("len", len(trimmed)),
		)
		preserveSecret = true
		newSnap.APIKey = ""
	}
	if err := u.instances.UpdateWithOptions(ctx, newSnap, u.cipher, preserveSecret, ifUnmodifiedSince); err != nil {
		if errors.Is(err, ports.ErrStaleWrite) {
			return ErrStaleWrite
		}
		return fmt.Errorf("update instance: %w", err)
	}
	if err := u.publish(ctx); err != nil {
		return err
	}
	u.tryReconcileWebhook(ctx, name)
	return nil
}

// isPlaceholderAPIKey returns true for values that cannot be a real
// Sonarr API key and look like a UI placeholder leaking through. A
// real Sonarr v3 API key is exactly 32 lowercase hex characters
// (sha1.Sum / RandomNumberGenerator output); anything shorter than 16
// bytes OR composed entirely of typical mask glyphs ('*', '•', '·')
// is rejected. The 16-byte floor leaves slack for future Sonarr key
// formats while still catching every common masked shape ('***',
// '••••••••', '********', etc.).
//
// This is intentionally permissive on the upper bound: we don't want
// to reject a legitimate non-Sonarr key shape if the user point this
// at a fork. Lower-bound rejection is the only safety net.
func isPlaceholderAPIKey(v string) bool {
	if len(v) < 16 {
		return true
	}
	for _, r := range v {
		switch r {
		case '*', '•', '·': // '*', '•', '·'
			continue
		default:
			return false
		}
	}
	return true
}

// Delete hard-deletes the row + cascaded history. Publishes after success.
func (u *UseCase) Delete(ctx context.Context, name string) error {
	if _, err := u.instances.GetByName(ctx, name, u.cipher); err != nil {
		return err
	}
	if err := u.instances.Delete(ctx, name); err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}
	if err := u.publish(ctx); err != nil {
		return err
	}
	if u.webhookReconciler != nil {
		u.webhookReconciler.HandleInstanceDeleted(ctx, domain.InstanceName(name))
	} else if u.webhookCache != nil {
		u.webhookCache.Delete(name)
	}
	return nil
}

func (u *UseCase) publish(ctx context.Context) error {
	row, err := u.runtimes.Get(ctx)
	if err != nil {
		return fmt.Errorf("reload runtime row: %w", err)
	}
	insts, err := u.instances.List(ctx, u.cipher)
	if err != nil {
		return fmt.Errorf("reload instances: %w", err)
	}
	for i := range insts {
		runtime.ApplyInstanceDefaults(&insts[i])
	}
	runtime.SortInstances(insts)
	snap := runtime.Snapshot{
		Cron: row.Cron, Scan: row.Scan, DryRun: row.DryRun,
		GlobalRateLimit: row.GlobalRateLimit, Auth: row.Auth,
		Instances: insts,
	}
	if u.bus != nil {
		u.bus.Publish(ctx, snap)
	}
	u.logger.InfoContext(ctx, "instance.crud.published",
		slog.Int("instance_count", len(insts)))
	return nil
}

// validate runs the create/update field rules. requireAPIKey is true
// on Create (api_key is required) and false on Update (empty = keep).
//
// Bound checks reject EXACTLY the cases ApplyInstanceDefaults would
// have rewritten — a user must intentionally state "I want the
// default" by omitting the field at the DTO layer or sending zero.
// Sending an explicit out-of-range value is always rejected.
//
// One subtlety: Timeout / SearchTimeout zero is rejected because the
// default-applier silently rewrites to 10s / 60s and the user loses
// the ability to see THEIR value round-trip. The HTTP layer treats
// zero as "use default" by omitting the field at DTO serialization,
// not by writing 0.
func validate(s runtime.InstanceSnapshot, requireAPIKey bool) error {
	if !nameRE.MatchString(s.Name) {
		return newValidationErr("name", "INVALID_INSTANCE_NAME",
			"must match ^[a-zA-Z0-9_-]{1,128}$")
	}
	if reservedNames[strings.ToLower(s.Name)] {
		return newValidationErr("name", "INVALID_INSTANCE_NAME_RESERVED",
			fmt.Sprintf("name %q is reserved", s.Name))
	}
	if err := validateInstanceURL(s.URL); err != nil {
		return err
	}
	if requireAPIKey && strings.TrimSpace(s.APIKey) == "" {
		return newValidationErr("api_key", "INVALID_INSTANCE_API_KEY",
			"api_key is required")
	}
	if s.Mode != "" && s.Mode != "auto" && s.Mode != "manual" {
		return newValidationErr("mode", "INVALID_INSTANCE_MODE",
			"mode must be one of auto, manual")
	}
	if err := boundDuration("timeout_sec",
		"INVALID_INSTANCE_TIMEOUT_OUT_OF_RANGE",
		s.Timeout, instanceTimeoutMin, instanceTimeoutMax); err != nil {
		return err
	}
	if err := boundDuration("search_timeout_sec",
		"INVALID_INSTANCE_SEARCH_TIMEOUT_OUT_OF_RANGE",
		s.SearchTimeout, instanceSearchTimeoutMin, instanceSearchTimeoutMax); err != nil {
		return err
	}
	if err := boundInt("rate_limit_rpm",
		"INVALID_INSTANCE_RATE_LIMIT_RPM_OUT_OF_RANGE",
		s.RateLimit.RPM, instanceRateLimitRPMMin, instanceRateLimitRPMMax); err != nil {
		return err
	}
	if err := boundInt("rate_limit_burst",
		"INVALID_INSTANCE_RATE_LIMIT_BURST_OUT_OF_RANGE",
		s.RateLimit.Burst, instanceRateLimitBurstMin, instanceRateLimitBurstMax); err != nil {
		return err
	}
	if err := boundDuration("cooldown.series_after_grab",
		"INVALID_INSTANCE_COOLDOWN_SERIES_OUT_OF_RANGE",
		s.Cooldown.SeriesAfterGrab, instanceCooldownMin, instanceCooldownMax); err != nil {
		return err
	}
	if err := boundDuration("cooldown.guid_after_failed_grab",
		"INVALID_INSTANCE_COOLDOWN_GUID_GRAB_OUT_OF_RANGE",
		s.Cooldown.GUIDAfterFailedGrab, instanceCooldownMin, instanceCooldownMax); err != nil {
		return err
	}
	if err := boundDuration("cooldown.guid_after_failed_import",
		"INVALID_INSTANCE_COOLDOWN_GUID_IMPORT_OUT_OF_RANGE",
		s.Cooldown.GUIDAfterFailedImport, instanceCooldownMin, instanceCooldownMax); err != nil {
		return err
	}
	if err := boundInt("retry.max_attempts",
		"INVALID_INSTANCE_RETRY_MAX_ATTEMPTS_OUT_OF_RANGE",
		s.Retry.MaxAttempts, instanceRetryMaxAttemptsMin, instanceRetryMaxAttemptsMax); err != nil {
		return err
	}
	if err := boundDuration("retry.initial_backoff",
		"INVALID_INSTANCE_RETRY_INITIAL_BACKOFF_OUT_OF_RANGE",
		s.Retry.InitialBackoff, instanceRetryBackoffMin, instanceRetryBackoffMax); err != nil {
		return err
	}
	if err := boundDuration("retry.max_backoff",
		"INVALID_INSTANCE_RETRY_MAX_BACKOFF_OUT_OF_RANGE",
		s.Retry.MaxBackoff, instanceRetryBackoffMin, instanceRetryBackoffMax); err != nil {
		return err
	}
	if err := boundDuration("health_check.recheck_auth",
		"INVALID_INSTANCE_HEALTH_RECHECK_AUTH_OUT_OF_RANGE",
		s.HealthCheck.RecheckAuth, instanceHealthIntervalMin, instanceHealthIntervalMax); err != nil {
		return err
	}
	if err := boundDuration("health_check.recheck_network",
		"INVALID_INSTANCE_HEALTH_RECHECK_NET_OUT_OF_RANGE",
		s.HealthCheck.RecheckNetwork, instanceHealthIntervalMin, instanceHealthIntervalMax); err != nil {
		return err
	}
	if err := boundInt("search.min_custom_format_score",
		"INVALID_INSTANCE_MIN_CUSTOM_FORMAT_SCORE_OUT_OF_RANGE",
		s.Search.MinCustomFormatScore,
		instanceMinCustomFormatScoreMin, instanceMinCustomFormatScoreMax); err != nil {
		return err
	}
	if err := boundInt("limits.scan_max_series",
		"INVALID_INSTANCE_SCAN_MAX_SERIES_OUT_OF_RANGE",
		s.Limits.ScanMaxSeries, instanceScanMaxSeriesMin, instanceScanMaxSeriesMax); err != nil {
		return err
	}
	if err := boundInt("limits.max_grabs_per_scan",
		"INVALID_INSTANCE_MAX_GRABS_PER_SCAN_OUT_OF_RANGE",
		s.Limits.MaxGrabsPerScan, instanceMaxGrabsPerScanMin, instanceMaxGrabsPerScanMax); err != nil {
		return err
	}
	if err := boundFloat("ranking.origin_bonus",
		"INVALID_INSTANCE_ORIGIN_BONUS_OUT_OF_RANGE",
		s.Ranking.OriginBonus, instanceOriginBonusMin, instanceOriginBonusMax); err != nil {
		return err
	}
	if err := validateOptionalPublicURL(
		"public_url", "INVALID_INSTANCE_PUBLIC_URL", s.PublicURL); err != nil {
		return err
	}
	if err := validateOptionalPublicURL(
		"webhook_url_override", "INVALID_INSTANCE_WEBHOOK_URL_OVERRIDE",
		s.WebhookURLOverride); err != nil {
		return err
	}
	return nil
}

// validateInstanceURL enforces scheme allow-list (http/https only),
// rejects embedded userinfo (api_key lives in instance_secret, not in
// the URL), and caps length at the model's column width.
func validateInstanceURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return newValidationErr("url", "INVALID_INSTANCE_URL",
			"url is required")
	}
	if len(trimmed) > instanceURLMaxLen {
		return newValidationErr("url", "INVALID_INSTANCE_URL_SCHEME",
			fmt.Sprintf("must be <= %d chars", instanceURLMaxLen))
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return newValidationErr("url", "INVALID_INSTANCE_URL_SCHEME",
			"malformed url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return newValidationErr("url", "INVALID_INSTANCE_URL_SCHEME",
			"scheme must be http or https")
	}
	if u.Host == "" {
		return newValidationErr("url", "INVALID_INSTANCE_URL_SCHEME",
			"host is required")
	}
	if u.User != nil {
		return newValidationErr("url", "INVALID_INSTANCE_URL_SCHEME",
			"userinfo not allowed in url")
	}
	return nil
}

func boundDuration(field, code string, d, min, max time.Duration) error {
	if d < min || d > max {
		return newValidationErr(field, code,
			fmt.Sprintf("must be between %s and %s", min, max))
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

// boundFloat mirrors boundInt for float64 fields and additionally
// rejects NaN/Inf — those slip through naive < / > comparisons
// (NaN compares false to everything).
func boundFloat(field, code string, v, min, max float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return newValidationErr(field, code, "must be a finite number")
	}
	if v < min || v > max {
		return newValidationErr(field, code,
			fmt.Sprintf("must be between %g and %g", min, max))
	}
	return nil
}

// validateOptionalPublicURL is the validator for the Phase 11
// `public_url` and `webhook_url_override` columns. A nil pointer means
// "no override" and is always accepted. A non-nil pointer must be:
//   - non-empty after TrimSpace,
//   - <= instanceURLMaxLen,
//   - parseable as a URL with scheme http/https,
//   - non-empty Host,
//   - no userinfo,
//   - no trailing slash on the path (the reconciler in 041c appends its
//     own path segment and a double `/` would break Sonarr's webhook
//     URL matching).
//
// The empty-string case rejects with the same code so a client cannot
// downgrade `{"public_url":""}` into a silent no-op. To clear the
// override the client must omit the JSON key (nil → no change at the
// snapshot level; the HTTP layer translates accordingly).
func validateOptionalPublicURL(field, code string, p *string) error {
	if p == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*p)
	if trimmed == "" {
		return newValidationErr(field, code,
			"must be a non-empty http(s) URL or omitted")
	}
	if len(trimmed) > instanceURLMaxLen {
		return newValidationErr(field, code,
			fmt.Sprintf("must be <= %d chars", instanceURLMaxLen))
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return newValidationErr(field, code, "malformed url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return newValidationErr(field, code, "scheme must be http or https")
	}
	if u.Host == "" {
		return newValidationErr(field, code, "host is required")
	}
	if u.User != nil {
		return newValidationErr(field, code, "userinfo not allowed in url")
	}
	if strings.HasSuffix(trimmed, "/") {
		return newValidationErr(field, code,
			"must not end with a trailing slash")
	}
	return nil
}

// tryReconcileWebhook runs the sync reconciler with a tight timeout so
// a slow Sonarr does not stall the HTTP request. Failures are logged
// at WARN and never propagated — the instance row is already saved
// and the cache carries the LastError so the next GET /webhook/status
// renders an "install failed" badge. publish() must have succeeded
// before calling this so the registry lookup sees the new row.
func (u *UseCase) tryReconcileWebhook(ctx context.Context, name string) {
	if u.webhookReconciler == nil {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := u.webhookReconciler.Reconcile(rctx, domain.InstanceName(name)); err != nil {
		u.logger.WarnContext(ctx, "instance.crud.webhook_reconcile_failed",
			slog.String("instance", name), slog.String("error", err.Error()))
	}
}
