package externalservices

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	infra "github.com/alexmorbo/seasonfill/internal/shared/clients/externalservices"
	apports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

type stubRepo struct {
	row     map[infra.Service]infra.Settings
	marks   []markCall
	upserts []infra.Settings
	getErr  error
}
type markCall struct {
	svc     infra.Service
	at      time.Time
	outcome infra.Outcome
	message string
}

func newStubRepo() *stubRepo {
	return &stubRepo{row: make(map[infra.Service]infra.Settings)}
}

func (r *stubRepo) Get(_ context.Context, svc infra.Service) (infra.Settings, error) {
	if r.getErr != nil {
		return infra.Settings{}, r.getErr
	}
	s, ok := r.row[svc]
	if !ok {
		return infra.Settings{}, apports.ErrNotFound
	}
	return s, nil
}

func (r *stubRepo) List(_ context.Context) ([]infra.Settings, error) {
	out := []infra.Settings{}
	for _, svc := range infra.AllServices {
		if s, ok := r.row[svc]; ok {
			out = append(out, s)
		} else {
			out = append(out, infra.Settings{Service: svc})
		}
	}
	return out, nil
}

func (r *stubRepo) Upsert(_ context.Context, s infra.Settings) error {
	r.row[s.Service] = s
	r.upserts = append(r.upserts, s)
	return nil
}

func (r *stubRepo) MarkTest(_ context.Context, svc infra.Service, at time.Time, outcome infra.Outcome, msg string) error {
	r.marks = append(r.marks, markCall{svc, at, outcome, msg})
	s := r.row[svc]
	s.LastTestOutcome = outcome
	s.LastTestMessage = msg
	s.LastTestAt = &at
	r.row[svc] = s
	return nil
}

type stubTester struct {
	outcome   infra.Outcome
	msg       string
	dur       time.Duration
	gotCtx    context.Context
	deadlines []time.Time
	sleep     time.Duration
	calls     int
}

func (t *stubTester) Test(ctx context.Context, _ infra.Settings) (infra.Outcome, string, time.Duration) {
	t.gotCtx = ctx
	t.calls++
	if dl, ok := ctx.Deadline(); ok {
		t.deadlines = append(t.deadlines, dl)
	}
	if t.sleep > 0 {
		select {
		case <-time.After(t.sleep):
		case <-ctx.Done():
			return infra.OutcomeTimeout, "ctx done", t.dur
		}
	}
	return t.outcome, t.msg, t.dur
}

type stubPub struct{ n int }

func (p *stubPub) Publish(context.Context) { p.n++ }

func TestUpsert_PUTSemantics_NilUnchanged(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	repo.row[infra.ServiceTMDB] = infra.Settings{
		Service: infra.ServiceTMDB, APIKey: "olddata", APIKeyLast4: "data", ProxyURL: "http://old:1",
	}
	uc := NewUseCase(repo, nil, &stubTester{outcome: infra.OutcomeOK}, &stubPub{}, nil)
	_, err := uc.Upsert(context.Background(), infra.ServiceTMDB, UpsertInput{Enabled: true})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got := repo.upserts[len(repo.upserts)-1]
	if got.APIKey != "olddata" {
		t.Fatalf("nil APIKey must preserve old value, got %q", got.APIKey)
	}
	if got.ProxyURL != "http://old:1" {
		t.Fatalf("nil ProxyURL must preserve old value, got %q", got.ProxyURL)
	}
}

