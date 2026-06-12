package externalservices

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	apports "github.com/alexmorbo/seasonfill/application/ports"
	infra "github.com/alexmorbo/seasonfill/infrastructure/externalservices"
)

// UseCase orchestrates the four operator-facing flows: List (masked),
// Upsert (PUT semantics), Test (real call), and the env-aware Read
// helper that the reload subscriber consumes. envLookup is injected
// for testability; production wires os.Getenv. publisher is optional —
// nil disables the post-Upsert republish (used by tests).
type UseCase struct {
	repo      Repository
	envLookup infra.EnvLookup
	tester    Tester
	publisher Publisher
	logger    *slog.Logger
	now       func() time.Time
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
		logger = slog.Default()
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
		out = append(out, mask(merged))
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
	return mask(merged), nil
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

// Test performs a real upstream call against the merged settings,
// classifies the outcome, and persists it. Returns the result for
// immediate UI feedback even when persistence fails (the operator
// still gets to see the verdict; persistence failures are logged at
// Error level).
func (uc *UseCase) Test(ctx context.Context, svc infra.Service) (TestResult, error) {
	if !svc.Valid() {
		return TestResult{}, infra.ErrInvalidService
	}
	db, err := uc.repo.Get(ctx, svc)
	rowExists := err == nil
	if err != nil && !errors.Is(err, apports.ErrNotFound) {
		return TestResult{}, err
	}
	merged := infra.Merge(svc, db, uc.envLookup)
	if merged.APIKey == "" {
		return TestResult{Outcome: infra.OutcomeAuthFailed, Message: "no api key configured"}, nil
	}
	outcome, message, latency := uc.tester.Test(ctx, merged)
	uc.logger.InfoContext(ctx, "external_services.test",
		slog.String("service", string(svc)),
		slog.String("outcome", string(outcome)),
		slog.String("proxy_scheme", schemeOf(merged.ProxyURL)),
		slog.Int64("duration_ms", latency.Milliseconds()),
	)
	if rowExists {
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
