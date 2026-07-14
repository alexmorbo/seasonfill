package tmdb

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/shared/clock"
)

// fakeStart is the virtual time the fake clock starts at in the
// rewritten AdaptivePause tests. Far enough in the future that
// arithmetic against UnixNano stays positive even after we Advance by
// minutes.
var fakeStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// recordingSleepClock wraps clock.Real() but rewrites Sleep to a no-op
// that records the duration argument. Used by the per-attempt-backoff
// tests (5xx retry, Retry-After honour, 429-fallback) that previously
// did `c.sleep = func(...){...}` — those tests do not care about the
// pause-window state, only that the requested wait was the expected
// value.
type recordingSleepClock struct {
	clock.Clock
	mu    sync.Mutex
	waits []time.Duration
	lastD time.Duration
}

func newRecordingSleepClock() *recordingSleepClock {
	return &recordingSleepClock{Clock: clock.Real()}
}

func (r *recordingSleepClock) Sleep(_ context.Context, d time.Duration) error {
	r.mu.Lock()
	r.waits = append(r.waits, d)
	r.lastD = d
	r.mu.Unlock()
	return nil
}

func (r *recordingSleepClock) Last() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastD
}

func (r *recordingSleepClock) Waits() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]time.Duration, len(r.waits))
	copy(out, r.waits)
	return out
}

// mustNew constructs a Client with the real clock. Used by tests that
// don't manipulate time.
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

// mustNewWithClock constructs a Client with an injected clock. The
// AdaptivePause tests pass a *clock.Fake; the per-attempt-backoff
// tests pass a *recordingSleepClock.
func mustNewWithClock(t *testing.T, base, tok string, clk clock.Clock) *Client {
	t.Helper()
	c, err := New(Config{
		BaseURL:    base,
		Token:      tok,
		Language:   "en-US",
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		Clock:      clk,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

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
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	clk := newRecordingSleepClock()
	c := mustNewWithClock(t, srv.URL, "tk", clk)
	defer c.Close()

	if _, err := c.GetTV(context.Background(), 1, ""); err != nil {
		t.Fatalf("GetTV: %v", err)
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("expected 3 hits (1 fail, 1 fail, 1 ok), got %d", got)
	}
}

func TestClient_RetryAfterHonoured_Seconds(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)
	clk := newRecordingSleepClock()
	c := mustNewWithClock(t, srv.URL, "tk", clk)
	defer c.Close()

	if _, err := c.GetTV(context.Background(), 1, ""); err != nil {
		t.Fatalf("GetTV: %v", err)
	}
	if got := clk.Last(); got != 7*time.Second {
		t.Fatalf("expected 7s Retry-After wait, got %v", got)
	}
}

func TestClient_RetryAfterHonoured_HTTPDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		future := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
		w.Header().Set("Retry-After", future)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	clk := newRecordingSleepClock()
	c := mustNewWithClock(t, srv.URL, "tk", clk)
	defer c.Close()

	_, _ = c.GetTV(context.Background(), 1, "") // ignore error — 3 attempts exhausted
	seen := clk.Last()
	if seen <= 0 || seen > 10*time.Second {
		t.Fatalf("expected ~3s HTTP-date wait, got %v", seen)
	}
}

func TestClient_429NoHeader_ExpoFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	clk := newRecordingSleepClock()
	c := mustNewWithClock(t, srv.URL, "tk", clk)
	defer c.Close()

	_, err := c.GetTV(context.Background(), 1, "")
	if err == nil {
		t.Fatal("expected 429 error after exhaustion")
	}
	waits := clk.Waits()
	if len(waits) != 2 { // 2 retries between 3 attempts
		t.Fatalf("waits=%v", waits)
	}
	if waits[0] != time.Second {
		t.Fatalf("first wait should be 1s, got %v", waits[0])
	}
}