func TestUpsert_PUTSemantics_EmptyClears(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	repo.row[infra.ServiceTMDB] = infra.Settings{
		Service: infra.ServiceTMDB, APIKey: "oldkey", APIKeyLast4: "dkey", ProxyURL: "http://old:1",
	}
	uc := NewUseCase(repo, nil, &stubTester{}, nil, nil)
	empty := ""
	_, err := uc.Upsert(context.Background(), infra.ServiceTMDB, UpsertInput{
		Enabled: false, APIKey: &empty, ProxyURL: &empty,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got := repo.upserts[len(repo.upserts)-1]
	if got.APIKey != "" || got.APIKeyLast4 != "" {
		t.Fatalf("empty must clear key: %+v", got)
	}
	if got.ProxyURL != "" {
		t.Fatalf("empty must clear proxy: %+v", got)
	}
}

func TestUpsert_PUTSemantics_NonEmptySets(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	uc := NewUseCase(repo, nil, &stubTester{}, nil, nil)
	key := "supersecret"
	proxy := "socks5://proxy.example:1080"
	user := "alice"
	pass := "wonderland"
	_, err := uc.Upsert(context.Background(), infra.ServiceOMDB, UpsertInput{
		Enabled:       true,
		APIKey:        &key,
		ProxyURL:      &proxy,
		ProxyUsername: &user,
		ProxyPassword: &pass,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got := repo.upserts[0]
	if got.APIKey != key || got.APIKeyLast4 != "cret" {
		t.Fatalf("api key not set: %+v", got)
	}
	if got.ProxyURL != proxy || got.ProxyUsername != user || got.ProxyPassword != pass {
		t.Fatalf("proxy fields not set: %+v", got)
	}
}

func TestUpsert_PublisherInvoked(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	pub := &stubPub{}
	uc := NewUseCase(repo, nil, &stubTester{}, pub, nil)
	if _, err := uc.Upsert(context.Background(), infra.ServiceTVDB, UpsertInput{Enabled: true}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if pub.n != 1 {
		t.Fatalf("Publish should fire once per Upsert, got %d", pub.n)
	}
}

func TestUpsert_InvalidService(t *testing.T) {
	t.Parallel()
	uc := NewUseCase(newStubRepo(), nil, &stubTester{}, nil, nil)
	_, err := uc.Upsert(context.Background(), infra.Service("imdb"), UpsertInput{})
	if !errors.Is(err, infra.ErrInvalidService) {
		t.Fatalf("expected ErrInvalidService, got %v", err)
	}
}

func TestList_EnvOverridesDB_MaskedNeverLeaksKey(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	repo.row[infra.ServiceTMDB] = infra.Settings{
		Service: infra.ServiceTMDB, APIKey: "db_key", APIKeyLast4: "_key",
	}
	env := func(name string) string {
		if name == "SEASONFILL_TMDB_TOKEN" {
			return "env_value"
		}
		return ""
	}
	uc := NewUseCase(repo, env, &stubTester{}, nil, nil)
	views, err := uc.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var tmdb MaskedView
	for _, v := range views {
		if v.Service == infra.ServiceTMDB {
			tmdb = v
		}
	}
	if tmdb.APIKeyMasked != "****alue" {
		t.Fatalf("env must drive masked last4: %q", tmdb.APIKeyMasked)
	}
	if !tmdb.APIKeyConfigured {
		t.Fatalf("APIKeyConfigured must be true when env supplies key")
	}
}

func TestList_AllThreeServicesReturned(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	uc := NewUseCase(repo, nil, &stubTester{}, nil, nil)
	views, err := uc.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(views) != 3 {
		t.Fatalf("expect 3 views, got %d", len(views))
	}
	seen := map[infra.Service]bool{}
	for _, v := range views {
		seen[v.Service] = true
	}
	for _, svc := range infra.AllServices {
		if !seen[svc] {
			t.Fatalf("missing svc %s", svc)
		}
	}
}

func TestGet_MaskedView(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	repo.row[infra.ServiceTMDB] = infra.Settings{
		Service:       infra.ServiceTMDB,
		APIKey:        "very-secret-key-abcd",
		APIKeyLast4:   "abcd",
		ProxyURL:      "socks5://proxy.example:1080",
		ProxyUsername: "alice",
	}
	uc := NewUseCase(repo, nil, &stubTester{}, nil, nil)
	view, err := uc.Get(context.Background(), infra.ServiceTMDB)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if view.APIKeyMasked != "****abcd" {
		t.Fatalf("masked key: %q", view.APIKeyMasked)
	}
	if view.ProxyScheme != "socks5" {
		t.Fatalf("scheme: %q", view.ProxyScheme)
	}
	if view.ProxyHost != "proxy.example:1080" {
		t.Fatalf("host: %q", view.ProxyHost)
	}
	if !view.ProxyAuthSet {
		t.Fatalf("ProxyAuthSet must be true when username set")
	}
}

func TestGet_InvalidService(t *testing.T) {
	t.Parallel()
	uc := NewUseCase(newStubRepo(), nil, &stubTester{}, nil, nil)
	_, err := uc.Get(context.Background(), infra.Service("imdb"))
	if !errors.Is(err, infra.ErrInvalidService) {
		t.Fatalf("expected ErrInvalidService, got %v", err)
	}
}

func TestTest_PersistsOutcome(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	repo.row[infra.ServiceOMDB] = infra.Settings{
		Service: infra.ServiceOMDB, APIKey: "k", APIKeyLast4: "k",
	}
	uc := NewUseCase(repo, nil, &stubTester{outcome: infra.OutcomeOK, dur: 50 * time.Millisecond}, nil, nil)
	res, err := uc.Test(context.Background(), infra.ServiceOMDB)
	if err != nil {
		t.Fatalf("test: %v", err)
	}
	if res.Outcome != infra.OutcomeOK || res.LatencyMS != 50 {
		t.Fatalf("result: %+v", res)
	}
	if len(repo.marks) != 1 || repo.marks[0].outcome != infra.OutcomeOK {
		t.Fatalf("MarkTest not called: %+v", repo.marks)
	}
}

func TestTest_NoKey_ShortCircuits(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	repo.row[infra.ServiceTMDB] = infra.Settings{Service: infra.ServiceTMDB}
	uc := NewUseCase(repo, nil, &stubTester{outcome: infra.OutcomeOK}, nil, nil)
	res, err := uc.Test(context.Background(), infra.ServiceTMDB)
	if err != nil {
		t.Fatalf("test: %v", err)
	}
	if res.Outcome != infra.OutcomeAuthFailed {
		t.Fatalf("no key must short-circuit to auth_failed: %+v", res)
	}
}

func TestTest_InvalidService(t *testing.T) {
	t.Parallel()
	uc := NewUseCase(newStubRepo(), nil, &stubTester{}, nil, nil)
	_, err := uc.Test(context.Background(), infra.Service("imdb"))
	if !errors.Is(err, infra.ErrInvalidService) {
		t.Fatalf("expected ErrInvalidService, got %v", err)
	}
}

// TestTest_AppliesTimeout asserts the Tester runs under a 5s deadline.
// The stub records the context deadline it receives; we check the
// deadline lies within (now, now+testTimeout+small slack].
func TestTest_AppliesTimeout(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	repo.row[infra.ServiceTMDB] = infra.Settings{
		Service: infra.ServiceTMDB, APIKey: "k",
	}
	tester := &stubTester{outcome: infra.OutcomeOK}
	uc := NewUseCase(repo, nil, tester, nil, nil)
	start := time.Now()
	if _, err := uc.Test(context.Background(), infra.ServiceTMDB); err != nil {
		t.Fatalf("test: %v", err)
	}
	if len(tester.deadlines) != 1 {
		t.Fatalf("expected exactly one deadline observed, got %d", len(tester.deadlines))
	}
	dl := tester.deadlines[0]
	if dl.Before(start) {
		t.Fatalf("deadline before start: %v vs %v", dl, start)
	}
	max := start.Add(testTimeout + 250*time.Millisecond)
	if dl.After(max) {
		t.Fatalf("deadline beyond testTimeout window: %v > %v", dl, max)
	}
}

func TestTest_NoRowNoPersistButResultReturned(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	// No row stored. env supplies the key so Merge fills it.
	env := func(name string) string {
		if name == "SEASONFILL_TVDB_TOKEN" {
			return "env_key"
		}
		return ""
	}
	uc := NewUseCase(repo, env, &stubTester{outcome: infra.OutcomeOK}, nil, nil)
	res, err := uc.Test(context.Background(), infra.ServiceTVDB)
	if err != nil {
		t.Fatalf("test: %v", err)
	}
	if res.Outcome != infra.OutcomeOK {
		t.Fatalf("env-keyed test should succeed: %+v", res)
	}
	if len(repo.marks) != 0 {
		t.Fatalf("no row → no MarkTest, got %+v", repo.marks)
	}
}

func TestEffectiveSettings_MergesEnv(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	repo.row[infra.ServiceTMDB] = infra.Settings{
		Service: infra.ServiceTMDB, APIKey: "db_key", APIKeyLast4: "_key",
	}
	env := func(name string) string {
		if name == "SEASONFILL_TMDB_PROXY_URL" {
			return "socks5://env.proxy:1"
		}
		return ""
	}
	uc := NewUseCase(repo, env, &stubTester{}, nil, nil)
	eff, err := uc.EffectiveSettings(context.Background(), infra.ServiceTMDB)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if eff.APIKey != "db_key" {
		t.Fatalf("db key must persist when env empty: %q", eff.APIKey)
	}
	if eff.ProxyURL != "socks5://env.proxy:1" {
		t.Fatalf("env proxy_url must override: %q", eff.ProxyURL)
	}
}

func TestMaskHelper_NoLast4_NoMasked(t *testing.T) {
	t.Parallel()
	// Direct hit on the mask helper to guarantee an empty key yields
	// an empty masked string (the UI relies on this to render the
	// "not configured" placeholder).
	v := mask(infra.Settings{Service: infra.ServiceTMDB})
	if v.APIKeyMasked != "" {
		t.Fatalf("empty key must yield empty mask, got %q", v.APIKeyMasked)
	}
	if v.APIKeyConfigured {
		t.Fatalf("APIKeyConfigured must be false when key empty")
	}
}

func TestMaskHelper_WithKey(t *testing.T) {
	t.Parallel()
	v := mask(infra.Settings{
		Service:     infra.ServiceTMDB,
		APIKey:      "abcdefgh",
		APIKeyLast4: "efgh",
	})
	if v.APIKeyMasked != "****efgh" {
		t.Fatalf("mask shape wrong: %q", v.APIKeyMasked)
	}
}

func TestEffectiveSettings_FreshInstallEnvFallback(t *testing.T) {
	t.Parallel()
	repo := newStubRepo() // empty
	env := func(name string) string {
		if name == "SEASONFILL_TMDB_TOKEN" {
			return "env-token"
		}
		return ""
	}
	uc := NewUseCase(repo, env, &stubTester{}, nil, nil)

	got, err := uc.EffectiveSettings(context.Background(), infra.ServiceTMDB)
	if err != nil {
		t.Fatalf("EffectiveSettings: %v", err)
	}
	if got.APIKey != "env-token" {
		t.Fatalf("env fallback failed: APIKey = %q", got.APIKey)
	}
	if !got.Enabled {
		t.Fatalf("env fallback must flip Enabled true")
	}

	_, src, err := uc.EffectiveSettingsWithSource(context.Background(), infra.ServiceTMDB)
	if err != nil {
		t.Fatalf("EffectiveSettingsWithSource: %v", err)
	}
	if src.APIKey != infra.FieldSourceEnv {
		t.Fatalf("Source = %q want env", src.APIKey)
	}
}

func TestEffectiveSettings_DBPopulatedEnvOverrides(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	repo.row[infra.ServiceOMDB] = infra.Settings{
		Service: infra.ServiceOMDB, APIKey: "db-key", APIKeyLast4: "-key", Enabled: true,
	}
	env := func(name string) string {
		if name == "SEASONFILL_OMDB_TOKEN" {
			return "env-key"
		}
		return ""
	}
	uc := NewUseCase(repo, env, &stubTester{}, nil, nil)

	got, src, err := uc.EffectiveSettingsWithSource(context.Background(), infra.ServiceOMDB)
	if err != nil {
		t.Fatalf("EffectiveSettingsWithSource: %v", err)
	}
	if got.APIKey != "env-key" {
		t.Fatalf("PRD §10.4.4 broken: env must win, got %q", got.APIKey)
	}
	if src.APIKey != infra.FieldSourceEnv {
		t.Fatalf("Source = %q want env", src.APIKey)
	}
}

func TestSchemeAndHostOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw    string
		scheme string
		host   string
	}{
		{"", "", ""},
		{"socks5://proxy.example:1080", "socks5", "proxy.example:1080"},
		{"http://user:pass@proxy.example:8080/path", "http", "proxy.example:8080"},
		{"HTTPS://host:443?x=1", "https", "host:443"},
		{"socks4://h:1", "socks4", "h:1"},
	}
	for _, tc := range cases {
		if got := schemeOf(tc.raw); got != tc.scheme {
			t.Errorf("schemeOf(%q) = %q, want %q", tc.raw, got, tc.scheme)
		}
		if got := hostOf(tc.raw); got != tc.host {
			t.Errorf("hostOf(%q) = %q, want %q", tc.raw, got, tc.host)
		}
	}
}

// Story 489 (B-17): Upsert with a non-empty TMDB key runs the inline
// validate-on-save probe; OutcomeAuthFailed rejects the save with
// ErrInvalidExternalKey and does NOT persist.
func TestUpsert_TMDB_ValidatesKeyAndRejects401(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	tester := &stubTester{outcome: infra.OutcomeAuthFailed, msg: "401 Invalid API key"}
	uc := NewUseCase(repo, nil, tester, nil, nil)
	key := "garbage-key"
	_, err := uc.Upsert(context.Background(), infra.ServiceTMDB, UpsertInput{
		Enabled: true,
		APIKey:  &key,
	})
	if !errors.Is(err, ErrInvalidExternalKey) {
		t.Fatalf("expected ErrInvalidExternalKey, got %v", err)
	}
	if len(repo.upserts) != 0 {
		t.Fatalf("expected zero persist calls on 401, got %d", len(repo.upserts))
	}
	if tester.calls != 1 {
		t.Fatalf("expected tester called once, got %d", tester.calls)
	}
	// Cache stamped invalid_key for the list endpoint.
	views, lerr := uc.List(context.Background())
	if lerr != nil {
		t.Fatalf("List: %v", lerr)
	}
	var tmdbView MaskedView
	for _, v := range views {
		if v.Service == infra.ServiceTMDB {
			tmdbView = v
		}
	}
	if tmdbView.LastValidationStatus != "invalid_key" {
		t.Fatalf("expected LastValidationStatus=invalid_key, got %q", tmdbView.LastValidationStatus)
	}
	if tmdbView.LastValidationAt == nil {
		t.Fatalf("expected LastValidationAt set")
	}
}

// Story 489 (B-17): Upsert with APIKey=nil leaves the existing key
// untouched and SKIPS the inline validate-on-save probe (the operator
// is only adjusting other fields).
func TestUpsert_TMDB_NoKeyChange_SkipsValidation(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	repo.row[infra.ServiceTMDB] = infra.Settings{
		Service: infra.ServiceTMDB, APIKey: "existing", APIKeyLast4: "ting",
	}
	tester := &stubTester{outcome: infra.OutcomeAuthFailed}
	uc := NewUseCase(repo, nil, tester, nil, nil)
	_, err := uc.Upsert(context.Background(), infra.ServiceTMDB, UpsertInput{
		Enabled: true,
		APIKey:  nil, // omit — no validation
	})
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if tester.calls != 0 {
		t.Fatalf("expected tester NOT called when api_key nil, got %d calls", tester.calls)
	}
	if len(repo.upserts) != 1 {
		t.Fatalf("expected one persist call, got %d", len(repo.upserts))
	}
}

// Story 489 (B-17): Upsert with APIKey="" is a clear-secret op — also
// skips validation (nothing to probe).
func TestUpsert_TMDB_EmptyKey_SkipsValidation(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	repo.row[infra.ServiceTMDB] = infra.Settings{
		Service: infra.ServiceTMDB, APIKey: "existing", APIKeyLast4: "ting",
	}
	tester := &stubTester{outcome: infra.OutcomeAuthFailed}
	uc := NewUseCase(repo, nil, tester, nil, nil)
	empty := ""
	_, err := uc.Upsert(context.Background(), infra.ServiceTMDB, UpsertInput{
		Enabled: false,
		APIKey:  &empty,
	})
	if err != nil {
		t.Fatalf("expected nil err on clear, got %v", err)
	}
	if tester.calls != 0 {
		t.Fatalf("expected tester NOT called on empty key, got %d calls", tester.calls)
	}
}

// Story 489 (B-17): OMDb + TVDB do NOT trigger the inline validation
// probe — the validation cache is TMDB-specific (B-17 scope).
func TestUpsert_NonTMDB_SkipsValidation(t *testing.T) {
	t.Parallel()
	for _, svc := range []infra.Service{infra.ServiceOMDB, infra.ServiceTVDB} {
		repo := newStubRepo()
		tester := &stubTester{outcome: infra.OutcomeAuthFailed}
		uc := NewUseCase(repo, nil, tester, nil, nil)
		key := "some-key"
		_, err := uc.Upsert(context.Background(), svc, UpsertInput{
			Enabled: true,
			APIKey:  &key,
		})
		if err != nil {
			t.Fatalf("svc=%s expected nil err, got %v", svc, err)
		}
		if tester.calls != 0 {
			t.Fatalf("svc=%s expected tester NOT called, got %d", svc, tester.calls)
		}
	}
}

// Story 489 (B-17): ReportAuthFailure (the tmdb.AuthFailureReporter
// hook) stamps the validation cache and surfaces on the next List
// poll. Mirrors the live 401 hot path.
func TestReportAuthFailure_SurfacesInList(t *testing.T) {
	t.Parallel()
	uc := NewUseCase(newStubRepo(), nil, nil, nil, nil)
	uc.ReportAuthFailure("tmdb", "401 Invalid API key")
	views, err := uc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var tmdbView MaskedView
	for _, v := range views {
		if v.Service == infra.ServiceTMDB {
			tmdbView = v
		}
	}
	if tmdbView.LastValidationStatus != "invalid_key" {
		t.Fatalf("expected LastValidationStatus=invalid_key, got %q", tmdbView.LastValidationStatus)
	}
	if tmdbView.LastValidationAt == nil {
		t.Fatalf("expected LastValidationAt set")
	}
	if tmdbView.LastValidationMessage != "401 Invalid API key" {
		t.Fatalf("unexpected LastValidationMessage: %q", tmdbView.LastValidationMessage)
	}
}

// Story 489 (B-17): an unknown service slug in ReportAuthFailure is a
// silent no-op (defensive — TMDB client hard-codes "tmdb" but a typo
// elsewhere must not crash the use case).
func TestReportAuthFailure_UnknownServiceNoOp(t *testing.T) {
	t.Parallel()
	uc := NewUseCase(newStubRepo(), nil, nil, nil, nil)
	uc.ReportAuthFailure("imdb", "anything")
	views, err := uc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, v := range views {
		if v.LastValidationStatus != "" {
			t.Fatalf("svc=%s expected empty validation status, got %q", v.Service, v.LastValidationStatus)
		}
	}
}

// Story 489 (B-17): a successful operator-driven Test clears the
// invalid_key flag (UX symmetry — banner/badge disappear on next poll).
func TestTest_OutcomeOK_ClearsInvalidKeyFlag(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	repo.row[infra.ServiceTMDB] = infra.Settings{Service: infra.ServiceTMDB, APIKey: "k"}
	tester := &stubTester{outcome: infra.OutcomeOK, msg: "ok"}
	uc := NewUseCase(repo, nil, tester, nil, nil)
	// Seed with a prior invalid_key from a live 401.
	uc.ReportAuthFailure("tmdb", "401 was here")
	// Now operator clicks Test → ok.
	if _, err := uc.Test(context.Background(), infra.ServiceTMDB); err != nil {
		t.Fatalf("Test: %v", err)
	}
	views, err := uc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, v := range views {
		if v.Service == infra.ServiceTMDB {
			if v.LastValidationStatus != "valid" {
				t.Fatalf("expected LastValidationStatus=valid, got %q", v.LastValidationStatus)
			}
		}
	}
}

// Story 497 (B-35): boot validation stamps "valid" when TMDB probe
// succeeds and "invalid_key" when OMDb probe returns 401. Both
// services configured.
func TestValidateConfiguredKeysOnBoot_StampsValidAndInvalidKey(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	repo.row[infra.ServiceTMDB] = infra.Settings{
		Service: infra.ServiceTMDB, APIKey: "good-tmdb", APIKeyLast4: "tmdb",
	}
	repo.row[infra.ServiceOMDB] = infra.Settings{
		Service: infra.ServiceOMDB, APIKey: "bad-omdb", APIKeyLast4: "mdb1",
	}
	// One tester drives both services; outcome is stamped by Service
	// in the stub's recorded call. Real-world the realTester picks
	// the right probe via Settings.Service. We split outcomes by
	// wrapping in a switch on the merged Service field.
	tester := &perServiceTester{
		byService: map[infra.Service]testerResult{
			infra.ServiceTMDB: {outcome: infra.OutcomeOK, msg: "ok"},
			infra.ServiceOMDB: {outcome: infra.OutcomeAuthFailed, msg: "401 invalid api"},
		},
	}
	uc := NewUseCase(repo, nil, tester, nil, nil)
	uc.ValidateConfiguredKeysOnBoot(context.Background())
	views, err := uc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var tmdbView, omdbView MaskedView
	for _, v := range views {
		switch v.Service {
		case infra.ServiceTMDB:
			tmdbView = v
		case infra.ServiceOMDB:
			omdbView = v
		}
	}
	if tmdbView.LastValidationStatus != "valid" {
		t.Fatalf("TMDB expected valid, got %q", tmdbView.LastValidationStatus)
	}
	if tmdbView.LastValidationAt == nil {
		t.Fatalf("TMDB expected LastValidationAt set")
	}
	if omdbView.LastValidationStatus != "invalid_key" {
		t.Fatalf("OMDb expected invalid_key, got %q", omdbView.LastValidationStatus)
	}
	if omdbView.LastValidationMessage != "401 invalid api" {
		t.Fatalf("OMDb unexpected message: %q", omdbView.LastValidationMessage)
	}
	// TVDB MUST remain unstamped (out of scope per Decision §2).
	for _, v := range views {
		if v.Service == infra.ServiceTVDB && v.LastValidationStatus != "" {
			t.Fatalf("TVDB must not be probed on boot, got status=%q", v.LastValidationStatus)
		}
	}
	if tester.calls(infra.ServiceTVDB) != 0 {
		t.Fatalf("TVDB tester must not be called on boot, got %d", tester.calls(infra.ServiceTVDB))
	}
}

// Story 497 (B-35): services with empty APIKey are silently skipped.
// No tester call, no stamp.
func TestValidateConfiguredKeysOnBoot_SkipsUnconfigured(t *testing.T) {
	t.Parallel()
	repo := newStubRepo() // empty rows + no env
	tester := &perServiceTester{}
	uc := NewUseCase(repo, nil, tester, nil, nil)
	uc.ValidateConfiguredKeysOnBoot(context.Background())
	if tester.totalCalls() != 0 {
		t.Fatalf("expected zero tester calls when no key configured, got %d", tester.totalCalls())
	}
	views, err := uc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, v := range views {
		if v.LastValidationStatus != "" {
			t.Fatalf("svc=%s expected empty validation status, got %q", v.Service, v.LastValidationStatus)
		}
	}
}

// Story 497 (B-35): transient outcomes (network/timeout/proxy_failed/
// dns_blocked) do NOT stamp the cache. Operator-driven Test() or the
// live 401 hot path repopulates the cache later. (Decision §4.)
func TestValidateConfiguredKeysOnBoot_TransientNoStamp(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	repo.row[infra.ServiceTMDB] = infra.Settings{
		Service: infra.ServiceTMDB, APIKey: "k", APIKeyLast4: "k",
	}
	repo.row[infra.ServiceOMDB] = infra.Settings{
		Service: infra.ServiceOMDB, APIKey: "k", APIKeyLast4: "k",
	}
	tester := &perServiceTester{
		byService: map[infra.Service]testerResult{
			infra.ServiceTMDB: {outcome: infra.OutcomeTimeout, msg: "ctx done"},
			infra.ServiceOMDB: {outcome: infra.OutcomeProxyFailed, msg: "proxy boom"},
		},
	}
	uc := NewUseCase(repo, nil, tester, nil, nil)
	uc.ValidateConfiguredKeysOnBoot(context.Background())
	views, err := uc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, v := range views {
		if v.LastValidationStatus != "" {
			t.Fatalf("svc=%s expected NO stamp on transient outcome, got %q",
				v.Service, v.LastValidationStatus)
		}
	}
	// Tester WAS called — we just refused to stamp the result.
	if tester.calls(infra.ServiceTMDB) != 1 || tester.calls(infra.ServiceOMDB) != 1 {
		t.Fatalf("expected one tester call per configured service, got tmdb=%d omdb=%d",
			tester.calls(infra.ServiceTMDB), tester.calls(infra.ServiceOMDB))
	}
}

// Story 497 (B-35): the per-probe deadline is bootValidationTimeout
// (30s — more generous than the 5s testTimeout used by Upsert/Test).
// We assert by capturing the deadline the tester observed.
func TestValidateConfiguredKeysOnBoot_AppliesBootTimeout(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	repo.row[infra.ServiceTMDB] = infra.Settings{
		Service: infra.ServiceTMDB, APIKey: "k", APIKeyLast4: "k",
	}
	tester := &perServiceTester{
		byService: map[infra.Service]testerResult{
			infra.ServiceTMDB: {outcome: infra.OutcomeOK},
		},
	}
	uc := NewUseCase(repo, nil, tester, nil, nil)
	start := time.Now()
	uc.ValidateConfiguredKeysOnBoot(context.Background())
	dls := tester.deadlinesFor(infra.ServiceTMDB)
	if len(dls) != 1 {
		t.Fatalf("expected exactly one deadline, got %d", len(dls))
	}
	got := dls[0]
	// boot timeout is 30s; small slack for scheduler latency.
	min := start.Add(bootValidationTimeout - time.Second)
	max := start.Add(bootValidationTimeout + 2*time.Second)
	if got.Before(min) || got.After(max) {
		t.Fatalf("deadline outside bootValidationTimeout window: got=%v want in [%v,%v]",
			got, min, max)
	}
}

// Story 497 (B-35): TMDB + OMDb probes run in parallel (errgroup).
// We assert by giving each tester a 200ms sleep — sequential would
// take ≥400ms; parallel should land near 200ms (with slack).
func TestValidateConfiguredKeysOnBoot_RunsInParallel(t *testing.T) {
	t.Parallel()
	repo := newStubRepo()
	repo.row[infra.ServiceTMDB] = infra.Settings{
		Service: infra.ServiceTMDB, APIKey: "k", APIKeyLast4: "k",
	}
	repo.row[infra.ServiceOMDB] = infra.Settings{
		Service: infra.ServiceOMDB, APIKey: "k", APIKeyLast4: "k",
	}
	tester := &perServiceTester{
		byService: map[infra.Service]testerResult{
			infra.ServiceTMDB: {outcome: infra.OutcomeOK, sleep: 200 * time.Millisecond},
			infra.ServiceOMDB: {outcome: infra.OutcomeOK, sleep: 200 * time.Millisecond},
		},
	}
	uc := NewUseCase(repo, nil, tester, nil, nil)
	start := time.Now()
	uc.ValidateConfiguredKeysOnBoot(context.Background())
	elapsed := time.Since(start)
	if elapsed >= 400*time.Millisecond {
		t.Fatalf("expected parallel execution (~200ms), got sequential-ish elapsed=%v", elapsed)
	}
	if tester.calls(infra.ServiceTMDB) != 1 || tester.calls(infra.ServiceOMDB) != 1 {
		t.Fatalf("expected one call per service, got tmdb=%d omdb=%d",
			tester.calls(infra.ServiceTMDB), tester.calls(infra.ServiceOMDB))
	}
}

// perServiceTester is a per-service Tester stub used by Story 497
// (B-35) tests. Existing `stubTester` returns one outcome for all
// services; the boot kick exercises TMDB + OMDb with potentially
// different outcomes, so we need a richer stub. Mutex-guarded because
// the boot kick fans out to two goroutines via errgroup.
type perServiceTester struct {
	mu        sync.Mutex
	byService map[infra.Service]testerResult
	seenCalls map[infra.Service]int
	deadlines map[infra.Service][]time.Time
}

type testerResult struct {
	outcome infra.Outcome
	msg     string
	sleep   time.Duration
}

func (t *perServiceTester) Test(ctx context.Context, s infra.Settings) (infra.Outcome, string, time.Duration) {
	t.mu.Lock()
	if t.seenCalls == nil {
		t.seenCalls = make(map[infra.Service]int)
	}
	if t.deadlines == nil {
		t.deadlines = make(map[infra.Service][]time.Time)
	}
	t.seenCalls[s.Service]++
	if dl, ok := ctx.Deadline(); ok {
		t.deadlines[s.Service] = append(t.deadlines[s.Service], dl)
	}
	res := t.byService[s.Service]
	t.mu.Unlock()

	if res.sleep > 0 {
		select {
		case <-time.After(res.sleep):
		case <-ctx.Done():
			return infra.OutcomeTimeout, "ctx done", 0
		}
	}
	return res.outcome, res.msg, 0
}

func (t *perServiceTester) calls(svc infra.Service) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.seenCalls[svc]
}

func (t *perServiceTester) totalCalls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, v := range t.seenCalls {
		n += v
	}
	return n
}

func (t *perServiceTester) deadlinesFor(svc infra.Service) []time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]time.Time(nil), t.deadlines[svc]...)
}
