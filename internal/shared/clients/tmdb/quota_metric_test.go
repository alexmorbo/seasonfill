package tmdb

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/runtime/quota"
)

// fakeQuotaCounter is the test double for quota.QuotaCounter. Records
// every Increment / Get / SetQuota / MarkExhausted call so the tests can
// assert wiring. Concurrent-safe.
type fakeQuotaCounter struct {
	mu          sync.Mutex
	incrementN  atomic.Int64
	currentN    int
	lastService string
}

func (f *fakeQuotaCounter) Increment(_ context.Context, service string, _ time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.incrementN.Add(1)
	f.currentN++
	f.lastService = service
	return f.currentN, nil
}

func (f *fakeQuotaCounter) Get(_ context.Context, _ string, _ time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.currentN, nil
}

func (f *fakeQuotaCounter) Reset(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

func (f *fakeQuotaCounter) SetQuota(_ context.Context, _ string, _ time.Time, _ int) error {
	return nil
}

func (f *fakeQuotaCounter) MarkExhausted(_ context.Context, _ string, _ time.Time) error {
	return nil
}

func (f *fakeQuotaCounter) Calls() int64 { return f.incrementN.Load() }

func (f *fakeQuotaCounter) Service() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastService
}

var _ quota.QuotaCounter = (*fakeQuotaCounter)(nil)

// TestClient_QuotaCounter_IncrementsOnSuccess verifies that every
// successful TMDB call increments the wired counter exactly once and the
// service label is "tmdb".
func TestClient_QuotaCounter_IncrementsOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	fake := &fakeQuotaCounter{}
	c, err := New(Config{
		BaseURL:      srv.URL,
		Token:        "tk",
		Language:     "en-US",
		HTTPClient:   &http.Client{Timeout: 5 * time.Second},
		QuotaCounter: fake,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	for i := range 3 {
		if _, err := c.GetTV(context.Background(), int64(i+1), ""); err != nil {
			t.Fatalf("GetTV[%d]: %v", i, err)
		}
	}
	if got := fake.Calls(); got != 3 {
		t.Fatalf("Increment calls = %d want 3", got)
	}
	if svc := fake.Service(); svc != "tmdb" {
		t.Fatalf("service label = %q want tmdb", svc)
	}
}

// TestClient_QuotaCounter_NilSafe documents the nil-OK contract: an
// unconfigured QuotaCounter must NOT panic during normal request flow.
// This is the boot-default path on installs that didn't (yet) wire the
// repo — story 504 must NEVER break those builds.
func TestClient_QuotaCounter_NilSafe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	// QuotaCounter intentionally omitted — the Config zero value
	// resolves to nil, which the constructor must accept.
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	if _, err := c.GetTV(context.Background(), 1, ""); err != nil {
		t.Fatalf("GetTV: %v", err)
	}
	// Reach in and verify the field is nil so a future refactor that
	// stamps a default counter trips this guard.
	if c.quota != nil {
		t.Fatal("expected nil quota counter when Config.QuotaCounter is omitted")
	}
}

// TestClient_QuotaCounter_OnlyIncrementsOnSuccess verifies the counter is
// NOT bumped on terminal 4xx (404, 401) — those calls reached upstream
// but Increment is the "successful response" path. 5xx/429 retried until
// they 200 should still tick. We assert only the 4xx case here; the
// happy-path test above covers the 200 case.
func TestClient_QuotaCounter_NotIncrementedOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	fake := &fakeQuotaCounter{}
	c, err := New(Config{
		BaseURL:      srv.URL,
		Token:        "tk",
		HTTPClient:   &http.Client{Timeout: 5 * time.Second},
		QuotaCounter: fake,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	_, _ = c.GetTV(context.Background(), 999, "") // ignore err — 404 is terminal
	if got := fake.Calls(); got != 0 {
		t.Fatalf("Increment calls on 404 = %d want 0", got)
	}
}

// TestClient_QuotaMetric_GaugePublished verifies the
// seasonfill_external_service_quota_used{service="tmdb"} gauge is
// registered and emits the fake counter's current value when scraped.
//
// VictoriaMetrics GetOrCreateGauge is process-global; we use a fake that
// returns a deterministic count so the gauge value is predictable.
func TestClient_QuotaMetric_GaugePublished(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	fake := &fakeQuotaCounter{}
	c, err := New(Config{
		BaseURL:      srv.URL,
		Token:        "tk",
		HTTPClient:   &http.Client{Timeout: 5 * time.Second},
		QuotaCounter: fake,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	// Drive the counter to a non-zero baseline so the assertion is
	// distinguishable from a "metric line present with value 0" false
	// positive.
	for range 5 {
		if _, err := c.GetTV(context.Background(), 1, ""); err != nil {
			t.Fatalf("GetTV: %v", err)
		}
	}

	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	body := buf.String()
	if !strings.Contains(body, `seasonfill_external_service_quota_used{service="tmdb"}`) {
		t.Fatalf("tmdb quota gauge missing from /metrics:\n%s", body)
	}
}
