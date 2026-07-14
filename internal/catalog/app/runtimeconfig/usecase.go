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
	"regexp"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
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

	// guidRewritesMaxLen / guidRewriteFromMaxLen / guidRewriteToMaxLen
	// bound the operator-curated tracker-URL substitution table (107).
	// 50 rules covers any realistic operator setup; the per-entry caps
	// keep a malicious / accidental PUT body small. The combined worst-
	// case payload (50×(512+1024)) is well under runtimeConfigBodyLimit
	// (64 KiB) which already caps the total — the per-entry checks just
	// give a precise error code.
	guidRewritesMaxLen    = 50
	guidRewriteFromMaxLen = 512
	guidRewriteToMaxLen   = 1024
)

var cronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

var oidcGroupsClaimPattern = regexp.MustCompile(
	`^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)*$`,
)

func defaultIfBlank(s, def string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return def
	}
	return t
}

type UseCase struct {
	runtimes        ports.RuntimeConfigRepository
	instances       ports.SonarrInstanceRepository
	cipher          *crypto.Cipher
	bus             *runtime.Bus
	logger          *slog.Logger
	now             func() time.Time
	clientSecretEnv string
}

func (u *UseCase) WithClientSecretEnv(v string) *UseCase {
	u.clientSecretEnv = v
	return u
}

func New(
	runtimes ports.RuntimeConfigRepository,
	instances ports.SonarrInstanceRepository,
	cipher *crypto.Cipher,
	bus *runtime.Bus,
	logger *slog.Logger,
) *UseCase {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "admin")
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
		var runtimeNF *sharedErrors.RuntimeConfigNotFoundError
		if errors.As(err, &runtimeNF) {
			u.logger.DebugContext(ctx, "runtimeconfig.get.default_fallback",
				slog.String("code", runtimeNF.Code()),
				slog.String("reason", "no row yet, serving defaults"))
		} else {
			u.logger.DebugContext(ctx, "runtimeconfig.get.default_fallback",
				slog.String("code", "not_found"),
				slog.String("reason", "no row yet, serving defaults"))
		}
		def := runtime.Defaults()
		row = ports.RuntimeConfigRow{
			Cron: def.Cron, Scan: def.Scan, DryRun: def.DryRun,
			GlobalRateLimit: def.GlobalRateLimit, Auth: def.Auth,
			GUIDRewrites: def.GUIDRewrites,
		}
	default:
		return Output{}, time.Time{},
			fmt.Errorf("runtimeconfig: get row: %w", err)
	}
	ts := row.UpdatedAt.Truncate(time.Second)
	return rowToOutput(row, ts, u.clientSecretEnv), ts, nil
}

