package externalservices

import (
	"context"
	"errors"
	"testing"
	"time"

	apports "github.com/alexmorbo/seasonfill/application/ports"
	infra "github.com/alexmorbo/seasonfill/infrastructure/externalservices"
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
}

func (t *stubTester) Test(ctx context.Context, _ infra.Settings) (infra.Outcome, string, time.Duration) {
	t.gotCtx = ctx
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
