package qbit

import (
	"bytes"
	"context"
	encjson "encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VictoriaMetrics/metrics"

	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// fakeQbit emulates the qBit Web API surface this wrapper exercises:
// /api/v2/auth/login, /api/v2/torrents/info, /api/v2/torrents/trackers.
type fakeQbit struct {
	srv             *httptest.Server
	user            string
	pass            string
	loginCalls      atomic.Int32
	failLogin       atomic.Bool
	ipBanned        atomic.Bool
	torrents        []map[string]any
	trackersByHash  map[string][]map[string]any
	trackerNotFound bool
}

func newFakeQbit(user, pass string) *fakeQbit {
	f := &fakeQbit{user: user, pass: pass, trackersByHash: map[string][]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		f.loginCalls.Add(1)
		if f.ipBanned.Load() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		u := r.Form.Get("username")
		p := r.Form.Get("password")
		if f.failLogin.Load() || u != f.user || p != f.pass {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Fails."))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test-session"})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Ok."))
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		cat := r.URL.Query().Get("category")
		out := make([]map[string]any, 0, len(f.torrents))
		for _, t := range f.torrents {
			if cat != "" && fmt.Sprint(t["category"]) != cat {
				continue
			}
			out = append(out, t)
		}
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, out)
	})
	mux.HandleFunc("/api/v2/torrents/trackers", func(w http.ResponseWriter, r *http.Request) {
		hash := r.URL.Query().Get("hash")
		if f.trackerNotFound {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		trk, ok := f.trackersByHash[hash]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, trk)
	})
	f.srv = httptest.NewServer(mux)
	return f
}

func (f *fakeQbit) close() { f.srv.Close() }

func writeJSON(w http.ResponseWriter, v any) {
	if err := encjson.NewEncoder(w).Encode(v); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func TestNewClient_Validation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  Config
		want error
	}{
		{"empty url", Config{}, ErrInvalidConfig},
		{"bad scheme", Config{URL: "ftp://qbit"}, ErrInvalidConfig},
		{"empty host", Config{URL: "http://"}, ErrInvalidConfig},
		{"parse error", Config{URL: "http://%zzz"}, ErrInvalidConfig},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewClient(tc.cfg)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestClient_AnonLogin(t *testing.T) {
	t.Parallel()
	f := newFakeQbit("", "")
	defer f.close()
	c, err := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("anon login should succeed without round-trip, got %v", err)
	}
	if f.loginCalls.Load() != 0 {
		t.Fatalf("anon login should not call upstream, got %d calls", f.loginCalls.Load())
	}
}

func TestClient_BasicLoginSucceeds(t *testing.T) {
	t.Parallel()
	f := newFakeQbit("admin", "secret")
	defer f.close()
	c, err := NewClient(Config{URL: f.srv.URL, Username: "admin", Password: "secret", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("login: %v", err)
	}
	if f.loginCalls.Load() == 0 {
		t.Fatalf("expected at least one login call")
	}
}

func TestClient_LoginBadCredentialsMapsAuth(t *testing.T) {
	t.Parallel()
	f := newFakeQbit("admin", "secret")
	defer f.close()
	c, _ := NewClient(Config{URL: f.srv.URL, Username: "admin", Password: "WRONG", Timeout: 2 * time.Second})
	err := c.Login(context.Background())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, sharedErrors.ErrInstanceUnauthorized) {
		t.Fatalf("want ErrInstanceUnauthorized in chain, got %v", err)
	}
}

func TestClient_LoginIPBannedMapsAuth(t *testing.T) {
	t.Parallel()
	f := newFakeQbit("admin", "secret")
	defer f.close()
	f.ipBanned.Store(true)
	c, _ := NewClient(Config{URL: f.srv.URL, Username: "admin", Password: "secret", Timeout: 2 * time.Second})
	err := c.Login(context.Background())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, sharedErrors.ErrInstanceUnauthorized) {
		t.Fatalf("want ErrInstanceUnauthorized in chain, got %v", err)
	}
}

func TestClient_LoginNetworkFailureMapsNetwork(t *testing.T) {
	t.Parallel()
	// Point at a server that immediately closes — upstream wraps the
	// transport error which we then join with ErrInstanceNetwork.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()
	c, _ := NewClient(Config{URL: srv.URL, Username: "admin", Password: "secret", Timeout: 2 * time.Second})
	err := c.Login(context.Background())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, sharedErrors.ErrInstanceNetwork) {
		t.Fatalf("want ErrInstanceNetwork in chain, got %v", err)
	}
}