// Update validates the incoming Input, persists it, then republishes a
// fresh Snapshot. Bumps SessionEpoch when any auth-invalidating field
// changes (Mode / OIDC). The bump uses the usecase clock (UnixNano)
// which is monotonic across normal real-world gaps and stable in tests.
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

	snap, err := u.inputToSnapshot(in, prevRow)
	if err != nil {
		return Output{}, time.Time{}, err
	}

	clientSecretChanged := in.Auth.OIDC.ClientSecret != nil

	if epochShouldBump(prevRow.Auth, snap.Auth) || clientSecretChanged {
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

	if in.Auth.OIDC.ClientSecret != nil {
		if err := u.runtimes.UpsertOIDCSecret(ctx, *in.Auth.OIDC.ClientSecret); err != nil {
			return Output{}, time.Time{},
				fmt.Errorf("runtimeconfig: oidc secret: %w", err)
		}
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
	return rowToOutput(stored, ts, u.clientSecretEnv), ts, nil
}

func epochShouldBump(prev, next runtime.AuthSnapshot) bool {
	if prev.OIDC.Issuer != next.OIDC.Issuer ||
		prev.OIDC.ClientID != next.OIDC.ClientID ||
		prev.OIDC.RedirectURL != next.OIDC.RedirectURL ||
		prev.OIDC.UsernameClaim != next.OIDC.UsernameClaim ||
		prev.OIDC.GroupsClaim != next.OIDC.GroupsClaim {
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
		Instances:    insts,
		GUIDRewrites: append([]runtime.GUIDRewriteRule(nil), row.GUIDRewrites...),
	}
	if u.bus != nil {
		u.bus.Publish(ctx, snap)
	}
	u.logger.InfoContext(ctx, "runtimeconfig.published",
		slog.Int("instance_count", len(insts)))
	return nil
}

func (u *UseCase) inputToSnapshot(in Input, prevRow ports.RuntimeConfigRow) (runtime.Snapshot, error) {
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
	hasStored := len(prevRow.OIDCClientSecretCiphertext) > 0
	if err := validateOIDCInput(in.Auth.OIDC, u.clientSecretEnv, hasStored); err != nil {
		return runtime.Snapshot{}, err
	}
	cleanedRewrites, err := validateGUIDRewrites(in.GUIDRewrites)
	if err != nil {
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
			OIDC: runtime.OIDCSnapshot{
				Issuer:        strings.TrimSpace(in.Auth.OIDC.Issuer),
				ClientID:      strings.TrimSpace(in.Auth.OIDC.ClientID),
				RedirectURL:   strings.TrimSpace(in.Auth.OIDC.RedirectURL),
				Scopes:        append([]string(nil), in.Auth.OIDC.Scopes...),
				UsernameClaim: strings.TrimSpace(in.Auth.OIDC.UsernameClaim),
				AllowedGroups: append([]string(nil), in.Auth.OIDC.AllowedGroups...),
				GroupsClaim:   defaultIfBlank(in.Auth.OIDC.GroupsClaim, "groups"),
			},
		},
		GUIDRewrites: cleanedRewrites,
	}, nil
}

func validateOIDCInput(in OIDCInput, env string, hasStoredSecret bool) error {
	hasEnv := env != ""
	hasIncoming := in.ClientSecret != nil && *in.ClientSecret != ""
	clearing := in.ClientSecret != nil && *in.ClientSecret == ""
	effectiveStored := hasStoredSecret && !clearing
	secretResolved := hasEnv || effectiveStored || hasIncoming

	hasIssuer := strings.TrimSpace(in.Issuer) != ""
	hasClientID := strings.TrimSpace(in.ClientID) != ""

	// The OIDC subtree is optional: operators may leave every field blank
	// (OIDC simply stays unconfigured / not ready). The all-or-nothing
	// check only kicks in once the operator starts configuring OIDC —
	// signalled by any of issuer / client_id / an incoming client_secret.
	configuring := hasIssuer || hasClientID || hasIncoming
	if !configuring {
		return nil
	}

	if !hasIssuer {
		return newValidationErr("auth.oidc.issuer", "INVALID_OIDC_ISSUER",
			"issuer is required when configuring OIDC")
	}
	if !strings.HasPrefix(in.Issuer, "https://") && !strings.HasPrefix(in.Issuer, "http://") {
		return newValidationErr("auth.oidc.issuer", "INVALID_OIDC_ISSUER",
			"issuer must be a valid URL")
	}
	if !hasClientID {
		return newValidationErr("auth.oidc.client_id", "INVALID_OIDC_CLIENT_ID",
			"client_id is required when configuring OIDC")
	}
	if !secretResolved {
		return newValidationErr("auth.oidc.client_secret", "OIDC_CLIENT_SECRET_MISSING",
			"OIDC client_secret missing (set OIDC_CLIENT_SECRET env or configure in UI)")
	}
	// redirect_url intentionally optional — Start derives it.

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
	claim := strings.TrimSpace(in.GroupsClaim)
	if claim != "" && !oidcGroupsClaimPattern.MatchString(claim) {
		return newValidationErr("auth.oidc.groups_claim", "INVALID_OIDC_GROUPS_CLAIM",
			"groups_claim must be dot-separated identifiers (e.g. 'groups' or 'realm_access.roles')")
	}
	return nil
}

