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
	// localNetworksMaxLen caps the per-request CIDR allow-list. Larger
	// values blow the per-request linear scan on the bypass hot path.
	localNetworksMaxLen = 64
)

var cronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

type UseCase struct {
	runtimes  ports.RuntimeConfigRepository
	instances ports.SonarrInstanceRepository
	cipher    *crypto.Cipher
	bus       *runtime.Bus
	logger    *slog.Logger
	now       func() time.Time
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
		now: time.Now,
	}
}

// WithClock swaps the wall-clock source used to compute new SessionEpoch
// values on mode/bypass/networks changes. Tests use this to assert
// monotonic bump behaviour without race-prone real-time sleeps.
func (u *UseCase) WithClock(now func() time.Time) *UseCase {
	if now != nil {
		u.now = now
	}
	return u
}

// Get returns the singleton row as an Output, falling back to
// runtime.Defaults() when the row is missing.
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
// fresh Snapshot. Bumps SessionEpoch when any auth-invalidating field
// changes (Mode / LocalBypass / LocalNetworks). The bump uses the
// usecase clock (UnixNano) which is monotonic across normal real-world
// gaps and stable in tests.
func (u *UseCase) Update(
	ctx context.Context,
	in Input,
	ifUnmodifiedSince *time.Time,
) (Output, time.Time, error) {
	// Read the existing row first so we can decide whether to bump the
	// epoch. ErrNotFound → treat as defaults (first ever PUT).
	prevRow, err := u.runtimes.Get(ctx)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return Output{}, time.Time{},
			fmt.Errorf("runtimeconfig: pre-read: %w", err)
	}
	prevEpoch := prevRow.Auth.SessionEpoch

	snap, err := u.inputToSnapshot(in)
	if err != nil {
		return Output{}, time.Time{}, err
	}
	// Decide epoch: keep previous unless an invalidating field changed.
	if epochShouldBump(prevRow.Auth, snap.Auth) {
		next := u.now().UTC().UnixNano()
		if next <= prevEpoch {
			next = prevEpoch + 1
		}
		snap.Auth.SessionEpoch = next
	} else {
		snap.Auth.SessionEpoch = prevEpoch
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
		u.logger.WarnContext(ctx, "runtimeconfig.publish_failed",
			slog.String("error", err.Error()))
	}
	ts := stored.UpdatedAt.Truncate(time.Second)
	return rowToOutput(stored, ts), ts, nil
}

// SetAuthMode is a focused rescue path used by the CLI: read the row,
// switch Mode, bump epoch, write. Does NOT consult If-Unmodified-Since
// (the operator is explicitly overriding). Returns the new epoch so the
// caller can log it.
func (u *UseCase) SetAuthMode(ctx context.Context, mode string) (int64, error) {
	if !validAuthMode(mode) {
		return 0, newValidationErr("auth.mode", "INVALID_AUTH_MODE",
			fmt.Sprintf("must be one of forms|basic|none|oidc, got %q", mode))
	}
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
		return 0, fmt.Errorf("runtimeconfig: get row: %w", err)
	}
	next := u.now().UTC().UnixNano()
	if next <= row.Auth.SessionEpoch {
		next = row.Auth.SessionEpoch + 1
	}
	snap := runtime.Snapshot{
		Cron: row.Cron, Scan: row.Scan, DryRun: row.DryRun,
		GlobalRateLimit: row.GlobalRateLimit,
		Auth: runtime.AuthSnapshot{
			SessionTTL:     row.Auth.SessionTTL,
			SecureCookie:   row.Auth.SecureCookie,
			TrustedProxies: append([]string(nil), row.Auth.TrustedProxies...),
			Mode:           mode,
			LocalBypass:    row.Auth.LocalBypass,
			LocalNetworks:  append([]string(nil), row.Auth.LocalNetworks...),
			SessionEpoch:   next,
			OIDC: runtime.OIDCSnapshot{
				Issuer:        row.Auth.OIDC.Issuer,
				ClientID:      row.Auth.OIDC.ClientID,
				RedirectURL:   row.Auth.OIDC.RedirectURL,
				Scopes:        append([]string(nil), row.Auth.OIDC.Scopes...),
				UsernameClaim: row.Auth.OIDC.UsernameClaim,
				AllowedGroups: append([]string(nil), row.Auth.OIDC.AllowedGroups...),
			},
		},
	}
	if err := u.runtimes.Upsert(ctx, snap, nil); err != nil {
		return 0, fmt.Errorf("runtimeconfig: upsert: %w", err)
	}
	stored, gerr := u.runtimes.Get(ctx)
	if gerr == nil {
		if perr := u.publish(ctx, stored); perr != nil {
			u.logger.WarnContext(ctx, "runtimeconfig.publish_failed",
				slog.String("error", perr.Error()))
		}
	}
	return next, nil
}

