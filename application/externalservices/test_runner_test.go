package externalservices

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	infra "github.com/alexmorbo/seasonfill/infrastructure/externalservices"
)

func TestClassifyTransportErr_ProxyConfigWins(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("wrapped: %w", infra.ErrProxyConfig)
	if got := classifyTransportErr(err); got != infra.OutcomeProxyFailed {
		t.Fatalf("expected proxy_failed, got %s", got)
	}
}

func TestClassifyTransportErr_DeadlineExceeded(t *testing.T) {
	t.Parallel()
	if got := classifyTransportErr(context.DeadlineExceeded); got != infra.OutcomeTimeout {
		t.Fatalf("expected timeout, got %s", got)
	}
}

type fakeTimeoutErr struct{}

func (fakeTimeoutErr) Error() string   { return "i/o timeout" }
func (fakeTimeoutErr) Timeout() bool   { return true }
func (fakeTimeoutErr) Temporary() bool { return true }

func TestClassifyTransportErr_NetTimeout(t *testing.T) {
	t.Parallel()
	if got := classifyTransportErr(fakeTimeoutErr{}); got != infra.OutcomeTimeout {
		t.Fatalf("expected timeout, got %s", got)
	}
}

func TestClassifyTransportErr_DNSNoSuchHost(t *testing.T) {
	t.Parallel()
	dnsErr := &net.DNSError{Err: "no such host", Name: "api.themoviedb.org", IsNotFound: true}
	if got := classifyTransportErr(dnsErr); got != infra.OutcomeDNSBlocked {
		t.Fatalf("expected dns_blocked, got %s", got)
	}
}

func TestClassifyTransportErr_StringDNS(t *testing.T) {
	t.Parallel()
	// Some platforms wrap DNS errors so *net.DNSError doesn't unwrap.
	if got := classifyTransportErr(errors.New("dial tcp 0.0.0.0:443: connection refused")); got != infra.OutcomeDNSBlocked {
		t.Fatalf("expected dns_blocked, got %s", got)
	}
}

func TestClassifyTransportErr_ProxyConnectString(t *testing.T) {
	t.Parallel()
	if got := classifyTransportErr(errors.New("proxyconnect tcp: read: connection reset")); got != infra.OutcomeProxyFailed {
		t.Fatalf("expected proxy_failed, got %s", got)
	}
}

func TestClassifyTransportErr_GenericNetwork(t *testing.T) {
	t.Parallel()
	if got := classifyTransportErr(errors.New("read: connection reset by peer")); got != infra.OutcomeNetwork {
		t.Fatalf("expected network, got %s", got)
	}
}

func TestBuildTestRequest_TMDB(t *testing.T) {
	t.Parallel()
	req, err := buildTestRequest(context.Background(), infra.Settings{Service: infra.ServiceTMDB, APIKey: "abc"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if req.Method != "GET" || !strings.Contains(req.URL.String(), "api.themoviedb.org") {
		t.Fatalf("tmdb url wrong: %s %s", req.Method, req.URL)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer abc" {
		t.Fatalf("tmdb auth header: %q", got)
	}
}

func TestBuildTestRequest_OMDB(t *testing.T) {
	t.Parallel()
	req, err := buildTestRequest(context.Background(), infra.Settings{Service: infra.ServiceOMDB, APIKey: "k1"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if req.Method != "GET" || !strings.Contains(req.URL.String(), "omdbapi.com") {
		t.Fatalf("omdb url wrong: %s %s", req.Method, req.URL)
	}
	if !strings.Contains(req.URL.RawQuery, "apikey=k1") || !strings.Contains(req.URL.RawQuery, "i=tt0903747") {
		t.Fatalf("omdb query: %s", req.URL.RawQuery)
	}
}

func TestBuildTestRequest_TVDB(t *testing.T) {
	t.Parallel()
	req, err := buildTestRequest(context.Background(), infra.Settings{Service: infra.ServiceTVDB, APIKey: "k2"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if req.Method != "POST" || !strings.Contains(req.URL.String(), "thetvdb.com") {
		t.Fatalf("tvdb url wrong: %s %s", req.Method, req.URL)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("tvdb content-type: %q", got)
	}
}

func TestBuildTestRequest_InvalidService(t *testing.T) {
	t.Parallel()
	_, err := buildTestRequest(context.Background(), infra.Settings{Service: infra.Service("imdb")})
	if err == nil {
		t.Fatalf("expected error for invalid service")
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()
	short := "short"
	if got := truncate(short); got != short {
		t.Fatalf("short pass through: %q", got)
	}
	long := strings.Repeat("a", 250)
	got := truncate(long)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("long must be ellipsised: %q", got[len(got)-5:])
	}
	if len(got) > 210 {
		t.Fatalf("truncated too long: %d", len(got))
	}
}

func TestNewRealTester(t *testing.T) {
	t.Parallel()
	tr := NewRealTester()
	if tr == nil {
		t.Fatal("nil tester")
	}
}
