// Package tmdb wraps the four TMDB endpoints the series_enrichment_worker
// (C-2), person_enrichment_worker (C-3), and orphan-resolution path
// consume. The package owns three concerns:
//
//  1. HTTP transport: v3/v4 auth (query param OR Bearer header),
//     retry/backoff on 5xx, Retry-After-honouring 429 handling, 5 rps
//     self-cap. Every network call goes through the injected
//     *http.Client (proxy-aware, built by
//     infrastructure/externalservices.HttpClientFor).
//  2. Raw response types: one *_types.go file per endpoint. These
//     are strictly JSON-shape structs — no business logic, no
//     time.Time (TMDB ships dates as YYYY-MM-DD strings; the mapper
//     parses them).
//  3. Mappers: pure functions turning raw responses into canon
//     domain values from stories 203–206. The mappers are the only
//     surface the application layer (via the TMDBClient port) cares
//     about; raw response types stay package-private to the caller.
//
// What this package does NOT do:
//   - Touch the DB. The mappers return domain values; persistence is
//     C-2's job.
//   - Import domain/enrichment. The merge policy (§5.4) is applied
//     one layer up by the worker.
//   - Hold goroutines other than the rate limiter's refill ticker.
//     Close() stops it.
package tmdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VictoriaMetrics/metrics"

	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/runtime/quota"
	"github.com/alexmorbo/seasonfill/internal/shared/clock"
	"github.com/alexmorbo/seasonfill/internal/shared/http/httpx"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// DefaultBaseURL is TMDB v3 production endpoint. Override only in
// tests (httptest.NewServer).
const DefaultBaseURL = "https://api.themoviedb.org/3"

// DefaultLanguage is the BCP-47 tag baked into every request that
// omits an explicit `language` argument. en-US matches Sonarr's
// canonical TVDB ordering — keeps the en-side mapper output
// stable when an operator's UI is in any language.
const DefaultLanguage = "en-US"

// defaultRPS is the Story 313 default target — TMDB has no published
// rate limit since 2019, community measurements put the practical
// per-IP ceiling at 40-50 sustained rps before 429s appear. We pick
// 50 as the "use 100% of what TMDB gives us" static cap; on 429 the
// adaptive PauseUntil (do() → tokenBucket.PauseUntil →
// applyPause) throttles all goroutines for the Retry-After window,
// so overshoot is self-correcting. Clients SHOULD NOT lower the cap
// further unless they have evidence of upstream tightening — the
// pause path already handles transient throttling correctly.
// Override via Config.RPS (env: SEASONFILL_TMDB_API_RPS).
const defaultRPS = 50.0

// rateLimitBurst is the bucket's capacity — how many calls can land
// back-to-back without waiting. Matches the historical "burst == cap
// pre-filled" behaviour so the very first burst of enrichment
// requests at boot doesn't immediately wait. Story 313: stays at 5
// (the burst is bounded by the enrichment worker count, not by RPS).
const rateLimitBurst = 5

// maxAttempts is the total request count (1 initial + 2 retries).
// Matches the story scope.
const maxAttempts = 3

// retryBackoffCap is the hard ceiling on any single sleep between
// retries — applied to BOTH the expo backoff AND a Retry-After
// header value (PRD §5.5 acceptance criterion).
const retryBackoffCap = 60 * time.Second

// defaultRetryAfterFallback is the Story 313 fallback when a 429
// arrives WITHOUT a Retry-After header (rare — TMDB sets it in
// practice). 10s mirrors the AWS SDK retry guidance for unspecified
// throttle responses and is well below retryBackoffCap so the
// 60s cap still wins on header-driven long pauses.
const defaultRetryAfterFallback = 10 * time.Second

// AuthFailureReporter receives a notification every time TMDB returns
// a 401 Unauthorized response. The reporter implementation (the
// externalservices.UseCase) stamps the in-process validationResults
// cache so the operator-facing /external-services endpoint surfaces
// the failure. Story 489 (B-17).
//
// Implementations must be safe for concurrent use — doOnce can fire
// from any worker goroutine. They MUST NOT block or perform IO; the
// reporter is called inline on the request hot path.
type AuthFailureReporter interface {
	ReportAuthFailure(service string, body string)
}