func epochShouldBump(prev, next runtime.AuthSnapshot) bool {
	if prev.Mode != next.Mode {
		return true
	}
	if prev.LocalBypass != next.LocalBypass {
		return true
	}
	if !stringSliceEqual(prev.LocalNetworks, next.LocalNetworks) {
		return true
	}
	if prev.OIDC.Issuer != next.OIDC.Issuer ||
		prev.OIDC.ClientID != next.OIDC.ClientID ||
		prev.OIDC.RedirectURL != next.OIDC.RedirectURL ||
		prev.OIDC.UsernameClaim != next.OIDC.UsernameClaim {
		return true
	}
	if !stringSliceEqual(prev.OIDC.Scopes, next.OIDC.Scopes) {
		return true
	}
	if !stringSliceEqual(prev.OIDC.AllowedGroups, next.OIDC.AllowedGroups) {
		return true
	}
	return false
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
	if !validAuthMode(in.Auth.Mode) {
		return runtime.Snapshot{}, newValidationErr(
			"auth.mode", "INVALID_AUTH_MODE",
			fmt.Sprintf("must be one of forms|basic|none|oidc, got %q", in.Auth.Mode))
	}
	cleanedNetworks, err := validateLocalNetworks(in.Auth.LocalNetworks)
	if err != nil {
		return runtime.Snapshot{}, err
	}
	if err := validateOIDCInput(in.Auth.OIDC, in.Auth.Mode); err != nil {
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
			Mode:           in.Auth.Mode,
			LocalBypass:    in.Auth.LocalBypass,
			LocalNetworks:  cleanedNetworks,
			OIDC: runtime.OIDCSnapshot{
				Issuer:        strings.TrimSpace(in.Auth.OIDC.Issuer),
				ClientID:      strings.TrimSpace(in.Auth.OIDC.ClientID),
				RedirectURL:   strings.TrimSpace(in.Auth.OIDC.RedirectURL),
				Scopes:        append([]string(nil), in.Auth.OIDC.Scopes...),
				UsernameClaim: strings.TrimSpace(in.Auth.OIDC.UsernameClaim),
				AllowedGroups: append([]string(nil), in.Auth.OIDC.AllowedGroups...),
			},
		},
	}, nil
}

func validAuthMode(m string) bool {
	switch m {
	case runtime.AuthModeForms, runtime.AuthModeBasic, runtime.AuthModeNone, runtime.AuthModeOIDC:
		return true
	default:
		return false
	}
}