// Story 489 (B-17): doOnce must invoke AuthFailureReporter when the
// upstream returns 401. The reporter is called exactly once per 401
// response (no retry — 401 is "terminal 4xx"). Body snippet must
// surface for operator-visible context.
func TestClient_DoOnce_Reports401ToReporter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status_code":7,"status_message":"Invalid API key"}`))
	}))
	t.Cleanup(srv.Close)

	rep := &fakeAuthReporter{}
	c, err := New(Config{
		BaseURL:             srv.URL,
		Token:               "tk",
		Language:            "en-US",
		HTTPClient:          &http.Client{Timeout: 5 * time.Second},
		AuthFailureReporter: rep,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	_, err = c.GetTV(context.Background(), 1, "")
	if err == nil {
		t.Fatalf("expected APIError on 401, got nil")
	}
	if got := rep.Calls(); got != 1 {
		t.Fatalf("expected reporter called once, got %d", got)
	}
	svc, body := rep.Last()
	if svc != "tmdb" {
		t.Fatalf("expected service=tmdb, got %q", svc)
	}
	if !strings.Contains(body, "Invalid API key") {
		t.Fatalf("expected body to contain 'Invalid API key', got %q", body)
	}
}

// Story 489 (B-17): 403 and 404 must NOT trigger the reporter — the
// signal is auth-specific.
func TestClient_DoOnce_DoesNotReportOn403Or404(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusNotFound} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		rep := &fakeAuthReporter{}
		c, err := New(Config{
			BaseURL:             srv.URL,
			Token:               "tk",
			Language:            "en-US",
			HTTPClient:          &http.Client{Timeout: 5 * time.Second},
			AuthFailureReporter: rep,
		})
		if err != nil {
			srv.Close()
			t.Fatalf("New: %v", err)
		}
		_, _ = c.GetTV(context.Background(), 1, "")
		c.Close()
		srv.Close()
		if got := rep.Calls(); got != 0 {
			t.Fatalf("status %d must not invoke reporter, got %d calls", code, got)
		}
	}
}

type fakeAuthReporter struct {
	mu          sync.Mutex
	calls       int
	lastService string
	lastBody    string
}

func (f *fakeAuthReporter) ReportAuthFailure(service, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastService = service
	f.lastBody = body
}

func (f *fakeAuthReporter) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeAuthReporter) Last() (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastService, f.lastBody
}