// Client is the TMDB v3 wrapper. Construct via New; close via Close.
//
// Concurrency: every method is safe for concurrent use. The rate
// limiter serialises outbound calls at Config.RPS regardless of the
// caller goroutine count. On 429 the limiter enters a GLOBAL pause
// (Story 313) — every goroutine waiting on a token blocks until the
// pause window expires.
//
// Lifetime: callers MUST call Close() at shutdown to stop the
// rate-limiter refill goroutine. Reload-bus subscribers should
// Close() the old Client before swapping in a new one (token /
// proxy change).
type Client struct {
	baseURL      string
	token        string
	authFormat   AuthFormat // Story 471 (B-18) — v3 (query) vs v4 (Bearer header).
	lang         string
	httpClient   *http.Client
	limiter      *tokenBucket
	logger       *slog.Logger
	clk          clock.Clock
	authReporter AuthFailureReporter // Story 489 (B-17) — nil-OK.
	quota        quota.QuotaCounter  // B-1 — nil-OK observability sink for daily request volume.
}

// Config holds the constructor arguments. BaseURL defaults to
// DefaultBaseURL. Language defaults to DefaultLanguage. HTTPClient
// is REQUIRED — pass the one built by
// infrastructure/externalservices.HttpClientFor.
//
// Story 313 / backlog B-2:
//   - RPS — float self-cap target. 0 → defaultRPS (50). Drives the
//     token-bucket refill interval (1s / RPS).
//   - Logger — used for tmdb.rate_limit.pause / resume INFO lines.
//     Nil-OK; falls back to slog.Default().
//
// B-12-1:
//   - Clock — injectable time source. Nil-OK; falls back to
//     clock.Real() which is a 1:1 alias for time.Now / time.NewTimer /
//     time.NewTicker / time.Sleep. Tests pass a *clock.Fake to drive
//     the rate-limiter pause window deterministically.
type Config struct {
	BaseURL    string
	Token      string
	Language   string
	HTTPClient *http.Client
	RPS        float64
	Logger     *slog.Logger
	Clock      clock.Clock
	// AuthFailureReporter is the optional 401-callback. Nil-OK — when
	// nil, doOnce skips reporting. Story 489 (B-17). The reporter must
	// be safe for concurrent use and non-blocking — see the interface
	// doc.
	AuthFailureReporter AuthFailureReporter
	// QuotaCounter is the optional observability sink for upstream
	// request volume per backlog B-1. Nil-OK — when nil, the client
	// neither Increments nor publishes the
	// seasonfill_external_service_quota_used{service="tmdb"} gauge.
	// TMDB has no published daily cap so this is observability-only:
	// the client does NOT gate calls on the counter (contrast with
	// OMDb's NewOMDbBudgetGuardDB which DOES enforce a cap). Calls
	// are counted via daily UTC windows — see quota.Daily(t, time.UTC).
	QuotaCounter quota.QuotaCounter
}

