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
	"regexp"
	"strings"
	"time"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

var (
	ErrValidation    = errors.New("validation failed")
	ErrDuplicateName = errors.New("instance name already exists")
	ErrLastInstance  = errors.New("cannot delete the last remaining instance")
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
}

func New(
	instances ports.SonarrInstanceRepository,
	runtimes ports.RuntimeConfigRepository,
	cipher *crypto.Cipher,
	bus *runtime.Bus,
	logger *slog.Logger,
) *UseCase {
	if logger == nil {
		logger = slog.Default()
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
	return u.publish(ctx)
}

// Update applies changes to an existing row, optionally preserving
// the stored api_key when newSnap.APIKey is empty. ifUnmodifiedSince
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
	preserveSecret := strings.TrimSpace(newSnap.APIKey) == ""
	if err := u.instances.UpdateWithOptions(ctx, newSnap, u.cipher, preserveSecret, ifUnmodifiedSince); err != nil {
		if errors.Is(err, ports.ErrStaleWrite) {
			return ErrStaleWrite
		}
		return fmt.Errorf("update instance: %w", err)
	}
	return u.publish(ctx)
}

// Delete enforces the LAST_INSTANCE guard then hard-deletes the row
// + cascaded history. Publishes after success.
func (u *UseCase) Delete(ctx context.Context, name string) error {
	if _, err := u.instances.GetByName(ctx, name, u.cipher); err != nil {
		return err
	}
	count, err := u.instances.Count(ctx)
	if err != nil {
		return fmt.Errorf("count instances: %w", err)
	}
	if count <= 1 {
		return ErrLastInstance
	}
	if err := u.instances.Delete(ctx, name); err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}
	return u.publish(ctx)
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
	if strings.TrimSpace(s.URL) == "" {
		return newValidationErr("url", "INVALID_INSTANCE_URL",
			"url is required")
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
