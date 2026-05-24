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

// Field bounds. Each pair (Min, Max) is inclusive on both ends. The
// sentinel `INVALID_<field>_OUT_OF_RANGE` is emitted when a parsed
// value falls outside [Min, Max]. Bounds picked to (a) prevent
// silently-poisonous values (e.g. 100-year cooldown), (b) match
// product reality (e.g. 10k rpm ≈ 167 req/s — well above Sonarr's
// indexer realistic ceiling), and (c) leave room for future tuning
// without re-issuing a schema migration.
const (
	sessionTTLMin = 5 * time.Minute
	sessionTTLMax = 7 * 24 * time.Hour

	scanShutdownGraceMin = 1 * time.Second
	scanShutdownGraceMax = 10 * time.Minute
	scanCooldownSweepMin = 10 * time.Second
	scanCooldownSweepMax = 24 * time.Hour
	cronJitterMin        = time.Duration(0)
	cronJitterMax        = 1 * time.Hour

	rateLimitRPMMin   = 0
	rateLimitRPMMax   = 10000
	rateLimitBurstMin = 0
	rateLimitBurstMax = 10000
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
// runtime.Snapshot. Range checks use the const block above; sentinel
// codes follow `INVALID_<field>_OUT_OF_RANGE` for new fields,
// `INVALID_<field>` for the original three (shipped) sentinels whose
// message text is now range-aware.
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
	if err := boundDuration("cron.jitter", "INVALID_JITTER_OUT_OF_RANGE",
		jitter, cronJitterMin, cronJitterMax); err != nil {
		return runtime.Snapshot{}, err
	}
	shutdown, err := parseDuration("scan.shutdown_grace", in.Scan.ShutdownGrace)
	if err != nil {
		return runtime.Snapshot{}, err
	}
	if err := boundDuration("scan.shutdown_grace",
		"INVALID_SCAN_SHUTDOWN_GRACE_OUT_OF_RANGE",
		shutdown, scanShutdownGraceMin, scanShutdownGraceMax); err != nil {
		return runtime.Snapshot{}, err
	}
	sweep, err := parseDuration("scan.cooldown_sweep", in.Scan.CooldownSweep)
	if err != nil {
		return runtime.Snapshot{}, err
	}
	if err := boundDuration("scan.cooldown_sweep",
		"INVALID_SCAN_COOLDOWN_SWEEP_OUT_OF_RANGE",
		sweep, scanCooldownSweepMin, scanCooldownSweepMax); err != nil {
		return runtime.Snapshot{}, err
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
	if err := boundInt("global_rate_limit.rpm",
		"INVALID_RATE_LIMIT_RPM_OUT_OF_RANGE",
		in.GlobalRateLimit.RPM, rateLimitRPMMin, rateLimitRPMMax); err != nil {
		return runtime.Snapshot{}, err
	}
	if err := boundInt("global_rate_limit.burst",
		"INVALID_RATE_LIMIT_BURST_OUT_OF_RANGE",
		in.GlobalRateLimit.Burst, rateLimitBurstMin, rateLimitBurstMax); err != nil {
		return runtime.Snapshot{}, err
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

// boundDuration returns nil if d ∈ [min, max], a typed
// ValidationError otherwise. The message lists the bounds using
// Duration.String() so the wire format is stable + human-readable.
func boundDuration(field, code string, d, min, max time.Duration) error {
	if d < min || d > max {
		return newValidationErr(field, code,
			fmt.Sprintf("must be between %s and %s", min, max))
	}
	return nil
}

// boundInt mirrors boundDuration for int-valued fields.
func boundInt(field, code string, v, min, max int) error {
	if v < min || v > max {
		return newValidationErr(field, code,
			fmt.Sprintf("must be between %d and %d", min, max))
	}
	return nil
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
