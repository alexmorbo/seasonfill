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
type UseCase struct {
	repo        Repository
	envLookup   infra.EnvLookup
	tester      Tester
	publisher   Publisher
	logger      *slog.Logger
	now         func() time.Time
	testResults sync.Map // map[infra.Service]testResult
}

// testResult is the per-pod-lifetime view of the last Test() outcome
// for one service. Replaces the legacy `last_test_*` columns on
// external_service_settings (dropped in D-5 schema).
type testResult struct {
	At      time.Time
	Outcome infra.Outcome
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
// in-process Test() outcome cache. Used by List + Get; Upsert returns
// a fresh masked view via the bare mask() helper because the test
// result is unchanged by a config write.
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
	return mask(merged), nil
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
