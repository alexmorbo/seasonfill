package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient_BearerAuth(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	_, err := c.GetTV(context.Background(), 1, "")
	if err != nil {
		t.Fatalf("GetTV: %v", err)
	}
	if seen != "Bearer tk" {
		t.Fatalf("auth header = %q", seen)
	}
}

func TestClient_RetryOn5xx(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	c := mustNew(t, srv.URL, "tk")
	defer c.Close()
	c.sleep = func(ctx context.Context, d time.Duration) error { return nil } // fast-forward

	if _, err := c.GetTV(context.Background(), 1, ""); err != nil {
		t.Fatalf("GetTV: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("expected 3 hits (1 fail, 1 fail, 1 ok), got %d", got)
	}
}

func TestClient_RetryAfterHonoured_Seconds(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	var waited time.Duration
	c.sleep = func(ctx context.Context, d time.Duration) error { waited = d; return nil }

	if _, err := c.GetTV(context.Background(), 1, ""); err != nil {
		t.Fatalf("GetTV: %v", err)
	}
	if waited != 7*time.Second {
		t.Fatalf("expected 7s Retry-After wait, got %v", waited)
	}
}

func TestClient_RetryAfterHonoured_HTTPDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		future := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
		w.Header().Set("Retry-After", future)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	var seen time.Duration
	c.sleep = func(ctx context.Context, d time.Duration) error { seen = d; return nil }

	_, _ = c.GetTV(context.Background(), 1, "") // ignore error — 3 attempts exhausted
	if seen <= 0 || seen > 10*time.Second {
		t.Fatalf("expected ~3s HTTP-date wait, got %v", seen)
	}
}

func TestClient_429NoHeader_ExpoFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	var waits []time.Duration
	c.sleep = func(ctx context.Context, d time.Duration) error { waits = append(waits, d); return nil }

	_, err := c.GetTV(context.Background(), 1, "")
	if err == nil {
		t.Fatal("expected 429 error after exhaustion")
	}
	if len(waits) != 2 { // 2 retries between 3 attempts
		t.Fatalf("waits=%v", waits)
	}
	if waits[0] != time.Second {
		t.Fatalf("first wait should be 1s, got %v", waits[0])
	}
}

func TestClient_NotFound_Terminal(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()
	c.sleep = func(ctx context.Context, d time.Duration) error { return nil }

	_, err := c.GetTV(context.Background(), 1, "")
	if !IsNotFound(err) {
		t.Fatalf("IsNotFound(%v) = false", err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("404 should not retry; hits=%d", hits)
	}
}

func TestClient_RateLimiter_5RPS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	start := time.Now()
	for i := 0; i < 10; i++ {
		_, err := c.GetTV(context.Background(), int64(i), "")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	elapsed := time.Since(start)
	// 5 pre-filled + 5 refills @ 200ms each → ~1s minimum.
	if elapsed < 800*time.Millisecond {
		t.Fatalf("10 calls completed in %v — limiter not throttling", elapsed)
	}
}

func TestClient_IncludeImageLanguages(t *testing.T) {
	cases := []struct{ lang, want string }{
		{"en-US", "en,null"},
		{"ru-RU", "ru,en,null"},
		{"de-DE", "de,en,null"},
		{"", "en,null"},
	}
	for _, tc := range cases {
		if got := includeImageLanguagesFor(tc.lang); got != tc.want {
			t.Errorf("lang=%q: got %q want %q", tc.lang, got, tc.want)
		}
	}
}

func TestClient_LanguageInQuery(t *testing.T) {
	var seenLang string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenLang = r.URL.Query().Get("language")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	if _, err := c.GetTV(context.Background(), 1, "ru-RU"); err != nil {
		t.Fatal(err)
	}
	if seenLang != "ru-RU" {
		t.Fatalf("language query = %q", seenLang)
	}
}

func mustNew(t *testing.T, base, tok string) *Client {
	t.Helper()
	c, err := New(Config{
		BaseURL:    base,
		Token:      tok,
		Language:   "en-US",
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestExpoBackoff(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{10, retryBackoffCap},
	}
	for _, tc := range cases {
		if got := expoBackoff(tc.attempt); got != tc.want {
			t.Errorf("attempt=%d: got %v want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"", 0},
		{"  ", 0},
		{"12", 12 * time.Second},
		{"-3", 0},
		{"banana", 0},
		// HTTP-date 5 seconds in the future
		{now.Add(5 * time.Second).Format(http.TimeFormat), 5 * time.Second},
	}
	for _, tc := range cases {
		if got := parseRetryAfter(tc.raw, now); got != tc.want {
			// HTTP-date has 1s resolution — allow ±1s.
			if abs(got-tc.want) > time.Second {
				t.Errorf("raw=%q: got %v want %v", tc.raw, got, tc.want)
			}
		}
	}
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