// validateGUIDRewrites trims, bounds, and dedupes the rule list. Returns
// the cleaned slice (always non-nil) so the caller can persist canonical
// form. Empty input → empty (non-nil) output.
//
// Rules:
//   - len ≤ guidRewritesMaxLen (DoS bound + sanity cap on UI list size)
//   - each From trimmed; non-empty after trim
//   - each From ≤ guidRewriteFromMaxLen chars
//   - each To trimmed; ≤ guidRewriteToMaxLen chars (empty allowed)
//   - From values are unique (operator typo guard)
func validateGUIDRewrites(list []runtime.GUIDRewriteRule) ([]runtime.GUIDRewriteRule, error) {
	if len(list) > guidRewritesMaxLen {
		return nil, newValidationErr("guid_rewrites",
			"INVALID_GUID_REWRITES_TOO_MANY",
			fmt.Sprintf("at most %d rules allowed", guidRewritesMaxLen))
	}
	out := make([]runtime.GUIDRewriteRule, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	for i, raw := range list {
		from := strings.TrimSpace(raw.From)
		to := strings.TrimSpace(raw.To)
		if from == "" {
			return nil, newValidationErr("guid_rewrites",
				"INVALID_GUID_REWRITE_FROM_EMPTY",
				fmt.Sprintf("rule %d: from must not be empty", i))
		}
		if len(from) > guidRewriteFromMaxLen {
			return nil, newValidationErr("guid_rewrites",
				"INVALID_GUID_REWRITE_FROM_TOO_LONG",
				fmt.Sprintf("rule %d: from exceeds %d chars", i, guidRewriteFromMaxLen))
		}
		if len(to) > guidRewriteToMaxLen {
			return nil, newValidationErr("guid_rewrites",
				"INVALID_GUID_REWRITE_TO_TOO_LONG",
				fmt.Sprintf("rule %d: to exceeds %d chars", i, guidRewriteToMaxLen))
		}
		if _, dup := seen[from]; dup {
			return nil, newValidationErr("guid_rewrites",
				"INVALID_GUID_REWRITE_DUPLICATE_FROM",
				fmt.Sprintf("rule %d: duplicate from value %q", i, from))
		}
		seen[from] = struct{}{}
		out = append(out, runtime.GUIDRewriteRule{From: from, To: to})
	}
	return out, nil
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

func rowToOutput(row ports.RuntimeConfigRow, ts time.Time, envSecret string) Output {
	proxies := append([]string(nil), row.Auth.TrustedProxies...)
	if proxies == nil {
		proxies = []string{}
	}
	rewrites := append([]runtime.GUIDRewriteRule(nil), row.GUIDRewrites...)
	if rewrites == nil {
		rewrites = []runtime.GUIDRewriteRule{}
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
			SessionEpoch:   row.Auth.SessionEpoch,
			OIDC: OIDCOutput{
				Issuer:                  row.Auth.OIDC.Issuer,
				ClientID:                row.Auth.OIDC.ClientID,
				RedirectURL:             row.Auth.OIDC.RedirectURL,
				Scopes:                  append([]string(nil), row.Auth.OIDC.Scopes...),
				UsernameClaim:           row.Auth.OIDC.UsernameClaim,
				AllowedGroups:           append([]string(nil), row.Auth.OIDC.AllowedGroups...),
				GroupsClaim:             defaultIfBlank(row.Auth.OIDC.GroupsClaim, "groups"),
				ClientSecretConfigured:  envSecret != "" || len(row.OIDCClientSecretCiphertext) > 0,
				ClientSecretEnvOverride: envSecret != "",
			},
		},
		AutoGeneratedAPIKey: row.APIKeyAutoGenerated,
		UpdatedAt:           ts.UTC(),
		GUIDRewrites:        rewrites,
	}
}
