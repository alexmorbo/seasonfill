package externalservices

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	infra "github.com/alexmorbo/seasonfill/internal/shared/clients/externalservices"
	apports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// ErrInvalidExternalKey is returned by Upsert when the inline
// validate-on-save probe rejects the supplied API key. The REST layer
// maps this to HTTP 422 with the `external_service_invalid_key` slug.
// Story 489 (B-17).
var ErrInvalidExternalKey = errors.New("externalservices: invalid api key")

// UseCase orchestrates the four operator-facing flows: List (masked),
// Upsert (PUT semantics), Test (real call), and the env-aware Read
// helper that the reload subscriber consumes. envLookup is injected
// for testability; production wires os.Getenv. publisher is optional —
// nil disables the post-Upsert republish (used by tests).
//
// D-5 (466c) — testResults caches the last Test() outcome per service
// for the pod lifetime. The legacy last_test_* DB columns were dropped
// per ADR Decision B; this sync.Map fills the operator-facing UI gap
// without a follow-up migration. Pod restart clears the cache; the
// operator clicks Test again. Keyed by infra.Service; value is
// testResult.
//
// Story 489 (B-17) — validationResults caches the live invalid-key
// signal per service. Populated by ReportAuthFailure (called by the
// TMDB client on a 401 from any enrichment request) and by
// recordValidationOK (called after a successful inline validate-on-save
// or a successful Test). Mirrors the testResults pattern; pod restart
// clears the cache and the next 401 immediately re-populates it.
type UseCase struct {
	repo              Repository
	envLookup         infra.EnvLookup
	tester            Tester
	publisher         Publisher
	logger            *slog.Logger
	now               func() time.Time
	testResults       sync.Map // map[infra.Service]testResult
	validationResults sync.Map // map[infra.Service]validationResult — Story 489 (B-17)
}

// testResult is the per-pod-lifetime view of the last Test() outcome
// for one service. Replaces the legacy `last_test_*` columns on
// external_service_settings (dropped in D-5 schema).
type testResult struct {
	At      time.Time
	Outcome infra.Outcome
	Message string
}

// validationResult is the runtime invalid-key signal for one service.
// Status is "valid" or "invalid_key"; the empty string means "never
// validated" (the caller renders an empty wire field).
// Story 489 (B-17).
type validationResult struct {
	At      time.Time
	Status  string
	Message string
}

// Tester is the proxy-aware HTTP probe; injectable for tests.
// Production wires the realTester defined in test_runner.go.
type Tester interface {
	Test(ctx context.Context, s infra.Settings) (infra.Outcome, string, time.Duration)
}

