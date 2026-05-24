// Package runtimeconfig is the application-layer orchestrator for
// GET/PUT /api/v1/config/runtime. It glues validation +
// RuntimeConfigRepository + SonarrInstanceRepository (for rebuilding
// the full Snapshot) + the reload bus into a single set of methods
// the HTTP handler can call without leaking infrastructure types.
package runtimeconfig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

// ValidationError is returned for any pre-save check failure. Code
// matches the wire sentinel the handler will emit.
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

func newValidationErr(field, code, msg string) *ValidationError {
	return &ValidationError{Field: field, Code: code, Message: msg}
}

var (
	ErrStaleWrite = errors.New("runtime_config was modified by another client")
)

const (
	sessionTTLMin = 5 * time.Minute
	sessionTTLMax = 7 * 24 * time.Hour
)

// cronParser matches the parser used in infrastructure/scheduler/cron.go
// so any expression that survives validation here is acceptable to the
// live scheduler after reload.
var cronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

type UseCase struct {
	runtimes  ports.RuntimeConfigRepository
	instances ports.SonarrInstanceRepository
	cipher    *crypto.Cipher
	bus       *runtime.Bus
	logger    *slog.Logger
}

func New(
	runtimes ports.RuntimeConfigRepository,
	instances ports.SonarrInstanceRepository,
	cipher *crypto.Cipher,
	bus *runtime.Bus,
	logger *slog.Logger,
) *UseCase {
	if logger == nil {
		logger = slog.Default()
	}
	return &UseCase{
		runtimes: runtimes, instances: instances,
		cipher: cipher, bus: bus, logger: logger,
	}
}

// Get returns the singleton row as a DTO, falling back to
// runtime.Defaults() when the row is missing. Second-truncated
// updated_at is returned separately so the handler can both serialize
// it in the body AND emit it as Last-Modified.
func (u *UseCase) Get(ctx context.Context) (dto.RuntimeConfigDTO, time.Time, error) {
	row, err := u.runtimes.Get(ctx)
	switch {
	case err == nil:
		// happy path
	case errors.Is(err, ports.ErrNotFound):
		def := runtime.Defaults()
		row = ports.RuntimeConfigRow{
			Cron: def.Cron, Scan: def.Scan, DryRun: def.DryRun,
			GlobalRateLimit: def.GlobalRateLimit, Auth: def.Auth,
		}
	default:
		return dto.RuntimeConfigDTO{}, time.Time{},
			fmt.Errorf("runtimeconfig: get row: %w", err)
	}
	ts := row.UpdatedAt.Truncate(time.Second)
	return rowToDTO(row, ts), ts, nil
}

// Update validates the incoming DTO, persists it, then republishes a
// fresh Snapshot built from (new runtime row + current instances).
// ifUnmodifiedSince (nil = ignore) implements optimistic concurrency.
// The precondition is enforced inside the repo's transaction so two
// concurrent IUS-bearing PUTs cannot both succeed. Returns
// ErrStaleWrite when the stored row's updated_at (second-truncated)
// is strictly newer than the header.
func (u *UseCase) Update(
	ctx context.Context,
	in dto.RuntimeConfigDTO,
	ifUnmodifiedSince *time.Time,
) (dto.RuntimeConfigDTO, time.Time, error) {
	snap, err := u.dtoToSnapshot(in)
	if err != nil {
		return dto.RuntimeConfigDTO{}, time.Time{}, err
	}
	if err := u.runtimes.Upsert(ctx, snap, ifUnmodifiedSince); err != nil {
		if errors.Is(err, ports.ErrStaleWrite) {
			return dto.RuntimeConfigDTO{}, time.Time{}, ErrStaleWrite
		}
		return dto.RuntimeConfigDTO{}, time.Time{},
			fmt.Errorf("runtimeconfig: upsert: %w", err)
	}
	stored, err := u.runtimes.Get(ctx)
	if err != nil {
		return dto.RuntimeConfigDTO{}, time.Time{},
			fmt.Errorf("runtimeconfig: re-read: %w", err)
	}
	if err := u.publish(ctx, stored); err != nil {
		u.logger.WarnContext(ctx, "runtimeconfig.publish_failed",
			slog.String("error", err.Error()))
	}
	ts := stored.UpdatedAt.Truncate(time.Second)
	return rowToDTO(stored, ts), ts, nil
}

