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

var nameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

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
// ErrValidation for bad inputs.
func (u *UseCase) Create(ctx context.Context, snap runtime.InstanceSnapshot) error {
	if err := validate(snap, true); err != nil {
		return err
	}
	runtime.ApplyInstanceDefaults(&snap)
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
// (nil = ignore) implements optimistic concurrency. The precondition
// check runs inside the same DB transaction as the write so two
// concurrent IUS-bearing PUTs cannot both succeed — one returns
// ErrStaleWrite. The repo compares at second resolution to match the
// RFC1123 Last-Modified header (1-second precision gap is documented
// on the handler godoc).
func (u *UseCase) Update(
	ctx context.Context,
	name string,
	newSnap runtime.InstanceSnapshot,
	ifUnmodifiedSince *time.Time,
) error {
	if newSnap.Name != name {
		return ErrNameImmutable
	}
	if err := validate(newSnap, false); err != nil {
		return err
	}
	existing, err := u.instances.GetByName(ctx, name, u.cipher)
	if err != nil {
		return err
	}
	newSnap.ID = existing.ID
	runtime.ApplyInstanceDefaults(&newSnap)
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
func validate(s runtime.InstanceSnapshot, requireAPIKey bool) error {
	if !nameRE.MatchString(s.Name) {
		return fmt.Errorf("%w: name must match ^[a-zA-Z0-9_-]{1,128}$", ErrValidation)
	}
	if strings.TrimSpace(s.URL) == "" {
		return fmt.Errorf("%w: url is required", ErrValidation)
	}
	if requireAPIKey && strings.TrimSpace(s.APIKey) == "" {
		return fmt.Errorf("%w: api_key is required", ErrValidation)
	}
	if s.Mode != "" && s.Mode != "auto" && s.Mode != "manual" {
		return fmt.Errorf("%w: mode must be one of auto, manual", ErrValidation)
	}
	if s.Timeout < 0 || s.SearchTimeout < 0 {
		return fmt.Errorf("%w: timeout / search_timeout must be >= 0", ErrValidation)
	}
	if s.RateLimit.RPM < 0 || s.RateLimit.Burst < 0 {
		return fmt.Errorf("%w: rate_limit_rpm / rate_limit_burst must be >= 0", ErrValidation)
	}
	if s.Cooldown.SeriesAfterGrab < 0 ||
		s.Cooldown.GUIDAfterFailedGrab < 0 ||
		s.Cooldown.GUIDAfterFailedImport < 0 {
		return fmt.Errorf("%w: cooldown durations must be >= 0", ErrValidation)
	}
	if s.Retry.MaxAttempts < 0 || s.Retry.InitialBackoff < 0 || s.Retry.MaxBackoff < 0 {
		return fmt.Errorf("%w: retry fields must be >= 0", ErrValidation)
	}
	if s.HealthCheck.RecheckAuth < 0 || s.HealthCheck.RecheckNetwork < 0 {
		return fmt.Errorf("%w: health_check intervals must be >= 0", ErrValidation)
	}
	return nil
}
