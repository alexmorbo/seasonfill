// Package omdb is the OMDb HTTP client for the Story 213 enrichment
// worker. The package surface is intentionally narrow: one client,
// one GET, one response struct + a typed error and the sentinel
// codes the worker pattern-matches on.
//
// The HTTP client is built via S-2's HttpClientFor(settings) so the
// per-service proxy / transport pool isolation carries over. The
// constructor takes the *http.Client as an argument; this package
// never imports infrastructure/externalservices directly.
package omdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/httpx"
)

// DefaultBaseURL is the production OMDb endpoint. Override only in
// tests via Config.BaseURL.
const DefaultBaseURL = "https://www.omdbapi.com"

// defaultTimeout is the per-call ceiling we stamp on the *http.Client
// before issuing the request. S-2's HttpClientFor already sets 10s;
// OMDb is fast (single endpoint, single row) so we keep the same
// budget and override only if the caller's client.Timeout is zero.
const defaultTimeout = 10 * time.Second

// Sentinel errors. Workers use errors.Is to classify outcomes.
//
//	ErrNotFound    → enrichment_errors.attempts=terminalAttempts (no retry)
//	ErrInvalidKey  → enrichment_errors retryable + auth_failed log (operator action req'd)
//	ErrDailyLimit  → enrichment_errors retryable + auth_failed log (degraded surface
//	                 picks this up; the budget guard usually prevents
//	                 reaching upstream once the in-process counter
//	                 hits zero, but a process restart resets the
//	                 counter while the upstream cap keeps accruing).
var (
	ErrNotFound   = errors.New("omdb: not found")
	ErrInvalidKey = errors.New("omdb: invalid api key")
	ErrDailyLimit = errors.New("omdb: daily limit reached")
)

// Response is the typed view of the OMDb JSON payload. Every field
// is a string per upstream contract — the mapper layer handles
// the `"N/A"` → NULL normalisation, the decimal `imdbRating` parse,
// and the comma-formatted `imdbVotes` parse. We keep the raw struct
// 1:1 with the upstream JSON shape so the mapper can be table-tested
// against fixtures without touching the network.
type Response struct {
	Title        string `json:"Title"`
	Year         string `json:"Year"`
	Rated        string `json:"Rated"`
	Released     string `json:"Released"`
	Runtime      string `json:"Runtime"`
	Genre        string `json:"Genre"`
	Director     string `json:"Director"`
	Writer       string `json:"Writer"`
	Actors       string `json:"Actors"`
	Plot         string `json:"Plot"`
	Language     string `json:"Language"`
	Country      string `json:"Country"`
	Awards       string `json:"Awards"`
	Poster       string `json:"Poster"`
	IMDBRating   string `json:"imdbRating"`
	IMDBVotes    string `json:"imdbVotes"`
	IMDBID       string `json:"imdbID"`
	Type         string `json:"Type"`
	TotalSeasons string `json:"totalSeasons"`

	// Response shape envelope. "True" on success; "False" on error
	// with the Error field populated. The string form mirrors the
	// upstream contract — we coerce to bool inside GetByIMDB.
	ResponseFlag string `json:"Response"`
	Error        string `json:"Error"`
}

// Config bundles the constructor arguments. APIKey is required;
// HTTPClient is required (built by the caller via S-2's
// HttpClientFor). BaseURL defaults to DefaultBaseURL.
type Config struct {
	APIKey     string
	HTTPClient *http.Client
	BaseURL    string
}

// Client is the OMDb HTTP client. Constructed once per (key, proxy)
// pair; the wiring layer reconstructs it when S-2 reload signals a
// settings change. Concurrency: HTTPClient is safe for concurrent
// callers (http.Client guarantee); the Client itself owns no
// mutable state.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// New constructs a Client. Returns an error when APIKey or
// HTTPClient is missing.
func New(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("omdb: api key required")
	}
	if cfg.HTTPClient == nil {
		return nil, errors.New("omdb: http client required")
	}
	base := cfg.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	if cfg.HTTPClient.Timeout == 0 {
		cfg.HTTPClient.Timeout = defaultTimeout
	}
	// Story 351 — wrap with the per-client metrics transport. OMDb is
	// single-endpoint so the EndpointFunc is a constant "/". Clone
	// the *http.Client so the caller's pointer is left untouched.
	clientWithMetrics := &http.Client{
		Transport:     httpx.NewMetricsTransport("omdb", omdbEndpointFor, cfg.HTTPClient.Transport),
		Timeout:       cfg.HTTPClient.Timeout,
		Jar:           cfg.HTTPClient.Jar,
		CheckRedirect: cfg.HTTPClient.CheckRedirect,
	}
	return &Client{
		apiKey:     cfg.APIKey,
		baseURL:    strings.TrimRight(base, "/"),
		httpClient: clientWithMetrics,
	}, nil
}

// omdbEndpointFor returns the static "/" label (OMDb is single
// endpoint — `GET /?i={imdbID}&apikey=...`). Kept as a function so it
// matches the httpx.EndpointFunc signature.
func omdbEndpointFor(*http.Request) string { return "/" }

// GetByIMDB calls GET /?i={imdb_id}&apikey={key}. Returns the parsed
// Response on Response="True"; one of the sentinel errors on the
// known failure modes; a wrapped *APIError on non-2xx HTTP; a
// wrapped network/parse error otherwise.
//
// The imdbID is passed verbatim — caller MUST normalise via
// tmdb.NormaliseIMDBID (or equivalent) before calling. Empty
// imdbID returns a programmer-error.
func (c *Client) GetByIMDB(ctx context.Context, imdbID domain.IMDBID) (*Response, error) {
	if imdbID == "" {
		return nil, errors.New("omdb: imdb id required")
	}

	q := url.Values{}
	q.Set("i", string(imdbID))
	q.Set("apikey", c.apiKey)
	q.Set("r", "json")
	full := c.baseURL + "/?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, fmt.Errorf("omdb: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("omdb: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("omdb: read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, &APIError{Status: resp.StatusCode, Body: string(body)}
	}

	var out Response
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("omdb: decode body: %w", err)
	}
	if strings.EqualFold(out.ResponseFlag, "False") {
		return nil, classifyEnvelopeError(out.Error)
	}
	return &out, nil
}

// classifyEnvelopeError maps the upstream's free-form Error string
// onto the sentinel set. Matching is case-insensitive + substring
// because the upstream wording has historically varied
// ("Incorrect IMDb ID" vs "Movie not found!" vs "Error getting data.").
// Unknown messages fall through to a generic *APIError with the
// envelope's Error string — the worker journals it as outcome=error.
func classifyEnvelopeError(msg string) error {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "not found"),
		strings.Contains(lower, "incorrect imdb"):
		return fmt.Errorf("%w: %s", ErrNotFound, msg)
	case strings.Contains(lower, "invalid api key"),
		strings.Contains(lower, "no api key"):
		return fmt.Errorf("%w: %s", ErrInvalidKey, msg)
	case strings.Contains(lower, "daily limit"),
		strings.Contains(lower, "request limit reached"):
		return fmt.Errorf("%w: %s", ErrDailyLimit, msg)
	default:
		return &APIError{Status: 0, Body: msg}
	}
}