// New constructs a Client. Returns an error when Token or HTTPClient
// is missing — both are required for any real call.
//
// Story 313 / backlog B-2:
//   - cfg.RPS = 0 → defaultRPS (50). Negative is also clamped to default.
//   - cfg.Logger = nil → slog.Default() so pause/resume INFO lines
//     still surface in production.
func New(cfg Config) (*Client, error) {
	if cfg.Token == "" {
		return nil, errors.New("tmdb: empty bearer token")
	}
	if cfg.HTTPClient == nil {
		return nil, errors.New("tmdb: nil http client (use externalservices.HttpClientFor)")
	}
	base := cfg.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	lang := cfg.Language
	if lang == "" {
		lang = DefaultLanguage
	}
	rps := cfg.RPS
	if rps <= 0 {
		rps = defaultRPS
	}
	// Refill interval = 1s / rps. At 50 rps → 20ms. At 4.5 rps → 222ms.
	// time.Duration math: time.Second / rps would integer-divide, so
	// we go through float64 nanoseconds to keep sub-millisecond accuracy.
	interval := time.Duration(float64(time.Second) / rps)
	if interval <= 0 {
		interval = time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "tmdb")
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.Real()
	}

	// 471 (B-18): detect TMDB auth format at construction. TMDB
	// accepts BOTH v3 API Keys (32-char hex, query-param auth) and v4
	// Read Access Tokens (JWT, Bearer-header auth). Picking the wrong
	// wire form returns 401. The detector runs once; doOnce funnels
	// every request through ApplyAuth with the cached format.
	authFormat := DetectAuthFormat(cfg.Token)
	if authFormat == AuthFormatUnknown {
		// Visible operator hint at boot — surfaces in `kubectl logs`
		// before the first enrichment request fails with 401. Log the
		// first 4 chars only so the secret is not leaked.
		last4Hint := cfg.Token
		if len(last4Hint) > 4 {
			last4Hint = last4Hint[:4] + "…"
		}
		logger.LogAttrs(context.Background(), slog.LevelWarn, "tmdb.auth.unknown_format",
			slog.String("token_prefix", last4Hint),
			slog.String("note", "neither 32-hex (v3) nor JWT (v4); defaulting to Bearer header — TMDB may return 401"),
		)
	}
	// Story 351 — wrap the injected httpClient with the per-client
	// metrics transport. We CLONE the *http.Client so the caller's
	// shared pointer is left untouched (the media downloader uses the
	// SAME shared pointer for image.tmdb.org and needs its own
	// "tmdb_cdn"-labelled wrap — see internal/wiring/enrichment.go).
	// Jar + CheckRedirect are round-tripped even if nil — keeps the
	// clone faithful so a future externalservices.HttpClientFor change
	// that sets them doesn't silently break.
	clientWithMetrics := &http.Client{
		Transport:     httpx.NewMetricsTransport("tmdb", httpx.TMDBEndpointFor, cfg.HTTPClient.Transport),
		Timeout:       cfg.HTTPClient.Timeout,
		Jar:           cfg.HTTPClient.Jar,
		CheckRedirect: cfg.HTTPClient.CheckRedirect,
	}
	c := &Client{
		baseURL:      strings.TrimRight(base, "/"),
		token:        cfg.Token,
		authFormat:   authFormat, // 471 (B-18)
		lang:         lang,
		httpClient:   clientWithMetrics,
		limiter:      newTokenBucket(interval, rateLimitBurst, clk),
		logger:       logger,
		clk:          clk,
		authReporter: cfg.AuthFailureReporter, // 489 (B-17)
		quota:        cfg.QuotaCounter,        // B-1 — nil-OK
	}
	// Story 313 — surface tmdb.rate_limit.resume INFO via the Client's
	// logger. The bucket doesn't know about slog; we hand it a closure
	// that captures c.logger. The closure is registered AFTER the bucket
	// is constructed because the bucket field is set above; this is
	// safe because no Wait() can complete (and thus no pause can have
	// been entered) before New() returns to the caller.
	resumeHook := func(durationSec float64) {
		c.logger.LogAttrs(context.Background(), slog.LevelInfo, "tmdb.rate_limit.resume",
			slog.Float64("pause_duration_seconds", durationSec),
		)
	}
	c.limiter.onResume.Store(&resumeHook)

	// B-1 — register the generic external-service quota gauge. Mirrors
	// the OMDb wiring in internal/enrichment/app/omdb_budget.go:117.
	// VictoriaMetrics GetOrCreateGauge is idempotent on the labelled
	// name — a second client constructed during the reload subscriber
	// swap reuses the same gauge without duplicate-registration panics.
	// The callback closes over `c.quota` so when the operator clears
	// the API key (Settings → External Services → Disable) and the
	// reload subscriber rebuilds the client without a QuotaCounter,
	// the next collection no-ops to 0 (the previous client's gauge is
	// not unregistered — the subscriber Close()s the client, not the
	// metric — but it converges to 0 over the next sweep).
	if c.quota != nil {
		metrics.GetOrCreateGauge(`seasonfill_external_service_quota_used{service="tmdb"}`, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			n, err := c.quota.Get(ctx, "tmdb", quota.Daily(c.clk.Now().UTC(), time.UTC))
			if err != nil {
				return 0
			}
			return float64(n)
		})
	}
	return c, nil
}

// Close stops the rate-limiter refill goroutine. Safe to call
// multiple times. After Close the client MUST NOT be used; new
// calls panic on a closed limiter channel.
func (c *Client) Close() { c.limiter.Close() }

