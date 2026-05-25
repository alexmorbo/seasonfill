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

// Field bounds. Each pair (Min, Max) is inclusive on both ends.
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

	// trustedProxiesMaxLen caps the list length so a misconfigured
	// caller can't blow the gin XFF parser with arbitrary input.
	trustedProxiesMaxLen = 64
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

// Get returns the singleton row as an Output, falling back to
// runtime.Defaults() when the row is missing. Second-truncated
// updated_at is returned separately so the handler can both serialize
// it in the body AND emit it as Last-Modified.
func (u *UseCase) Get(ctx context.Context) (Output, time.Time, error) {
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
		return Output{}, time.Time{},
			fmt.Errorf("runtimeconfig: get row: %w", err)
	}
	ts := row.UpdatedAt.Truncate(time.Second)
	return rowToOutput(row, ts), ts, nil
}

// Update validates the incoming Input, persists it, then republishes a
// fresh Snapshot built from (new runtime row + current instances).
// ifUnmodifiedSince (nil = ignore) implements optimistic concurrency:
// the IUS pointer is forwarded as-is to the repo's Upsert, where the
// stored vs provided comparison runs INSIDE the same DB transaction
// as the write (no TOCTOU window). `ports.ErrStaleWrite` is
// translated to `runtimeconfig.ErrStaleWrite` so HTTP can map to 412.
func (u *UseCase) Update(
	ctx context.Context,
	in Input,
	ifUnmodifiedSince *time.Time,
) (Output, time.Time, error) {
	snap, err := u.inputToSnapshot(in)
	if err != nil {
		return Output{}, time.Time{}, err
	}
	if err := u.runtimes.Upsert(ctx, snap, ifUnmodifiedSince); err != nil {
		if errors.Is(err, ports.ErrStaleWrite) {
			return Output{}, time.Time{}, ErrStaleWrite
		}
		return Output{}, time.Time{},
			fmt.Errorf("runtimeconfig: upsert: %w", err)
	}
	stored, err := u.runtimes.Get(ctx)
	if err != nil {
		return Output{}, time.Time{},
			fmt.Errorf("runtimeconfig: re-read: %w", err)
	}
	if err := u.publish(ctx, stored); err != nil {
		// Publish failure must not roll back the DB write — subscribers
		// can rebuild from the next publish. Log + continue.
		u.logger.WarnContext(ctx, "runtimeconfig.publish_failed",
			slog.String("error", err.Error()))
	}
	ts := stored.UpdatedAt.Truncate(time.Second)
	return rowToOutput(stored, ts), ts, nil
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

// inputToSnapshot validates every field and converts a typed Input to a
// runtime.Snapshot. Instances stays nil — the Upsert path only writes
// the singleton row. Duration values are already parsed by the caller.
func (u *UseCase) inputToSnapshot(in Input) (runtime.Snapshot, error) {
	if _, err := cronParser.Parse(in.Cron.Schedule); err != nil {
		return runtime.Snapshot{}, newValidationErr(
			"cron.schedule", "INVALID_CRON", err.Error())
	}
	if err := boundDuration("cron.jitter", "INVALID_JITTER_OUT_OF_RANGE",
		in.Cron.Jitter, cronJitterMin, cronJitterMax); err != nil {
		return runtime.Snapshot{}, err
	}
	if err := boundDuration("scan.shutdown_grace",
		"INVALID_SCAN_SHUTDOWN_GRACE_OUT_OF_RANGE",
		in.Scan.ShutdownGrace, scanShutdownGraceMin, scanShutdownGraceMax); err != nil {
		return runtime.Snapshot{}, err
	}
	if err := boundDuration("scan.cooldown_sweep",
		"INVALID_SCAN_COOLDOWN_SWEEP_OUT_OF_RANGE",
		in.Scan.CooldownSweep, scanCooldownSweepMin, scanCooldownSweepMax); err != nil {
		return runtime.Snapshot{}, err
	}
	if in.Auth.SessionTTL < sessionTTLMin || in.Auth.SessionTTL > sessionTTLMax {
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
			Jitter:   in.Cron.Jitter,
		},
		Scan: runtime.ScanSnapshot{
			ShutdownGrace: in.Scan.ShutdownGrace,
			CooldownSweep: in.Scan.CooldownSweep,
		},
		DryRun: in.DryRun,
		GlobalRateLimit: runtime.RateLimitSnapshot{
			RPM: in.GlobalRateLimit.RPM, Burst: in.GlobalRateLimit.Burst,
		},
		Auth: runtime.AuthSnapshot{
			SessionTTL:     in.Auth.SessionTTL,
			SecureCookie:   in.Auth.SecureCookie,
			TrustedProxies: append([]string(nil), in.Auth.TrustedProxies...),
		},
	}, nil
}

// boundDuration returns nil if d ∈ [min, max], a typed
// ValidationError otherwise.
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
//
// Rejects entries that span the entire address space (0.0.0.0, ::,
// 0.0.0.0/0, ::/0) — accepting them would trust every client header
// and defeat the proxy allow-list. Also caps list length at
// trustedProxiesMaxLen so a misconfigured caller can't blow the gin
// XFF parser with arbitrary input.
func validateTrustedProxies(list []string) error {
	if len(list) > trustedProxiesMaxLen {
		return newValidationErr("auth.trusted_proxies",
			"INVALID_TRUSTED_PROXIES_TOO_MANY",
			fmt.Sprintf("at most %d entries allowed", trustedProxiesMaxLen))
	}
	for _, raw := range list {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			return newValidationErr("auth.trusted_proxies",
				"INVALID_TRUSTED_PROXY", "empty entry not allowed")
		}
		if ip := net.ParseIP(entry); ip != nil {
			if ip.IsUnspecified() {
				return newValidationErr("auth.trusted_proxies",
					"INVALID_TRUSTED_PROXY_TOO_BROAD",
					fmt.Sprintf("%q matches the entire address space", entry))
			}
			continue
		}
		if _, ipnet, err := net.ParseCIDR(entry); err == nil {
			ones, _ := ipnet.Mask.Size()
			if ones == 0 {
				return newValidationErr("auth.trusted_proxies",
					"INVALID_TRUSTED_PROXY_TOO_BROAD",
					fmt.Sprintf("%q matches the entire address space", entry))
			}
			continue
		}
		return newValidationErr("auth.trusted_proxies",
			"INVALID_TRUSTED_PROXY",
			fmt.Sprintf("%q is neither an IP nor a CIDR", entry))
	}
	return nil
}

func rowToOutput(row ports.RuntimeConfigRow, ts time.Time) Output {
	proxies := append([]string(nil), row.Auth.TrustedProxies...)
	if proxies == nil {
		proxies = []string{}
	}
	return Output{
		Cron: CronInput{
			Enabled:  row.Cron.Enabled,
			Schedule: row.Cron.Schedule,
			OnStart:  row.Cron.OnStart,
			Jitter:   row.Cron.Jitter,
		},
		Scan: ScanInput{
			ShutdownGrace: row.Scan.ShutdownGrace,
			CooldownSweep: row.Scan.CooldownSweep,
		},
		DryRun: row.DryRun,
		GlobalRateLimit: GlobalRateLimitInput{
			RPM: row.GlobalRateLimit.RPM, Burst: row.GlobalRateLimit.Burst,
		},
		Auth: AuthInput{
			SessionTTL:     row.Auth.SessionTTL,
			SecureCookie:   row.Auth.SecureCookie,
			TrustedProxies: proxies,
		},
		AutoGeneratedAPIKey: row.APIKeyAutoGenerated,
		UpdatedAt:           ts.UTC(),
	}
}
