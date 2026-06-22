package externalservices

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	infra "github.com/alexmorbo/seasonfill/internal/shared/clients/externalservices"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

// realTester implements Tester by issuing the documented cheap probe
// per service:
//
//   - TMDB:  GET https://api.themoviedb.org/3/configuration with
//     Authorization: Bearer <token>.
//   - OMDb:  GET https://www.omdbapi.com/?i=tt0903747&apikey=<key>
//     (Breaking Bad — stable IMDb ID).
//   - TVDB:  POST https://api4.thetvdb.com/v4/login with
//     {"apikey":"<key>"}.
//
// Outcome classification per PRD §10.4.7 closed set:
//
//	ok | auth_failed | network | timeout | proxy_failed | dns_blocked
//
// Order of classification matters: ErrProxyConfig beats DeadlineExceeded;
// DeadlineExceeded beats generic net.OpError; DNS lookup failure beats
// generic network classification.
type realTester struct{}

// NewRealTester returns the production Tester.
func NewRealTester() Tester { return realTester{} }

func (realTester) Test(ctx context.Context, s infra.Settings) (infra.Outcome, string, time.Duration) {
	client, err := infra.HttpClientFor(s)
	if err != nil {
		return infra.OutcomeProxyFailed, truncate(err.Error()), 0
	}
	req, err := buildTestRequest(ctx, s)
	if err != nil {
		return infra.OutcomeNetwork, truncate(err.Error()), 0
	}
	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return classifyTransportErr(err), truncate(err.Error()), elapsed
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return infra.OutcomeAuthFailed, truncate(string(body)), elapsed
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return infra.OutcomeOK, "", elapsed
	default:
		return infra.OutcomeNetwork, fmt.Sprintf("http %d: %s", resp.StatusCode, truncate(string(body))), elapsed
	}
}

func buildTestRequest(ctx context.Context, s infra.Settings) (*http.Request, error) {
	switch s.Service {
	case infra.ServiceTMDB:
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			"https://api.themoviedb.org/3/configuration", nil)
		if err != nil {
			return nil, err
		}
		// 471 (B-18): TMDB accepts BOTH v3 (32-hex, query) and v4
		// (JWT, Bearer header) credentials. The connection-test
		// probe must use the same auth method the runtime client
		// would pick, or Save → Test would pass for v4 JWT and
		// fail for v3 hex even when the key is valid.
		tmdb.ApplyAuth(req, s.APIKey, tmdb.DetectAuthFormat(s.APIKey))
		return req, nil
	case infra.ServiceOMDB:
		u := url.URL{Scheme: "https", Host: "www.omdbapi.com"}
		q := u.Query()
		q.Set("i", "tt0903747")
		q.Set("apikey", s.APIKey)
		u.RawQuery = q.Encode()
		return http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	case infra.ServiceTVDB:
		body := strings.NewReader(fmt.Sprintf(`{"apikey":%q}`, s.APIKey))
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			"https://api4.thetvdb.com/v4/login", body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}
	return nil, fmt.Errorf("buildTestRequest: invalid service %q", s.Service)
}

// classifyTransportErr maps a transport error to one of the closed-set
// Outcomes. Order matters: ErrProxyConfig > DeadlineExceeded >
// net.Error.Timeout() > DNS lookup failures > generic network.
func classifyTransportErr(err error) infra.Outcome {
	if errors.Is(err, infra.ErrProxyConfig) {
		return infra.OutcomeProxyFailed
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return infra.OutcomeTimeout
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		// IsNoSuchHost == NXDOMAIN style; the RU DPI typically replies
		// with 0.0.0.0 for known upstream hosts which surfaces as
		// IsNotFound or a connection-refused on the bogus IP. Both
		// shapes land here.
		if dnsErr.IsNotFound {
			return infra.OutcomeDNSBlocked
		}
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return infra.OutcomeTimeout
	}
	// CONNECT denials / SOCKS handshake fails surface as "proxyconnect"
	// or "socks" in the stdlib + x/net packages.
	msg := err.Error()
	if strings.Contains(msg, "proxyconnect") || strings.Contains(msg, "socks") {
		return infra.OutcomeProxyFailed
	}
	// String fallbacks for DNS-shaped errors that don't unwrap into
	// *net.DNSError on every platform (e.g. socks5 dialer DNS errors).
	if strings.Contains(msg, "no such host") || strings.Contains(msg, "dial tcp 0.0.0.0") {
		return infra.OutcomeDNSBlocked
	}
	return infra.OutcomeNetwork
}

func truncate(s string) string {
	const maxLen = 200
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