// do is the single transport path. Every endpoint method funnels
// through do — it owns rate-limiting, Bearer auth, retry, and 429
// handling. Returns the response body bytes ready for json.Unmarshal
// OR an *APIError when the upstream surfaced a structured error
// payload.
func (c *Client) do(ctx context.Context, path string, query url.Values) ([]byte, error) {
	for attempt := range maxAttempts {
		// Story 306 — observe wall-clock limiter wait. Always Update,
		// even on a pre-filled (zero-wait) token; the histogram's p0
		// then captures "how often did we breeze through". Story 313:
		// the limiter.Wait also blocks on the global pause deadline,
		// so the wait time now includes both bucket-empty AND
		// pause-active waits — the operator's "limiter saturation"
		// dashboard sees both as throughput cost.
		waitStart := c.clk.Now()
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		observability.ObserveTMDBLimiterWait(c.clk.Now().Sub(waitStart).Seconds())

		body, retryWait, rawRetryAfter, err := c.doOnce(ctx, path, query)
		if err == nil {
			observability.IncTMDBRequest("success")
			c.incrementQuota(ctx)
			return body, nil
		}
		// Classify the attempt's outcome for tmdb_requests_total. 429
		// (rate_limited) is counted per attempt — a 429 → 429 → 200
		// sequence yields {rate_limited:2, success:1}, which is the
		// "upstream pushed back" signal the operator wants. 5xx +
		// network + terminal 4xx all collapse to "error".
		if isRateLimitedErr(err) {
			observability.IncTMDBRequest("rate_limited")
			// Story 313 — GLOBAL PAUSE. The pause-window duration is
			// computed from the RAW Retry-After header (rawRetryAfter)
			// independent of the per-call retryWait. doOnce substitutes
			// expoBackoff for retryWait when the header is missing — but
			// that's a per-goroutine retry concern, not a fleet-wide pause
			// concern. Coupling them would shrink the pause to ~1s on a
			// header-less 429 (busy-loop risk against TMDB). We use the
			// raw header (0 if missing) + defaultRetryAfterFallback so
			// the bucket holds back for the full window TMDB needs.
			pauseFor := rawRetryAfter
			if pauseFor <= 0 {
				pauseFor = defaultRetryAfterFallback
			}
			if pauseFor > retryBackoffCap {
				pauseFor = retryBackoffCap
			}
			c.applyPause(ctx, pauseFor, path, attempt)
		} else {
			observability.IncTMDBRequest("error")
		}

		// retryWait > 0 → caller signalled "wait this long then retry".
		// Honoured both for 429 (Retry-After) and 5xx (expo backoff).
		// A terminal error (4xx other than 429, JSON parse, ctx cancel)
		// has retryWait == 0 and we abort.
		if retryWait == 0 || attempt == maxAttempts-1 {
			return nil, err
		}
		if err := c.clk.Sleep(ctx, retryWait); err != nil {
			return nil, err
		}
	}
	// Unreachable — the loop above always returns inside.
	return nil, errors.New("tmdb: retry loop exited without verdict")
}

// applyPause hands the Retry-After window to the shared token bucket
// (Story 313). Logs INFO at pause entry; the bucket's resume path
// logs INFO on exit. Idempotent under compounding 429s — the bucket's
// PauseUntil only extends, never shortens.
func (c *Client) applyPause(ctx context.Context, dur time.Duration, path string, attempt int) {
	if dur <= 0 {
		return
	}
	until := c.clk.Now().Add(dur)
	entered := c.limiter.PauseUntil(until)
	if !entered {
		// Already paused with a later or equal deadline — no new pause
		// window opened. Don't double-count metrics or double-log.
		return
	}
	c.logger.LogAttrs(ctx, slog.LevelInfo, "tmdb.rate_limit.pause",
		slog.Float64("retry_after_seconds", dur.Seconds()),
		slog.String("request_path", path),
		slog.Int("attempt", attempt),
	)
}

// isRateLimitedErr unwraps to *APIError and reports whether the
// status was 429. Used only by metric classification in do() — does
// NOT alter the retry verdict.
func isRateLimitedErr(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status == http.StatusTooManyRequests
	}
	return false
}

