// Package tmdb wraps the four TMDB endpoints the series_enrichment_worker
// (C-2), person_enrichment_worker (C-3), and orphan-resolution path
// consume. The package owns three concerns:
//
//  1. HTTP transport: Bearer-token auth, retry/backoff on 5xx,
//     Retry-After-honouring 429 handling, 5 rps self-cap. Every
//     network call goes through the injected *http.Client (proxy-
//     aware, built by infrastructure/externalservices.HttpClientFor).
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
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is TMDB v3 production endpoint. Override only in
// tests (httptest.NewServer).
const DefaultBaseURL = "https://api.themoviedb.org/3"

// DefaultLanguage is the BCP-47 tag baked into every request that
// omits an explicit `language` argument. en-US matches Sonarr's
// canonical TVDB ordering — keeps the en-side mapper output
// stable when an operator's UI is in any language.
const DefaultLanguage = "en-US"

// rateLimitRPS is the self-cap mandated by PRD §5.5. TMDB's real
// limit is ~40 rps but we share a single bucket across every
// enrichment worker process to stay polite, leaving headroom for
// future expansion (e.g. cold-start backfill).
const rateLimitRPS = 5

// maxAttempts is the total request count (1 initial + 2 retries).
// Matches the story scope.
const maxAttempts = 3

// retryBackoffCap is the hard ceiling on any single sleep between
// retries — applied to BOTH the expo backoff AND a Retry-After
// header value (PRD §5.5 acceptance criterion).
const retryBackoffCap = 60 * time.Second

// Client is the TMDB v3 wrapper. Construct via New; close via Close.
//
// Concurrency: every method is safe for concurrent use. The rate
// limiter serialises outbound calls at 5 rps regardless of the
// caller goroutine count.
//
// Lifetime: callers MUST call Close() at shutdown to stop the
// rate-limiter refill goroutine. Reload-bus subscribers should
// Close() the old Client before swapping in a new one (token /
// proxy change).
type Client struct {
	baseURL    string
	token      string
	lang       string
	httpClient *http.Client
	limiter    *tokenBucket
	clock      func() time.Time
	sleep      func(ctx context.Context, d time.Duration) error
}

// Config holds the constructor arguments. BaseURL defaults to
// DefaultBaseURL. Language defaults to DefaultLanguage. HTTPClient
// is REQUIRED — pass the one built by
// infrastructure/externalservices.HttpClientFor.
type Config struct {
	BaseURL    string
	Token      string
	Language   string
	HTTPClient *http.Client
}

// New constructs a Client. Returns an error when Token or HTTPClient
// is missing — both are required for any real call.
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
	c := &Client{
		baseURL:    strings.TrimRight(base, "/"),
		token:      cfg.Token,
		lang:       lang,
		httpClient: cfg.HTTPClient,
		limiter:    newTokenBucket(rateLimitRPS),
		clock:      time.Now,
		sleep:      ctxSleep,
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
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		body, retryWait, err := c.doOnce(ctx, path, query)
		if err == nil {
			return body, nil
		}
		// retryWait > 0 → caller signalled "wait this long then retry".
		// Honoured both for 429 (Retry-After) and 5xx (expo backoff).
		// A terminal error (4xx other than 429, JSON parse, ctx cancel)
		// has retryWait == 0 and we abort.
		if retryWait == 0 || attempt == maxAttempts-1 {
			return nil, err
		}
		if err := c.sleep(ctx, retryWait); err != nil {
			return nil, err
		}
	}
	// Unreachable — the loop above always returns inside.
	return nil, errors.New("tmdb: retry loop exited without verdict")
}

// doOnce performs a single HTTP request. Returns (body, 0, nil) on
// 2xx, (nil, backoff, err) when the caller should retry, or
// (nil, 0, err) on terminal failure.
func (c *Client) doOnce(ctx context.Context, path string, query url.Values) ([]byte, time.Duration, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("tmdb: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Network errors are retryable — treat them as 5xx.
		return nil, expoBackoff(0), fmt.Errorf("tmdb: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("tmdb: read body: %w", err)
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return body, 0, nil
	case resp.StatusCode == http.StatusTooManyRequests:
		// PRD §5.5 / §10.4.7: honour Retry-After if present, fall
		// back to expo backoff capped at 60s otherwise.
		wait := parseRetryAfter(resp.Header.Get("Retry-After"), c.clock())
		if wait <= 0 {
			wait = expoBackoff(0)
		}
		if wait > retryBackoffCap {
			wait = retryBackoffCap
		}
		return nil, wait, &APIError{Status: resp.StatusCode, Body: string(body)}
	case resp.StatusCode >= 500:
		return nil, expoBackoff(0), &APIError{Status: resp.StatusCode, Body: string(body)}
	default:
		// 4xx other than 429 — terminal. Includes 401 (bad token),
		// 404 (entity gone), 422 (bad request).
		return nil, 0, &APIError{Status: resp.StatusCode, Body: string(body)}
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
	d := time.Second << attempt
	if d > retryBackoffCap {
		d = retryBackoffCap
	}
	return d
}

// ctxSleep blocks for d or until ctx cancels, whichever wins.
// Injected on Client.sleep so tests can fast-forward.
func ctxSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
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
type tokenBucket struct {
	tokens chan struct{}
	stop   chan struct{}
	once   sync.Once
}

func newTokenBucket(rps int) *tokenBucket {
	tb := &tokenBucket{
		tokens: make(chan struct{}, rps),
		stop:   make(chan struct{}),
	}
	// Pre-fill so the first rps calls don't block.
	for i := 0; i < rps; i++ {
		tb.tokens <- struct{}{}
	}
	go tb.refill(rps)
	return tb
}

func (tb *tokenBucket) refill(rps int) {
	t := time.NewTicker(time.Second / time.Duration(rps))
	defer t.Stop()
	for {
		select {
		case <-tb.stop:
			return
		case <-t.C:
			select {
			case tb.tokens <- struct{}{}:
			default:
				// Bucket full — drop the refill (steady state).
			}
		}
	}
}

func (tb *tokenBucket) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tb.tokens:
		return nil
	}
}

func (tb *tokenBucket) Close() {
	tb.once.Do(func() { close(tb.stop) })
}