func NewUseCase(repo Repository, env infra.EnvLookup, tester Tester, pub Publisher, logger *slog.Logger) *UseCase {
	if env == nil {
		env = func(string) string { return "" }
	}
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "admin")
	}
	return &UseCase{
		repo:      repo,
		envLookup: env,
		tester:    tester,
		publisher: pub,
		logger:    logger,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// MaskedView is the wire shape List/Upsert emit. The plaintext key is
// never on this struct — only api_key_masked = "****" + last4.
type MaskedView struct {
	Service          infra.Service
	Enabled          bool
	APIKeyMasked     string // "****abcd" when last4 set, "" otherwise
	APIKeyConfigured bool   // any source (env or DB) supplies a key
	ProxyURLSet      bool
	ProxyAuthSet     bool
	ProxyScheme      string // host-only, redacted
	ProxyHost        string
	LastTestAt       *time.Time
	LastTestOutcome  infra.Outcome
	LastTestMessage  string
	// Story 489 (B-17): runtime validation status surfaced by the TMDB
	// client's 401 hook + the inline validate-on-save probe. Empty
	// Status = never validated (FE renders nothing). "valid" or
	// "invalid_key" otherwise.
	LastValidationAt      *time.Time
	LastValidationStatus  string
	LastValidationMessage string
}

// ReportAuthFailure implements tmdb.AuthFailureReporter. The TMDB
// client calls this from doOnce when it sees a 401. Idempotent — every
// 401 stamps now() + "invalid_key" + the truncated response body. The
// /external-services List/Get endpoints surface the latest stamp via
// the maskWithTestResult fold so the operator sees the failure on the
// next poll.
//
// Story 489 (B-17). The reporter is on the request hot path — only a
// sync.Map write and a single WarnContext log line. No DB, no IO.
func (uc *UseCase) ReportAuthFailure(service string, body string) {
	svc := infra.Service(service)
	if !svc.Valid() {
		return
	}
	uc.validationResults.Store(svc, validationResult{
		At:      uc.now(),
		Status:  "invalid_key",
		Message: body,
	})
	uc.logger.WarnContext(context.Background(), "external_services.auth_failure_reported",
		slog.String("service", service),
	)
}

// recordValidationOK clears the invalid_key flag after a successful
// validation probe (inline Upsert validate-on-save OR an explicit
// operator-driven Test that returns OutcomeOK). Story 489 (B-17).
func (uc *UseCase) recordValidationOK(svc infra.Service) {
	uc.validationResults.Store(svc, validationResult{
		At:     uc.now(),
		Status: "valid",
	})
}

// List returns the merged (env > DB) masked view for every service.
// Missing rows surface as zero-value MaskedView with Service set.
// D-5 (466c) — Test outcomes are now sourced from the in-process
// testResults cache rather than the dropped last_test_* columns.
func (uc *UseCase) List(ctx context.Context) ([]MaskedView, error) {
	dbRows, err := uc.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	dbBySvc := make(map[infra.Service]infra.Settings, len(dbRows))
	for _, r := range dbRows {
		dbBySvc[r.Service] = r
	}
	out := make([]MaskedView, 0, len(infra.AllServices))
	for _, svc := range infra.AllServices {
		merged := infra.Merge(svc, dbBySvc[svc], uc.envLookup)
		out = append(out, uc.maskWithTestResult(svc, merged))
	}
	return out, nil
}

// Get returns the merged masked view for one service.
func (uc *UseCase) Get(ctx context.Context, svc infra.Service) (MaskedView, error) {
	if !svc.Valid() {
		return MaskedView{}, infra.ErrInvalidService
	}
	db, err := uc.repo.Get(ctx, svc)
	if err != nil && !errors.Is(err, apports.ErrNotFound) {
		return MaskedView{}, err
	}
	merged := infra.Merge(svc, db, uc.envLookup)
	return uc.maskWithTestResult(svc, merged), nil
}

// maskWithTestResult derives the wire MaskedView and folds in the
// in-process Test() outcome cache plus the Story 489 (B-17) validation
// cache. Used by List, Get, AND Upsert (so the response after a save
// includes the validation stamp the inline probe just produced).
func (uc *UseCase) maskWithTestResult(svc infra.Service, merged infra.Settings) MaskedView {
	view := mask(merged)
	if cached, ok := uc.testResults.Load(svc); ok {
		if tr, ok := cached.(testResult); ok {
			at := tr.At
			view.LastTestAt = &at
			view.LastTestOutcome = tr.Outcome
			view.LastTestMessage = tr.Message
		}
	}
	// Story 489 (B-17): fold the live 401-hook / validate-on-save cache.
	if cached, ok := uc.validationResults.Load(svc); ok {
		if vr, ok := cached.(validationResult); ok {
			at := vr.At
			view.LastValidationAt = &at
			view.LastValidationStatus = vr.Status
			view.LastValidationMessage = vr.Message
		}
	}
	return view
}

// UpsertInput is the application-level write shape. Pointer fields
// implement PUT semantics: nil = unchanged, *"" = clear, *"non-empty"
// = set. Enabled is non-pointer because the UI always sends a boolean.
type UpsertInput struct {
	Enabled       bool
	APIKey        *string
	ProxyURL      *string
	ProxyUsername *string
	ProxyPassword *string
}

// Upsert merges input into the existing row (or a fresh zero row),
// writes it, and republishes the snapshot. Returns the post-write
// masked view.
//
// Story 489 (B-17) — when the operator sets a non-empty TMDB API key,
// the use case synchronously probes TMDB via the injected Tester
// BEFORE persisting. A 401 (OutcomeAuthFailed) surfaces as
// ErrInvalidExternalKey; the row is NOT written, the validation cache
// is stamped invalid_key so the next List/Get reflects it, and the
// REST layer translates the sentinel to HTTP 422. OMDb/TVDB skip the
// inline probe — their classifier semantics differ and the operator
// pain B-17 names is TMDB-specific.
func (uc *UseCase) Upsert(ctx context.Context, svc infra.Service, in UpsertInput) (MaskedView, error) {
	if !svc.Valid() {
		return MaskedView{}, infra.ErrInvalidService
	}
	existing, err := uc.repo.Get(ctx, svc)
	if err != nil && !errors.Is(err, apports.ErrNotFound) {
		return MaskedView{}, err
	}
	next := existing
	next.Service = svc
	next.Enabled = in.Enabled
	if in.APIKey != nil {
		next.APIKey = *in.APIKey
		next.APIKeyLast4 = infra.Last4(*in.APIKey)
	}
	if in.ProxyURL != nil {
		next.ProxyURL = *in.ProxyURL
	}
	if in.ProxyUsername != nil {
		next.ProxyUsername = *in.ProxyUsername
	}
	if in.ProxyPassword != nil {
		next.ProxyPassword = *in.ProxyPassword
	}

	// Story 489 (B-17): validate-on-save for TMDB only. Probe the merged
	// settings synchronously when the operator supplied a non-empty key;
	// reject the save with ErrInvalidExternalKey on 401 so the REST
	// layer can surface 422 + the FE keeps the form open with the bad
	// value in the input.
	if svc == infra.ServiceTMDB && in.APIKey != nil && *in.APIKey != "" && uc.tester != nil {
		tctx, cancel := context.WithTimeout(ctx, testTimeout)
		merged := infra.Merge(svc, next, uc.envLookup)
		outcome, message, _ := uc.tester.Test(tctx, merged)
		cancel()
		if outcome == infra.OutcomeAuthFailed {
			uc.validationResults.Store(svc, validationResult{
				At:      uc.now(),
				Status:  "invalid_key",
				Message: message,
			})
			uc.logger.WarnContext(ctx, "external_services.upsert.rejected_invalid_key",
				slog.String("service", string(svc)),
				slog.String("message", message),
			)
			return MaskedView{}, ErrInvalidExternalKey
		}
		if outcome == infra.OutcomeOK {
			// Probe succeeded — record a valid stamp before persisting
			// so the response reflects the just-confirmed state.
			uc.recordValidationOK(svc)
		}
	}

	if err := uc.repo.Upsert(ctx, next); err != nil {
		return MaskedView{}, err
	}
	if uc.publisher != nil {
		uc.publisher.Publish(ctx)
	}
	uc.logger.InfoContext(ctx, "external_services.upsert",
		slog.String("service", string(svc)),
		slog.Bool("enabled", next.Enabled),
		slog.String("proxy_scheme", schemeOf(next.ProxyURL)),
	)
	merged := infra.Merge(svc, next, uc.envLookup)
	return uc.maskWithTestResult(svc, merged), nil
}

// TestResult is the wire shape /test returns to the UI.
type TestResult struct {
	Outcome   infra.Outcome
	Message   string
	LatencyMS int64
}

// testTimeout caps every Test() upstream call. PRD §10.4.7 prescribes
// 5–10s; 5s keeps the UI snappy — operator hits "Test" and expects a
// verdict before alt-tabbing away. Applied at the use case boundary
// so the realTester probe and any future probe inherit the same cap.
const testTimeout = 5 * time.Second

// Test performs a real upstream call against the merged settings,
// classifies the outcome, and persists it. Returns the result for
// immediate UI feedback even when persistence fails (the operator
// still gets to see the verdict; persistence failures are logged at
// Error level).
func (uc *UseCase) Test(ctx context.Context, svc infra.Service) (TestResult, error) {
	if !svc.Valid() {
		return TestResult{}, infra.ErrInvalidService
	}
	tctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	db, err := uc.repo.Get(tctx, svc)
	rowExists := err == nil
	if err != nil && !errors.Is(err, apports.ErrNotFound) {
		return TestResult{}, err
	}
	merged := infra.Merge(svc, db, uc.envLookup)
	if merged.APIKey == "" {
		return TestResult{Outcome: infra.OutcomeAuthFailed, Message: "no api key configured"}, nil
	}
	outcome, message, latency := uc.tester.Test(tctx, merged)
	uc.logger.InfoContext(tctx, "external_services.test",
		slog.String("service", string(svc)),
		slog.String("outcome", string(outcome)),
		slog.String("proxy_scheme", schemeOf(merged.ProxyURL)),
		slog.Int64("duration_ms", latency.Milliseconds()),
	)
	// D-5 (466c) — cache the latest verdict in-process. The repo's
	// MarkTest is a no-op under the new schema; the sync.Map fills
	// the gap so List + Get surface the outcome until the pod restarts.
	// rowExists is preserved so explicit empty-state UI still shows
	// "never tested" for a fresh-install service the operator has not
	// configured yet.
	uc.testResults.Store(svc, testResult{
		At:      uc.now(),
		Outcome: outcome,
		Message: message,
	})
	// Story 489 (B-17): an operator-driven Test that succeeds clears
	// any prior invalid_key flag (UX symmetry — banner+badge disappear
	// on next poll). An auth_failed Test mirrors the live 401 hook and
	// stamps the cache so the operator-driven probe and the runtime
	// hook converge on one signal.
	switch outcome {
	case infra.OutcomeOK:
		uc.recordValidationOK(svc)
	case infra.OutcomeAuthFailed:
		uc.validationResults.Store(svc, validationResult{
			At:      uc.now(),
			Status:  "invalid_key",
			Message: message,
		})
	}
	if rowExists {
		// Persistence uses the parent ctx; under D-5 this is a no-op
		// returning nil — kept for port symmetry. If a future story
		// re-introduces durable last_test_*, no behaviour change here.
		if err := uc.repo.MarkTest(ctx, svc, uc.now(), outcome, message); err != nil {
			uc.logger.ErrorContext(ctx, "external_services.test.persist_failed",
				slog.String("service", string(svc)), slog.Any("err", err))
		}
	}
	return TestResult{Outcome: outcome, Message: message, LatencyMS: latency.Milliseconds()}, nil
}

// EffectiveSettings returns the merged (env > DB) plaintext settings
// for the reload subscriber to feed into HttpClientFor. Plaintext
// crosses this boundary — only the reload subscriber consumes it,
// never the HTTP layer.
func (uc *UseCase) EffectiveSettings(ctx context.Context, svc infra.Service) (infra.Settings, error) {
	db, err := uc.repo.Get(ctx, svc)
	if err != nil && !errors.Is(err, apports.ErrNotFound) {
		return infra.Settings{}, err
	}
	return infra.Merge(svc, db, uc.envLookup), nil
}

// EffectiveSettingsWithSource mirrors EffectiveSettings but additionally
// returns the per-field origin map for use by the boot subscriber's
// extsvc.source log line. Plaintext crosses this boundary; only the
// reload subscriber consumes it.
func (uc *UseCase) EffectiveSettingsWithSource(ctx context.Context, svc infra.Service) (infra.Settings, infra.SourceMap, error) {
	db, err := uc.repo.Get(ctx, svc)
	if err != nil && !errors.Is(err, apports.ErrNotFound) {
		return infra.Settings{}, infra.SourceMap{}, err
	}
	s, src := infra.MergeWithSource(svc, db, uc.envLookup)
	return s, src, nil
}

func mask(s infra.Settings) MaskedView {
	view := MaskedView{
		Service:          s.Service,
		Enabled:          s.Enabled,
		APIKeyConfigured: s.APIKey != "",
		ProxyURLSet:      s.ProxyURL != "",
		ProxyAuthSet:     s.ProxyUsername != "" || s.ProxyPassword != "",
		LastTestAt:       s.LastTestAt,
		LastTestOutcome:  s.LastTestOutcome,
		LastTestMessage:  s.LastTestMessage,
	}
	if s.APIKeyLast4 != "" {
		view.APIKeyMasked = "****" + s.APIKeyLast4
	}
	if s.ProxyURL != "" {
		view.ProxyScheme = schemeOf(s.ProxyURL)
		view.ProxyHost = hostOf(s.ProxyURL)
	}
	return view
}

func schemeOf(raw string) string {
	if raw == "" {
		return ""
	}
	i := strings.Index(raw, "://")
	if i <= 0 {
		return ""
	}
	return strings.ToLower(raw[:i])
}

func hostOf(raw string) string {
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