func TestClient_ListTorrents_Filtered(t *testing.T) {
	t.Parallel()
	f := newFakeQbit("", "")
	defer f.close()
	f.torrents = []map[string]any{
		{"hash": "AAA", "name": "S01", "category": "sonarr", "state": "uploading", "added_on": int64(1700000000)},
		{"hash": "BBB", "name": "Other", "category": "radarr", "state": "uploading", "added_on": int64(1700000100)},
	}
	c, _ := NewClient(Config{URL: f.srv.URL, Category: "sonarr", Timeout: 2 * time.Second})
	got, err := c.ListTorrents(context.Background())
	if err != nil {
		t.Fatalf("ListTorrents: %v", err)
	}
	if len(got) != 1 || got[0].Hash != "AAA" || got[0].Category != "sonarr" {
		t.Fatalf("unexpected result: %+v", got)
	}
	if got[0].AddedOn.IsZero() {
		t.Fatal("AddedOn should be set")
	}
}

func TestClient_ListTorrents_AllCategories(t *testing.T) {
	t.Parallel()
	f := newFakeQbit("", "")
	defer f.close()
	f.torrents = []map[string]any{
		{"hash": "AAA", "name": "S01", "category": "sonarr", "state": "uploading", "added_on": int64(1700000000)},
		{"hash": "BBB", "name": "Other", "category": "radarr", "state": "uploading", "added_on": int64(1700000100)},
	}
	c, _ := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	got, err := c.ListTorrents(context.Background())
	if err != nil {
		t.Fatalf("ListTorrents: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
}

func TestClient_GetTrackers_OK(t *testing.T) {
	t.Parallel()
	f := newFakeQbit("", "")
	defer f.close()
	f.trackersByHash["AAA"] = []map[string]any{
		{"url": "http://tr1/announce", "status": 2, "msg": "Working"},
		{"url": "http://tr2/announce", "status": 4, "msg": "Torrent not found"},
	}
	c, _ := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	got, err := c.GetTrackers(context.Background(), "AAA")
	if err != nil {
		t.Fatalf("GetTrackers: %v", err)
	}
	if len(got) != 2 || got[0].Status != 2 || got[1].Msg != "Torrent not found" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestClient_GetTrackers_NotFound(t *testing.T) {
	t.Parallel()
	f := newFakeQbit("", "")
	defer f.close()
	c, _ := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	_, err := c.GetTrackers(context.Background(), "missing")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, ErrTorrentNotFound) {
		t.Fatalf("want ErrTorrentNotFound, got %v", err)
	}
}

func TestClient_GetTrackers_EmptyHash(t *testing.T) {
	t.Parallel()
	f := newFakeQbit("", "")
	defer f.close()
	c, _ := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	_, err := c.GetTrackers(context.Background(), "")
	if !errors.Is(err, ErrTorrentNotFound) {
		t.Fatalf("want ErrTorrentNotFound, got %v", err)
	}
}

func TestClient_Close_NoOp(t *testing.T) {
	t.Parallel()
	f := newFakeQbit("", "")
	defer f.close()
	c, _ := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Login(context.Background()); err == nil {
		t.Fatal("Login after Close should fail")
	}
	if _, err := c.ListTorrents(context.Background()); err == nil {
		t.Fatal("ListTorrents after Close should fail")
	}
	if _, err := c.GetTrackers(context.Background(), "x"); err == nil {
		t.Fatal("GetTrackers after Close should fail")
	}
}

// dumpQbitMetrics writes the global VictoriaMetrics set to a string.
// Used by the qbit metrics integration tests to verify the
// MetricsTransport wrap is wired. Lives here (not in httpx) because
// the test exercises the wrap end-to-end through a real *qbt.Client.
func dumpQbitMetrics() string {
	buf := &bytes.Buffer{}
	metrics.WritePrometheus(buf, true)
	return buf.String()
}

// TestClient_HTTPMetricsTransport_Wired covers Story 478 (B-31):
// every outbound qBit Web API call must increment
// seasonfill_external_http_requests_total{client="qbit",...}. The test
// drives two real wire calls through the wrapper (Login on
// /api/v2/auth/login, Ping on /api/v2/app/version) against an
// httptest.Server and asserts the corresponding counter labels.
//
// We pick login + version specifically because they have stable
// 200-OK paths that don't require any pre-seeded torrent state,
// keeping the test minimal and deterministic.
func TestClient_HTTPMetricsTransport_Wired(t *testing.T) {
	// NOT t.Parallel — this test reads the global VictoriaMetrics
	// registry which is shared process-wide. A parallel run would let
	// other tests' counters bleed into our assertions. The wrap itself
	// is goroutine-safe; the assertion is what needs serialisation.
	f := newFakeQbit("admin", "secret")
	defer f.close()

	// /api/v2/app/version is not in fakeQbit's default mux; install it
	// so Ping has a 200 path to hit.
	f.srv.Config.Handler.(*http.ServeMux).HandleFunc(
		"/api/v2/app/version",
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("v4.6.0"))
		},
	)

	c, err := NewClient(Config{
		URL:      f.srv.URL,
		Username: "admin",
		Password: "secret",
		Timeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	body := dumpQbitMetrics()

	// Counter assertions — the metric line must carry client="qbit"
	// AND the expected endpoint label AND status="200".
	wantSubstrs := []string{
		`seasonfill_external_http_requests_total{client="qbit",endpoint="auth_login",method="POST",status="200"}`,
		`seasonfill_external_http_requests_total{client="qbit",endpoint="app_version",method="GET",status="200"}`,
		// Histograms expose _count with the full label set — assert
		// on _count rather than _sum/_bucket because _count is the
		// stable shape across VictoriaMetrics versions.
		`seasonfill_external_http_request_duration_seconds_count{client="qbit",endpoint="auth_login",method="POST",status="200"}`,
		`seasonfill_external_http_request_duration_seconds_count{client="qbit",endpoint="app_version",method="GET",status="200"}`,
	}
	for _, want := range wantSubstrs {
		if !strings.Contains(body, want) {
			t.Errorf("metrics dump missing expected line:\n  want substr: %s\n  full dump:\n%s", want, body)
		}
	}

	// In-flight gauge must exist for client="qbit" — even if the
	// current value is 0 by the time we sample (Inc/Dec around the
	// round-trip already balanced). The metric line shape is
	// `seasonfill_external_http_requests_in_flight{client="qbit"} N`.
	const inFlightPrefix = `seasonfill_external_http_requests_in_flight{client="qbit"}`
	if !strings.Contains(body, inFlightPrefix) {
		t.Errorf("metrics dump missing in-flight gauge for client=qbit:\n%s", body)
	}
}

// TestClient_HTTPMetricsTransport_UnknownEndpoint_BucketsSafely covers
// the cardinality-bound contract: a qBit V2 endpoint not in the
// QbitEndpointFor table must bucket as endpoint="other", never as the
// raw path. Prevents a future library upgrade from silently exploding
// the label space.
//
// We drive this by installing a custom V2 handler at
// /api/v2/torrents/exportTorrent (a real qBit endpoint that is NOT
// wrapped by seasonfill today and therefore intentionally absent from
// QbitEndpointFor). The wrapper's wire surface doesn't expose this
// endpoint, so we hit it directly through the library's underlying
// http.Client to keep the test focused on the mapper contract.
func TestClient_HTTPMetricsTransport_UnknownEndpoint_BucketsSafely(t *testing.T) {
	f := newFakeQbit("", "")
	defer f.close()

	called := atomic.Int32{}
	f.srv.Config.Handler.(*http.ServeMux).HandleFunc(
		"/api/v2/torrents/exportTorrent",
		func(w http.ResponseWriter, _ *http.Request) {
			called.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		},
	)

	c, err := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = c.Close() }()

	// Reach into the unexported field via the type assertion shortcut
	// used elsewhere in the package — we're in the same package, so
	// c.(*client).inner is accessible without reflection.
	impl, ok := c.(*client)
	if !ok {
		t.Fatalf("Client is not *client (test wiring assumption broken)")
	}
	httpClient := impl.inner.GetHTTPClient()
	if httpClient == nil {
		t.Fatalf("library http.Client is nil — metrics wrap precondition broken")
	}

	req, _ := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		f.srv.URL+"/api/v2/torrents/exportTorrent?hash=ABC",
		nil,
	)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if called.Load() != 1 {
		t.Fatalf("expected handler hit, got %d", called.Load())
	}

	body := dumpQbitMetrics()
	const wantOther = `seasonfill_external_http_requests_total{client="qbit",endpoint="other",method="GET",status="200"}`
	if !strings.Contains(body, wantOther) {
		t.Errorf("expected unmapped V2 endpoint to bucket as 'other':\n  want substr: %s\n  full dump:\n%s", wantOther, body)
	}
	// Cardinality guard: make sure the raw path didn't leak through.
	if strings.Contains(body, `endpoint="exportTorrent"`) || strings.Contains(body, `endpoint="/api/v2/torrents/exportTorrent"`) {
		t.Errorf("raw path leaked into endpoint label — cardinality bound broken:\n%s", body)
	}
}