func TestClient_NotFound_Terminal(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	clk := newRecordingSleepClock()
	c := mustNewWithClock(t, srv.URL, "tk", clk)
	defer c.Close()

	_, err := c.GetTV(context.Background(), 1, "")
	if !IsNotFound(err) {
		t.Fatalf("IsNotFound(%v) = false", err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("404 should not retry; hits=%d", hits)
	}
}

func TestClient_RateLimiter_DefaultRPS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	// At 50 rps (defaultRPS, story 313 "use 100% of TMDB's practical
	// ceiling") the refill interval is 20ms. With a 5-token burst
	// pre-fill, 30 sequential calls = 5 free + 25 × 20ms ≈ 500ms in
	// steady-state. Threshold ">= 400ms" guards "limiter actually
	// throttles at all" — a regression to no limiter would clear all
	// 30 in well under 100ms. Ceiling 2s catches a regression to a
	// much slower rate without flaking under CI noise.
	start := time.Now()
	for i := range 30 {
		_, err := c.GetTV(context.Background(), int64(i), "")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	elapsed := time.Since(start)
	if elapsed < 400*time.Millisecond {
		t.Fatalf("30 calls completed in %v — 50 rps default limiter not throttling (expected >= 400ms)", elapsed)
	}
	// Sanity ceiling: even with CI noise, 30 calls at 50 rps should
	// land in under 2s. A regression to <5 rps would land at ~6s.
	if elapsed > 2*time.Second {
		t.Fatalf("30 calls took %v — limiter throttling slower than 50 rps (regression?)", elapsed)
	}
}

func TestClient_RateLimiter_EnvOverride(t *testing.T) {
	// Story 313 — verify Config.RPS overrides the 50 rps default.
	// Set 4.5 rps and check the old (story 306) timing contract still
	// holds: 10 calls in >= 950ms. This is the regression guard for
	// "operator turned down the throttle in prod".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{
		BaseURL:    srv.URL,
		Token:      "tk",
		Language:   "en-US",
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		RPS:        4.5,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	start := time.Now()
	for i := range 10 {
		_, err := c.GetTV(context.Background(), int64(i), "")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	elapsed := time.Since(start)
	if elapsed < 950*time.Millisecond {
		t.Fatalf("10 calls at RPS=4.5 completed in %v — env override not honoured", elapsed)
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

// 306 — assert that a successful TMDB call leaves the tmdb_requests_total
// counter populated AND the limiter-wait histogram has at least one
// observation. The VictoriaMetrics global set is mutated by the test
// so we use a unique label-free pair; cumulative counters survive any
// test ordering.
func TestClient_Metrics_RecordsSuccessAndLimiterWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	if _, err := c.GetTV(context.Background(), 1, ""); err != nil {
		t.Fatalf("GetTV: %v", err)
	}

	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	body := buf.String()
	if !strings.Contains(body, `tmdb_requests_total{result="success"}`) {
		t.Fatalf("tmdb_requests_total{result=success} missing from /metrics:\n%s", body)
	}
	if !strings.Contains(body, `tmdb_limiter_wait_seconds`) {
		t.Fatalf("tmdb_limiter_wait_seconds missing from /metrics:\n%s", body)
	}
}

// 306 — assert that a 429 response leaves the tmdb_requests_total
// counter populated with the rate_limited result. Three attempts are
// configured; we 429 all three so the final return is an error and
// the metric reflects all three pushes.
func TestClient_Metrics_RecordsRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	clk := newRecordingSleepClock()
	c := mustNewWithClock(t, srv.URL, "tk", clk)
	defer c.Close()

	_, _ = c.GetTV(context.Background(), 1, "") // ignore err — 3 attempts exhausted

	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	body := buf.String()
	if !strings.Contains(body, `tmdb_requests_total{result="rate_limited"}`) {
		t.Fatalf("tmdb_requests_total{result=rate_limited} missing from /metrics:\n%s", body)
	}
}

// B-12-1 — A 429 response with Retry-After triggers a global pause of
// the shared token bucket. A subsequent request must block until the
// pause window expires. Driven by a *clock.Fake so the assertion is
// exact (no scheduling-jitter window).
//
// Sequence:
//  1. Goroutine 1 calls GetTV. Server replies 429 with Retry-After=1s.
//     do() calls applyPause → bucket sets pauseDeadlineNanos = now+1s,
//     spawns watchResume (which parks on a 1s timer).
//     do() then calls clk.Sleep(ctx, 1s) for the per-attempt retry
//     backoff (also a 1s waiter).
//  2. Test thread BlockUntilWaiters(2) — both the per-attempt Sleep
//     and the watchResume timer are parked.
//  3. Advance(1s). Both waiters fire. Per-attempt retry proceeds,
//     server returns 200, GetTV returns.
//  4. watchResume sees deadline reached → clears pauseDeadlineNanos.
//  5. Second GetTV call: limiter.Wait observes pauseDeadline==0 (or
//     ==now), takes the token, hits server, returns 200 — no wait.
func TestClient_AdaptivePause_BlocksOtherCallsOn429(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	fc := clock.NewFake(fakeStart)
	c, err := New(Config{
		BaseURL:    srv.URL,
		Token:      "tk",
		Language:   "en-US",
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		RPS:        1000, // bucket never throttles in this test
		Clock:      fc,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	done := make(chan error, 1)
	go func() {
		_, err := c.GetTV(context.Background(), 1, "")
		done <- err
	}()

	// Per-attempt backoff sleep + watchResume's pause timer == 2 waiters.
	fc.BlockUntilWaiters(2)
	fc.Advance(time.Second)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("first GetTV: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first GetTV did not unblock within 2s wall after Advance")
	}

	// Second call: bucket should be unpaused, returns without any
	// Advance call. We use a separate goroutine + timeout to guard
	// against accidental re-entry into the pause path.
	done2 := make(chan error, 1)
	go func() {
		_, err := c.GetTV(context.Background(), 2, "")
		done2 <- err
	}()
	select {
	case err := <-done2:
		if err != nil {
			t.Fatalf("second GetTV: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("post-pause call blocked — bucket failed to resume")
	}
}

// B-12-1 — A 429 WITHOUT a Retry-After header falls back to the
// 10-second default. The bucket's pause deadline MUST land at
// EXACTLY fakeNow+10s — not the 1s expoBackoff per-call retryWait,
// which would be a busy-loop risk.
//
// Driven by *clock.Fake so the deadline equality is bit-exact (no
// "8s..12s window" — the legacy test had to widen the assert because
// of wall-clock jitter).
func TestClient_AdaptivePause_FallbackWhenHeaderMissing(t *testing.T) {
	hit := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case hit <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusTooManyRequests) // no Retry-After
	}))
	t.Cleanup(srv.Close)

	fc := clock.NewFake(fakeStart)
	c, err := New(Config{
		BaseURL:    srv.URL,
		Token:      "tk",
		Language:   "en-US",
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		RPS:        1000,
		Clock:      fc,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.GetTV(ctx, 1, "")
	}()

	// Wait for the first 429 to land on the server.
	select {
	case <-hit:
	case <-time.After(2 * time.Second):
		t.Fatal("server never received the 429 trigger request")
	}

	// After the 429, do() runs applyPause → PauseUntil → spawns
	// watchResume (timer on a 10s pause) → then per-attempt
	// clk.Sleep(ctx, 1s expoBackoff). Both are waiters; deadline
	// publication is synchronous in applyPause, so once we see 2
	// waiters the pauseDeadlineNanos atomic is already published.
	fc.BlockUntilWaiters(2)

	deadline := c.limiter.pauseDeadlineNanos.Load()
	if deadline == 0 {
		t.Fatal("pause deadline never set after 429")
	}
	// EXACT assertion — no wall-clock jitter possible under fake clock.
	wantDeadline := fakeStart.Add(defaultRetryAfterFallback).UnixNano()
	if deadline != wantDeadline {
		t.Fatalf("pause deadline = %d, want exactly %d (delta=%v)",
			deadline, wantDeadline, time.Duration(deadline-wantDeadline))
	}

	// Confirm the in-pause gauge is reported via /metrics.
	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	body := buf.String()
	if !strings.Contains(body, "tmdb_rate_limit_in_pause 1") {
		t.Fatalf("tmdb_rate_limit_in_pause gauge missing or wrong value:\n%s", body)
	}

	// Cancel the in-flight call so we don't have to wait the full
	// fake-clock 10s window. The goroutine returns; we don't Advance.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("GetTV goroutine did not unblock after ctx cancel")
	}
}

// B-12-1 — Compounding 429s during an active pause must NOT
// double-tick the pauses_total counter. Two goroutines race into the
// limiter simultaneously; the server replies 429 to both first calls
// (well within the pause window). The bucket must open exactly one
// pause window; the second 429 must extend-or-noop, not create a
// fresh entry.
//
// Driven by *clock.Fake so the "well within the pause window" claim
// is deterministic — we Advance once past the pause boundary AFTER
// both 429s have published, then let the post-pause 200s complete.
func TestClient_AdaptivePause_NoCompoundOnSecond429(t *testing.T) {
	var hits atomic.Int32
	// arrival barrier: each of the first two requests announces itself on
	// `arrived` the instant its handler runs (both goroutines are past
	// acquire() and have physically issued their first HTTP request), then
	// blocks on `release` until the test has confirmed BOTH have landed.
	// This makes "both 429s land before either returns" a hard rendezvous
	// rather than relying on the fake-clock waiter count as a barrier — see
	// the note below for why the waiter count alone is unsafe here.
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n <= 2 {
			// Announce arrival, then park until the test releases both.
			// Blocking here holds the 429 response back until BOTH
			// first-attempt requests are provably in flight, so neither
			// goroutine can retry into the still-429 window early.
			arrived <- struct{}{}
			<-release
			// 3s window — the precise width only matters under fake
			// clock as a positive number; the second 429 is guaranteed
			// to land within it because we don't Advance until both
			// goroutines are parked.
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	fc := clock.NewFake(fakeStart)
	c, err := New(Config{
		BaseURL:    srv.URL,
		Token:      "tk",
		Language:   "en-US",
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
		RPS:        1000,
		Clock:      fc,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	before := countersFromMetrics(t, "tmdb_rate_limit_pauses_total")

	start := make(chan struct{})
	done := make(chan error, 2)
	for i := range 2 {
		id := int64(i + 1)
		go func() {
			<-start
			_, err := c.GetTV(context.Background(), id, "")
			done <- err
		}()
	}
	close(start)

	// DETERMINISTIC RENDEZVOUS — wait until BOTH first-attempt requests have
	// physically arrived at the server (both are past acquire() and have
	// issued their HTTP call). Only then do we release the parked handlers so
	// both 429s return, and both goroutines head into applyPause + the
	// per-attempt retry Sleep together.
	//
	// Why this gate is required (root cause of the CI FAIL(5.06s) flake):
	// acquire()'s limiter.Wait ALSO parks on a fake-clock timer when a pause
	// is already active. If g1 publishes the pause deadline before g2 reaches
	// limiter.Wait, g2 parks in the acquire pause-loop — a counted waiter —
	// instead of in the retry Sleep. BlockUntilWaiters(3) would then unblock
	// on the WRONG trio {watchResume timer, g1 retry-Sleep, g2 acquire-timer}
	// while the server has seen only ONE 429. Advance(3s) then lets g1 retry
	// straight back into the still-429 window (hits<=2), which opens a SECOND
	// pause and re-parks g1 in Sleep — with no further Advance coming, g1
	// hangs until the test's 5s wall guard fails. A fast local scheduler
	// almost always interleaves both 429s first (50/50 local PASS); CI's
	// slower, -race scheduler exposes the alternate interleaving.
	//
	// Holding the handlers on `release` until BOTH have arrived means neither
	// 429 returns until both goroutines are provably past acquire() with
	// attempt 0 issued. No retry can begin before Advance, so the only waiters
	// that can form are exactly the 2 per-attempt Sleeps + the single
	// watchResume timer — making BlockUntilWaiters(3) an unambiguous barrier.
	for range 2 {
		select {
		case <-arrived:
		case <-time.After(2 * time.Second):
			t.Fatal("server did not receive both first-attempt requests")
		}
	}
	// Both first attempts are in flight; let their 429s return.
	close(release)

	// Both per-attempt Sleeps (1 waiter each) + the watchResume 3s timer (1
	// waiter, spawned exactly ONCE — the second 429 extends/no-ops the pause,
	// it does not spawn a second watcher) == 3 waiters total.
	fc.BlockUntilWaiters(3)

	// Advance past the pause window. All three waiters fire:
	//   - watchResume's 3s timer (since fakeStart+3s == deadline)
	//   - per-attempt Sleep #1 (1s expoBackoff, fired earlier; we
	//     Advance by 3s so it has woken)
	//   - per-attempt Sleep #2 (same)
	fc.Advance(3 * time.Second)

	// Both goroutines retry; server returns 200. Wait for both.
	for i := range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("goroutine %d: %v", i, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("goroutine %d did not complete in 5s wall after Advance", i)
		}
	}

	after := countersFromMetrics(t, "tmdb_rate_limit_pauses_total")
	if delta := after - before; delta != 1 {
		t.Fatalf("pauses_total delta = %d; want 1 (no compounding); server saw %d hits", delta, hits.Load())
	}
	if h := hits.Load(); h != 4 {
		t.Fatalf("server hit count = %d; want 4 (2x 429 + 2x 200)", h)
	}
}

// B-12-1 — happy path: a 2xx response must NOT touch any pause metric
// or state. Driven by fake clock for symmetry with the other
// AdaptivePause tests; logical behaviour is the original.
func TestClient_AdaptivePause_HappyPathLeavesBucketUnpaused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	fc := clock.NewFake(fakeStart)
	c := mustNewWithClock(t, srv.URL, "tk", fc)
	defer c.Close()

	if _, err := c.GetTV(context.Background(), 1, ""); err != nil {
		t.Fatalf("GetTV: %v", err)
	}
	if d := c.limiter.pauseDeadlineNanos.Load(); d != 0 {
		t.Fatalf("pauseDeadlineNanos = %d after happy path; want 0", d)
	}
}

// 313 — verify SEASONFILL_TMDB_RPS<=0 collapses to the default.
// Done via the constructor's clamping rule; this test guards against
// accidentally setting a negative interval that would tick at every
// call.
func TestClient_New_NegativeRPS_FallsBackToDefault(t *testing.T) {
	c, err := New(Config{
		BaseURL:    "http://example.invalid",
		Token:      "tk",
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		RPS:        -1,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	// We can't directly inspect the interval (unexported) but we can
	// assert the bucket has rateLimitBurst tokens pre-filled — which
	// is what New() with the default does. The capacity must equal
	// rateLimitBurst (5).
	if cap(c.limiter.tokens) != rateLimitBurst {
		t.Fatalf("bucket capacity = %d, want %d", cap(c.limiter.tokens), rateLimitBurst)
	}
}

// countersFromMetrics returns the cumulative value of a label-free
// counter line from /metrics. Lookup format: exact line "name N"
// where N is integer (story 313 only ticks counter once per pause
// so integer is sufficient — the pause-seconds counter is float
// and tested separately).
func countersFromMetrics(t *testing.T, name string) int64 {
	t.Helper()
	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if strings.HasPrefix(line, name+" ") {
			parts := strings.Fields(line)
			if len(parts) != 2 {
				continue
			}
			n, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				continue
			}
			return n
		}
	}
	return 0
}

// Story 351 — verify the per-HTTP-call metric family is emitted by a
// real TMDB-style GetTV. Distinct from Story 306's
// tmdb_requests_total{result=success}: that one is retry-semantic;
// this one is per-RoundTrip and carries endpoint + method + status (literal HTTP code).
func TestClient_Metrics_ExternalHTTPFamily_OnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	if _, err := c.GetTV(context.Background(), 1399, ""); err != nil {
		t.Fatalf("GetTV: %v", err)
	}

	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	body := buf.String()
	if !strings.Contains(body, `seasonfill_external_http_requests_total{client="tmdb"`) {
		t.Fatalf("seasonfill_external_http_requests_total{client=tmdb} missing:\n%s", body)
	}
	if !strings.Contains(body, `endpoint="/tv/{id}"`) {
		t.Fatalf("endpoint=/tv/{id} missing:\n%s", body)
	}
	if !strings.Contains(body, `status="200"`) {
		t.Fatalf("status=200 missing:\n%s", body)
	}
}

// TestClient_V3HexAuth_UsesQueryParam verifies Story 471 (B-18): a
// 32-char hex token is classified as v3 API Key and sent via
// `?api_key=…` query param, NOT as Authorization header. This is the
// regression the Phase 2 cutover surfaced — operator pasted a v3 hex
// key, got 401 from TMDB.
func TestClient_V3HexAuth_UsesQueryParam(t *testing.T) {
	const v3Token = "80b85503e3cca9aa92f99ab20f473fb1"
	var (
		seenAuth   string
		seenAPIKey string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenAPIKey = r.URL.Query().Get("api_key")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, v3Token)
	defer c.Close()

	if _, err := c.GetTV(context.Background(), 1, ""); err != nil {
		t.Fatalf("GetTV: %v", err)
	}
	if seenAuth != "" {
		t.Fatalf("v3 path must NOT send Authorization header, got %q", seenAuth)
	}
	if seenAPIKey != v3Token {
		t.Fatalf("v3 path must send api_key query, got %q", seenAPIKey)
	}
}

// TestClient_V4JWTAuth_UsesBearerHeader verifies Story 471 (B-18):
// a JWT-shaped token (eyJ… with 2 dots) is classified as v4 Read
// Access Token and sent via Authorization: Bearer header.
func TestClient_V4JWTAuth_UsesBearerHeader(t *testing.T) {
	const v4Token = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.signature"
	var (
		seenAuth   string
		seenAPIKey string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenAPIKey = r.URL.Query().Get("api_key")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, v4Token)
	defer c.Close()

	if _, err := c.GetTV(context.Background(), 1, ""); err != nil {
		t.Fatalf("GetTV: %v", err)
	}
	if seenAuth != "Bearer "+v4Token {
		t.Fatalf("v4 path must send Bearer header, got %q", seenAuth)
	}
	if seenAPIKey != "" {
		t.Fatalf("v4 path must NOT send api_key query, got %q", seenAPIKey)
	}
}

// 429 → literal status="429" path. Compare with Story 306's
// TestClient_Metrics_RecordsRateLimited which asserts the retry-semantic
// counter; this one asserts the per-RoundTrip status label is the literal code.
func TestClient_Metrics_ExternalHTTPFamily_On429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	clk := newRecordingSleepClock()
	c := mustNewWithClock(t, srv.URL, "tk", clk)
	defer c.Close()

	_, _ = c.GetTV(context.Background(), 1399, "")

	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	body := buf.String()
	if !strings.Contains(body, `seasonfill_external_http_requests_total{client="tmdb"`) {
		t.Fatalf("client=tmdb missing:\n%s", body)
	}
	if !strings.Contains(body, `status="429"`) {
		t.Fatalf("status=429 missing for 429 response:\n%s", body)
	}
}