// validateLocalNetworks accepts CIDRs only — bare IPs are intentionally
// rejected (callers should write `127.0.0.1/32`). Returns the cleaned
// list (trimmed + deduplicated by canonical CIDR string) so the caller
// can persist canonical form instead of raw user input.
//
// Rules:
//   - len ≤ localNetworksMaxLen (DoS bound on the bypass hot path)
//   - each entry trimmed; empty entries rejected
//   - net.ParseCIDR must succeed; the *canonical* form (ipnet.String())
//     is what we dedupe on, so " 10.0.0.0/8 " and "10.0.0.0/8"
//     collapse to one entry
//   - mixed IPv4 / IPv6 allowed
func validateLocalNetworks(list []string) ([]string, error) {
	if len(list) > localNetworksMaxLen {
		return nil, newValidationErr("auth.local_networks",
			"INVALID_LOCAL_NETWORKS_TOO_MANY",
			fmt.Sprintf("at most %d entries allowed", localNetworksMaxLen))
	}
	seen := make(map[string]struct{}, len(list))
	out := make([]string, 0, len(list))
	for _, raw := range list {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			return nil, newValidationErr("auth.local_networks",
				"INVALID_LOCAL_NETWORK", "empty entry not allowed")
		}
		_, ipnet, err := net.ParseCIDR(entry)
		if err != nil {
			return nil, newValidationErr("auth.local_networks",
				"INVALID_LOCAL_NETWORK",
				fmt.Sprintf("%q is not a valid CIDR: %s", entry, err.Error()))
		}
		canon := ipnet.String()
		if _, dup := seen[canon]; dup {
			continue
		}
		seen[canon] = struct{}{}
		out = append(out, canon)
	}
	return out, nil
}

// validateOIDCInput enforces minimum invariants. Fields are only
// strictly required when auth_mode=oidc; otherwise empty values are
// accepted (operator may pre-fill before switching modes). Always
// validates URLs/scopes shape when non-empty.
func validateOIDCInput(in OIDCInput, mode string) error {
	if mode == runtime.AuthModeOIDC {
		if strings.TrimSpace(in.Issuer) == "" {
			return newValidationErr("auth.oidc.issuer", "INVALID_OIDC_ISSUER",
				"issuer is required when auth_mode=oidc")
		}
		if !strings.HasPrefix(in.Issuer, "https://") && !strings.HasPrefix(in.Issuer, "http://") {
			return newValidationErr("auth.oidc.issuer", "INVALID_OIDC_ISSUER",
				"issuer must be a valid URL")
		}
		if strings.TrimSpace(in.ClientID) == "" {
			return newValidationErr("auth.oidc.client_id", "INVALID_OIDC_CLIENT_ID",
				"client_id is required when auth_mode=oidc")
		}
		if strings.TrimSpace(in.RedirectURL) == "" {
			return newValidationErr("auth.oidc.redirect_url", "INVALID_OIDC_REDIRECT_URL",
				"redirect_url is required when auth_mode=oidc")
		}
	}
	if len(in.Scopes) > 0 {
		hasOpenID := false
		for _, s := range in.Scopes {
			if strings.TrimSpace(s) == "openid" {
				hasOpenID = true
				break
			}
		}
		if !hasOpenID {
			return newValidationErr("auth.oidc.scopes", "INVALID_OIDC_SCOPES",
				"scopes must include 'openid'")
		}
	}
	if len(in.AllowedGroups) > 64 {
		return newValidationErr("auth.oidc.allowed_groups", "INVALID_OIDC_GROUPS_TOO_MANY",
			"at most 64 allowed_groups entries")
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
	networks := append([]string(nil), row.Auth.LocalNetworks...)
	if networks == nil {
		networks = []string{}
	}
	mode := row.Auth.Mode
	if mode == "" {
		mode = runtime.AuthModeForms
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
		Auth: AuthOutput{
			SessionTTL:     row.Auth.SessionTTL,
			SecureCookie:   row.Auth.SecureCookie,
			TrustedProxies: proxies,
			Mode:           mode,
			LocalBypass:    row.Auth.LocalBypass,
			LocalNetworks:  networks,
			SessionEpoch:   row.Auth.SessionEpoch,
			OIDC: OIDCOutput{
				Issuer:        row.Auth.OIDC.Issuer,
				ClientID:      row.Auth.OIDC.ClientID,
				RedirectURL:   row.Auth.OIDC.RedirectURL,
				Scopes:        append([]string(nil), row.Auth.OIDC.Scopes...),
				UsernameClaim: row.Auth.OIDC.UsernameClaim,
				AllowedGroups: append([]string(nil), row.Auth.OIDC.AllowedGroups...),
			},
		},
		AutoGeneratedAPIKey: row.APIKeyAutoGenerated,
		UpdatedAt:           ts.UTC(),
	}
}