// doOnce performs a single HTTP request. Returns (body, 0, 0, nil) on
// 2xx, (nil, backoff, rawRetryAfter, err) when the caller should retry,
// or (nil, 0, 0, err) on terminal failure.
//
// Story 313 — the third return value `rawRetryAfter` is the unmodified
// parsed Retry-After header (0 when missing or unparseable). It is set
// ONLY on 429 responses. do() uses it to size the bucket-wide pause
// window independently of `retryWait` — `retryWait` substitutes
// expoBackoff(0)=1s when the header is missing, which is fine for this
// goroutine's next attempt but would dangerously shrink the global
// pause if reused there.
func (c *Client) doOnce(ctx context.Context, path string, query url.Values) ([]byte, time.Duration, time.Duration, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("tmdb: build request: %w", err)
	}
	// 471 (B-18): pick header vs query based on detected token format.
	// v3 (32-hex) → ?api_key=…; v4 (JWT) / Unknown → Bearer header.
	// ApplyAuth merges `api_key` into the existing query string built
	// from `query` above, so language=/append_to_response=/etc.
	// survive the auth-write step.
	ApplyAuth(req, c.token, c.authFormat)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Network errors are retryable — treat them as 5xx.
		return nil, expoBackoff(0), 0, fmt.Errorf("tmdb: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("tmdb: read body: %w", err)
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return body, 0, 0, nil
	case resp.StatusCode == http.StatusTooManyRequests:
		// PRD §5.5 / §10.4.7: honour Retry-After if present, fall
		// back to expo backoff capped at 60s otherwise. Story 313:
		// the RAW parsed Retry-After is returned separately so do()
		// can size the global pause window without inheriting the
		// expo-backoff fallback (which is per-goroutine, not fleet-wide).
		raw := parseRetryAfter(resp.Header.Get("Retry-After"), c.clk.Now())
		wait := raw
		if wait <= 0 {
			wait = expoBackoff(0)
		}
		if wait > retryBackoffCap {
			wait = retryBackoffCap
		}
		return nil, wait, raw, &APIError{Status: resp.StatusCode, Body: string(body)}
	case resp.StatusCode >= 500:
		return nil, expoBackoff(0), 0, &APIError{Status: resp.StatusCode, Body: string(body)}
	default:
		// 4xx other than 429 — terminal. Includes 401 (bad token),
		// 404 (entity gone), 422 (bad request).
		if resp.StatusCode == http.StatusUnauthorized && c.authReporter != nil {
			// Story 489 (B-17): notify the use case so /external-services
			// surfaces the failure. Truncate to 200 chars so the cache
			// stays bounded — the body is operator-facing context, not
			// an audit log. 401 (not 403): forbidden has different
			// semantics in TMDB land (region restrictions etc).
			snippet := string(body)
			if len(snippet) > 200 {
				snippet = snippet[:200] + "…"
			}
			c.authReporter.ReportAuthFailure("tmdb", snippet)
		}
		return nil, 0, 0, &APIError{Status: resp.StatusCode, Body: string(body)}
	}
}

// languageFor merges the per-call override with the client default.
// Empty per-call → default.
func (c *Client) languageFor(lang string) string {
	if lang == "" {
		return c.lang
	}
	return lang
}

// includeImageLanguagesFor builds the include_image_language query
// value for a given BCP-47 tag. Per PRD §5.5 the form is
// `{2-letter},en,null` (ru-RU → ru,en,null). The `null` tag asks
// TMDB to surface language-agnostic art (posters with no text).
func includeImageLanguagesFor(lang string) string {
	short := strings.ToLower(lang)
	if i := strings.Index(short, "-"); i > 0 {
		short = short[:i]
	}
	if short == "" || short == "en" {
		return "en,null"
	}
	return short + ",en,null"
}

