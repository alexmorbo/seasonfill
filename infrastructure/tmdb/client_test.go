package tmdb

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/observability"
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

	c := mustNew(t, srv.URL, "tk")
	defer c.Close()
	c.sleep = func(ctx context.Context, d time.Duration) error { return nil } // fast-forward

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

func TestClient_RateLimiter_50RPS_Default(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	// At 50 rps the refill interval is 20ms. 5 pre-filled + 25 refills
	// at 20ms each → 30 calls in roughly 500ms steady-state. We measure
	// 30 calls so timing variance over a longer window is more
	// reliable than the legacy 10-call window. Threshold accounts for
	// CI jitter — assert ">= 400ms" (30-5 == 25 refills × 20ms == 500ms
	// minimum; 400ms allows 20% slack).
	start := time.Now()
	for i := range 30 {
		_, err := c.GetTV(context.Background(), int64(i), "")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	elapsed := time.Since(start)
	if elapsed < 400*time.Millisecond {
		t.Fatalf("30 calls completed in %v — 50 rps limiter not throttling (expected >= 400ms)", elapsed)
	}
	// Sanity ceiling: even with CI noise, 30 calls at 50 rps should
	// land in under 2s. A regression to 5 rps would land at ~5s.
	if elapsed > 2*time.Second {
		t.Fatalf("30 calls took %v — limiter throttling more than 50 rps (regression?)", elapsed)
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
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()
	c.sleep = func(ctx context.Context, d time.Duration) error { return nil } // fast-forward

	_, _ = c.GetTV(context.Background(), 1, "") // ignore err — 3 attempts exhausted

	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	body := buf.String()
	if !strings.Contains(body, `tmdb_requests_total{result="rate_limited"}`) {
		t.Fatalf("tmdb_requests_total{result=rate_limited} missing from /metrics:\n%s", body)
	}
}

// 313 — A 429 response with Retry-After triggers a global pause of
// the shared token bucket. A subsequent request from a different
// "worker" (sequential here for determinism) blocks for the pause
// window before being served. Uses a high RPS so the bucket itself
// never throttles — the only thing that can block the second call
// is the pause.
func TestClient_AdaptivePause_BlocksOtherCallsOn429(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "1") // 1 second pause
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{
		BaseURL:    srv.URL,
		Token:      "tk",
		Language:   "en-US",
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		RPS:        1000, // bucket never throttles in this test
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	// First call: hits 429, sleeps Retry-After, retries successfully.
	// We don't fast-forward c.sleep because we want the real pause path
	// to engage. With Retry-After=1s the whole test takes ~1s.
	start := time.Now()
	if _, err := c.GetTV(context.Background(), 1, ""); err != nil {
		t.Fatalf("first GetTV: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 900*time.Millisecond {
		t.Fatalf("first call returned in %v — Retry-After=1s not honoured", elapsed)
	}
	// Bucket should be unpaused now. Second call must return immediately.
	start = time.Now()
	if _, err := c.GetTV(context.Background(), 2, ""); err != nil {
		t.Fatalf("second GetTV: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("post-pause call took %v — bucket failed to resume", elapsed)
	}
}

// 313 — A 429 WITHOUT a Retry-After header falls back to the
// 10-second default. Verifies the pause-fallback path and that the
// in-pause gauge flips. The pause-window MUST be the 10s fallback,
// not the per-call expoBackoff (1s) — that would be a busy-loop risk.
//
// The test samples the bucket's pause deadline AS SOON AS the first
// 429 has been processed (signalled by the server seeing one hit),
// then cancels the request so we don't have to wait the full 10s
// fallback window. The pause deadline is independent of the cancel —
// it's already published on the bucket.
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

	c, err := New(Config{
		BaseURL:    srv.URL,
		Token:      "tk",
		Language:   "en-US",
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		RPS:        1000,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	beforeNanos := time.Now().UnixNano()
	go func() {
		defer close(done)
		_, _ = c.GetTV(ctx, 1, "")
	}()

	// Wait for the first 429 to land + pause to be applied.
	select {
	case <-hit:
	case <-time.After(2 * time.Second):
		t.Fatal("server never received the 429 trigger request")
	}
	// Spin briefly until the bucket publishes the deadline (applyPause
	// runs synchronously in do() after doOnce returns; this is a very
	// short window).
	var deadline int64
	deadlineSet := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadlineSet) {
		deadline = c.limiter.pauseDeadlineNanos.Load()
		if deadline != 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if deadline == 0 {
		t.Fatal("pause deadline never set after 429")
	}

	// Confirm the deadline reflects the 10s fallback (NOT the 1s
	// expoBackoff that retryWait would carry). Some tens of ms may have
	// elapsed between beforeNanos and the pause being applied — allow a
	// generous window but reject anything <8s (which would indicate the
	// retryWait-coupled regression).
	expectedMinNanos := beforeNanos + int64(8*time.Second)
	expectedMaxNanos := beforeNanos + int64(12*time.Second)
	if deadline < expectedMinNanos || deadline > expectedMaxNanos {
		t.Fatalf("pause deadline outside expected 10s fallback window: deadline=%d, expected [%d, %d] (delta_seconds_low=%v, delta_seconds_high=%v)",
			deadline, expectedMinNanos, expectedMaxNanos,
			time.Duration(deadline-beforeNanos), time.Duration(deadline-beforeNanos))
	}

	// Confirm the in-pause gauge is reported via /metrics.
	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	body := buf.String()
	if !strings.Contains(body, "tmdb_rate_limit_in_pause 1") {
		t.Fatalf("tmdb_rate_limit_in_pause gauge missing or wrong value:\n%s", body)
	}

	// Cancel the in-flight call (it's waiting on the bucket pause). The
	// goroutine returns; we don't have to wait the full 10s fallback.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("GetTV goroutine did not unblock after ctx cancel")
	}
}

// 313 — Compounding 429s during an active pause must NOT double-tick
// the pauses_total counter. The invariant: when two goroutines hit
// 429 NEARLY-SIMULTANEOUSLY (before the first pause window expires),
// only ONE pause window is opened, not two.
//
// We fire two concurrent goroutines. The server replies 429 to BOTH
// first calls (well within the pause window). The bucket should open
// exactly one pause window; the second 429 must extend-or-noop, not
// create a fresh pause entry.
//
// A short Retry-After (200ms) keeps the test fast — long enough to
// guarantee the second 429 lands before the pause expires, short
// enough that the post-pause success retries finish quickly.
func TestClient_AdaptivePause_NoCompoundOnSecond429(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		// First two responses 429 (one per goroutine's first attempt).
		// Subsequent responses 200 so the retries succeed and the
		// goroutines exit cleanly.
		if n <= 2 {
			w.Header().Set("Retry-After", "1") // 1s — same window for both 429s
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{
		BaseURL:    srv.URL,
		Token:      "tk",
		Language:   "en-US",
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		RPS:        1000,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	before := countersFromMetrics(t, "tmdb_rate_limit_pauses_total")

	// Synchronise the two goroutines so they race into the limiter
	// together. The bucket has 5 pre-filled tokens — both will pass
	// Wait() with no delay, then both will hit the server within
	// microseconds of each other.
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

	// Wait for both goroutines to complete. With Retry-After=1s + retry
	// loop, each goroutine finishes in ~1-2s. Bound the test at 5s.
	for i := range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("goroutine %d: %v", i, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("goroutine %d did not complete in 5s", i)
		}
	}

	after := countersFromMetrics(t, "tmdb_rate_limit_pauses_total")

	// Both 429s landed within the FIRST 1s pause window. Counter delta
	// must be exactly 1 (not 2 — no compounding). hits should be 4: 2
	// initial 429s + 2 post-pause 200s.
	if delta := after - before; delta != 1 {
		t.Fatalf("pauses_total delta = %d; want 1 (no compounding); server saw %d hits", delta, hits.Load())
	}
	if h := hits.Load(); h != 4 {
		t.Fatalf("server hit count = %d; want 4 (2x 429 + 2x 200)", h)
	}
}

// 313 — happy path: a 2xx response must NOT touch any pause metric or
// state. Regression guard against accidentally entering the pause
// branch on a non-429 outcome.
func TestClient_AdaptivePause_HappyPathLeavesBucketUnpaused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	c := mustNew(t, srv.URL, "tk")
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

// 429 → literal status="429" path. Compare with Story 306's
// TestClient_Metrics_RecordsRateLimited which asserts the retry-semantic
// counter; this one asserts the per-RoundTrip status label is the literal code.
func TestClient_Metrics_ExternalHTTPFamily_On429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()
	c.sleep = func(ctx context.Context, d time.Duration) error { return nil }

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