func (u *UseCase) publish(ctx context.Context, row ports.RuntimeConfigRow) error {
	insts, err := u.instances.List(ctx, u.cipher)
	if err != nil {
		return fmt.Errorf("list instances: %w", err)
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
	u.logger.InfoContext(ctx, "runtimeconfig.published",
		slog.Int("instance_count", len(insts)))
	return nil
}

// dtoToSnapshot validates every field and parses durations into a
// runtime.Snapshot. Instances stays nil — the Upsert path only writes
// the singleton row.
func (u *UseCase) dtoToSnapshot(in dto.RuntimeConfigDTO) (runtime.Snapshot, error) {
	if _, err := cronParser.Parse(in.Cron.Schedule); err != nil {
		return runtime.Snapshot{}, newValidationErr(
			"cron.schedule", "INVALID_CRON", err.Error())
	}
	jitter, err := parseDuration("cron.jitter", in.Cron.Jitter)
	if err != nil {
		return runtime.Snapshot{}, err
	}
	if jitter < 0 {
		return runtime.Snapshot{}, newValidationErr(
			"cron.jitter", "INVALID_JITTER", "must be >= 0")
	}
	shutdown, err := parseDuration("scan.shutdown_grace", in.Scan.ShutdownGrace)
	if err != nil {
		return runtime.Snapshot{}, err
	}
	if shutdown <= 0 {
		return runtime.Snapshot{}, newValidationErr(
			"scan.shutdown_grace", "INVALID_SCAN_SHUTDOWN_GRACE", "must be > 0")
	}
	sweep, err := parseDuration("scan.cooldown_sweep", in.Scan.CooldownSweep)
	if err != nil {
		return runtime.Snapshot{}, err
	}
	if sweep <= 0 {
		return runtime.Snapshot{}, newValidationErr(
			"scan.cooldown_sweep", "INVALID_SCAN_COOLDOWN_SWEEP", "must be > 0")
	}
	sessionTTL, err := parseDuration("auth.session_ttl", in.Auth.SessionTTL)
	if err != nil {
		return runtime.Snapshot{}, err
	}
	if sessionTTL < sessionTTLMin || sessionTTL > sessionTTLMax {
		return runtime.Snapshot{}, newValidationErr(
			"auth.session_ttl", "INVALID_SESSION_TTL",
			fmt.Sprintf("must be between %s and %s", sessionTTLMin, sessionTTLMax))
	}
	if in.GlobalRateLimit.RPM < 0 || in.GlobalRateLimit.Burst < 0 {
		return runtime.Snapshot{}, newValidationErr(
			"global_rate_limit", "INVALID_RATE_LIMIT",
			"rpm and burst must be >= 0")
	}
	if err := validateTrustedProxies(in.Auth.TrustedProxies); err != nil {
		return runtime.Snapshot{}, err
	}
	return runtime.Snapshot{
		Cron: runtime.CronSnapshot{
			Enabled:  in.Cron.Enabled,
			Schedule: in.Cron.Schedule,
			OnStart:  in.Cron.OnStart,
			Jitter:   jitter,
		},
		Scan: runtime.ScanSnapshot{
			ShutdownGrace: shutdown,
			CooldownSweep: sweep,
		},
		DryRun: in.DryRun,
		GlobalRateLimit: runtime.RateLimitSnapshot{
			RPM: in.GlobalRateLimit.RPM, Burst: in.GlobalRateLimit.Burst,
		},
		Auth: runtime.AuthSnapshot{
			SessionTTL:     sessionTTL,
			SecureCookie:   in.Auth.SecureCookie,
			TrustedProxies: append([]string(nil), in.Auth.TrustedProxies...),
		},
	}, nil
}

func parseDuration(field, raw string) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, newValidationErr(field, "INVALID_DURATION",
			"required (Go-style duration string, e.g. \"30s\")")
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, newValidationErr(field, "INVALID_DURATION", err.Error())
	}
	return d, nil
}

// validateTrustedProxies accepts both bare IPs and CIDRs. Empty list
// is OK — it disables XFF entirely (documented behaviour).
func validateTrustedProxies(list []string) error {
	for _, raw := range list {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			return newValidationErr("auth.trusted_proxies",
				"INVALID_TRUSTED_PROXY", "empty entry not allowed")
		}
		if ip := net.ParseIP(entry); ip != nil {
			continue
		}
		if _, _, err := net.ParseCIDR(entry); err == nil {
			continue
		}
		return newValidationErr("auth.trusted_proxies",
			"INVALID_TRUSTED_PROXY",
			fmt.Sprintf("%q is neither an IP nor a CIDR", entry))
	}
	return nil
}

func rowToDTO(row ports.RuntimeConfigRow, ts time.Time) dto.RuntimeConfigDTO {
	return dto.RuntimeConfigDTO{
		Cron: dto.RuntimeCronDTO{
			Enabled:  row.Cron.Enabled,
			Schedule: row.Cron.Schedule,
			OnStart:  row.Cron.OnStart,
			Jitter:   row.Cron.Jitter.String(),
		},
		Scan: dto.RuntimeScanDTO{
			ShutdownGrace: row.Scan.ShutdownGrace.String(),
			CooldownSweep: row.Scan.CooldownSweep.String(),
		},
		DryRun: row.DryRun,
		GlobalRateLimit: dto.RuntimeRateLimitDTO{
			RPM: row.GlobalRateLimit.RPM, Burst: row.GlobalRateLimit.Burst,
		},
		Auth: dto.RuntimeAuthDTO{
			SessionTTL:     row.Auth.SessionTTL.String(),
			SecureCookie:   row.Auth.SecureCookie,
			TrustedProxies: append([]string(nil), row.Auth.TrustedProxies...),
		},
		AutoGeneratedAPIKey: row.APIKeyAutoGenerated,
		UpdatedAt:           ts.UTC(),
	}
}