// parseRetryAfter accepts the two RFC 7231 forms:
//   - delta-seconds:  "Retry-After: 120"
//   - HTTP-date:      "Retry-After: Fri, 31 Dec 1999 23:59:59 GMT"
//
// Returns 0 on empty / unparseable header — caller falls back to
// expo backoff.
func parseRetryAfter(raw string, now time.Time) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(raw); err == nil {
		d := t.Sub(now)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

// expoBackoff is the 1s/2s/4s schedule capped at retryBackoffCap.
// attempt=0 → 1s, attempt=1 → 2s, attempt=2 → 4s. We never call
// past attempt=2 because maxAttempts==3.
func expoBackoff(attempt int) time.Duration {
	d := min(time.Second<<attempt, retryBackoffCap)
	return d
}

// incrementQuota nudges the optional B-1 QuotaCounter by one. Nil-OK —
// no-ops when the counter is unconfigured. Errors are swallowed at WARN
// level: the DB-backed counter is transient-tolerant (next call retries)
// and TMDB has no hard cap to enforce, so a missed Increment is purely
// an observability gap, never a correctness one. Mirrors omdb_budget.go
// degrade-open policy.
func (c *Client) incrementQuota(ctx context.Context) {
	if c.quota == nil {
		return
	}
	window := quota.Daily(c.clk.Now().UTC(), time.UTC)
	if _, err := c.quota.Increment(ctx, "tmdb", window); err != nil {
		c.logger.LogAttrs(ctx, slog.LevelWarn, "tmdb.quota.increment_failed",
			slog.String("error", err.Error()),
		)
	}
}

// tokenBucket is a fixed-rate refill bucket. Capacity == rps,
// refill 1 token every 1/rps seconds. Wait blocks until a token
// is available or ctx cancels.
//
// Implemented as a buffered channel + a refill goroutine because
// the stdlib has no rate limiter and we want zero new dependencies
// (golang.org/x/time/rate would solve it but adds a dep just for
// this — the channel approach is ~15 LOC and equally correct).
//
// Story 313 — GLOBAL PAUSE. When upstream pushes back with 429, do()
// calls PauseUntil(t) to block all Wait()ers until t. The pause is
// stored as a Unix-nanosecond timestamp in an atomic int64 so Wait()
// can sample it lock-free. Compounding 429s extend the deadline only
// when they would land later — never shorten.
//
// Pause + token-bucket interaction: Wait() first checks the pause
// deadline; if active, it sleeps until the deadline OR ctx cancel,
// THEN consumes a token. A pause does NOT pre-consume tokens — the
// bucket fills normally during the pause, so when the pause ends
// the workers can burst at the bucket capacity before throttling
// resumes. This matches the operator's "stop, wait, then full speed"
// intent.
//
// Concurrent pause entry: pauseDeadlineNanos is an atomic int64 that
// publishes monotonically. PauseUntil's compare-and-swap loop ensures
// concurrent 429 handlers don't race to a shorter deadline. Resume
// is publication of 0 to the same atomic; the metric flips when the
// resume completes (resume goroutine inside PauseUntil — see code).
type tokenBucket struct {
	tokens chan struct{}
	stop   chan struct{}
	once   sync.Once

	// B-12-1 — injectable time source. Production = clock.Real(); tests
	// pass a *clock.Fake so the pause-window state machine is bit-exact.
	clk clock.Clock

	// Story 313 — global pause state.
	// pauseDeadlineNanos: 0 = not paused; positive = UnixNano deadline.
	pauseDeadlineNanos atomic.Int64
	// pauseGen: incremented each fresh pause entry. Distinguishes
	// "this is the same pause that's already running" from "a new
	// pause started concurrently". The resume goroutine compares gen
	// before flipping the gauge so a concurrent extend doesn't get
	// its tail clipped.
	pauseGen   atomic.Uint64
	pauseStart atomic.Int64 // UnixNano of the current pause's first entry — read by resume to compute duration
	// onResume is the optional Client-side hook fired AFTER the gauge
	// flip and the seconds-counter add. Used for the
	// tmdb.rate_limit.resume INFO line. Nil-OK (tests skip the log).
	onResume atomic.Pointer[func(durationSec float64)]
}

func newTokenBucket(interval time.Duration, capacity int, clk clock.Clock) *tokenBucket {
	if capacity < 1 {
		capacity = 1
	}
	if interval <= 0 {
		interval = time.Second
	}
	if clk == nil {
		clk = clock.Real()
	}
	tb := &tokenBucket{
		tokens: make(chan struct{}, capacity),
		stop:   make(chan struct{}),
		clk:    clk,
	}
	// Pre-fill so the first `capacity` calls don't block.
	for i := 0; i < capacity; i++ {
		tb.tokens <- struct{}{}
	}
	go tb.refill(interval)
	return tb
}

func (tb *tokenBucket) refill(interval time.Duration) {
	t := tb.clk.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-tb.stop:
			return
		case <-t.C():
			select {
			case tb.tokens <- struct{}{}:
			default:
				// Bucket full — drop the refill (steady state).
			}
		}
	}
}

// Wait blocks until (a) the pause deadline has passed AND a token is
// available, or (b) ctx cancels. The pause check is a cheap atomic
// load — uncontended fast path is the same as before (no pause).
func (tb *tokenBucket) Wait(ctx context.Context) error {
	// Story 313 — global pause check. Loop because a pause may be
	// extended while we're waiting; we re-sample the deadline after
	// each timer fires.
	for {
		deadlineNanos := tb.pauseDeadlineNanos.Load()
		if deadlineNanos == 0 {
			break
		}
		now := tb.clk.Now().UnixNano()
		if now >= deadlineNanos {
			break
		}
		remaining := time.Duration(deadlineNanos - now)
		t := tb.clk.NewTimer(remaining)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C():
			// Re-loop: check whether the pause was extended.
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tb.tokens:
		return nil
	}
}

// PauseUntil opens (or extends) a global pause window ending at
// `until`. Returns true ONLY when a fresh pause window was opened —
// i.e. when the bucket was NOT already paused with a deadline >=
// until. Returns false when:
//   - we were already paused with a deadline >= until (no extend)
//   - until is in the past or zero (no-op)
//
// The caller uses the bool to gate metric ticks (avoid compounding
// counter increments during a single window). Story 313.
func (tb *tokenBucket) PauseUntil(until time.Time) bool {
	if until.IsZero() {
		return false
	}
	newNanos := until.UnixNano()
	if newNanos <= tb.clk.Now().UnixNano() {
		return false
	}
	for {
		old := tb.pauseDeadlineNanos.Load()
		if old >= newNanos {
			// Existing pause window already lasts at least as long —
			// no metric tick, no extend. Returns false so the caller's
			// "already paused" guard kicks in.
			return false
		}
		if !tb.pauseDeadlineNanos.CompareAndSwap(old, newNanos) {
			continue
		}
		if old == 0 {
			// Fresh pause entry. Tick the counter + flip the gauge +
			// spawn the resume watcher exactly once per window. The
			// extend case (old > 0) does NOT spawn a new watcher —
			// the existing one re-reads the deadline on wakeup.
			tb.pauseStart.Store(tb.clk.Now().UnixNano())
			gen := tb.pauseGen.Add(1)
			observability.IncTMDBRateLimitPause()
			observability.SetTMDBRateLimitInPause(true)
			go tb.watchResume(gen, until)
			return true
		}
		// Extended an existing pause (old > 0 < new). Don't tick
		// counter (compounding 429s within one window), don't flip
		// gauge (it's already 1). The existing resume watcher will
		// re-read the deadline when it wakes.
		return false
	}
}

// watchResume waits for the pause to end then publishes the resume
// metric + log. Re-checks the deadline on wakeup because a concurrent
// extend may have pushed it out. Bound by gen so a second pause that
// happens to share an Until doesn't trigger two watchers stepping on
// each other's gauge writes. Story 313.
func (tb *tokenBucket) watchResume(gen uint64, until time.Time) {
	for {
		now := tb.clk.Now()
		// Re-sample deadline from the atomic to honour extends.
		deadlineNanos := tb.pauseDeadlineNanos.Load()
		if deadlineNanos == 0 {
			return // Someone else cleared the pause; bail.
		}
		deadline := time.Unix(0, deadlineNanos)
		if !now.Before(deadline) {
			break
		}
		t := tb.clk.NewTimer(deadline.Sub(now))
		select {
		case <-tb.stop:
			t.Stop()
			return
		case <-t.C():
		}
	}
	// Only the latest pause-gen watcher clears state. A stale watcher
	// from an extended pause returns silently above (deadline check),
	// but the gen guard double-checks against a brand-new pause that
	// happened to land while we were sleeping.
	if tb.pauseGen.Load() != gen {
		return
	}
	start := tb.pauseStart.Load()
	tb.pauseDeadlineNanos.Store(0)
	observability.SetTMDBRateLimitInPause(false)
	var elapsed float64
	if start > 0 {
		elapsed = tb.clk.Now().Sub(time.Unix(0, start)).Seconds()
		observability.AddTMDBRateLimitPauseSeconds(elapsed)
	}
	if hook := tb.onResume.Load(); hook != nil {
		(*hook)(elapsed)
	}
}

func (tb *tokenBucket) Close() {
	tb.once.Do(func() { close(tb.stop) })
}
