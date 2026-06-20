package httpx

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsTransport_RoundTrip_Success_200(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	endpoints := func(*http.Request) string { return "/test" }
	tr := NewMetricsTransport("testclient", endpoints, http.DefaultTransport)
	c := &http.Client{Transport: tr}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/anything", nil)
	resp, err := c.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body := dumpMetrics()
	assert.Contains(t, body, `seasonfill_external_http_requests_total{client="testclient",endpoint="/test",method="GET",status="200"}`)
	// VictoriaMetrics histograms are exposed via _bucket/_sum/_count
	// — assert on the _count line which always carries the full label set.
	assert.Contains(t, body, `seasonfill_external_http_request_duration_seconds_count{client="testclient",endpoint="/test",method="GET",status="200"}`)
}

func TestMetricsTransport_RoundTrip_429_Literal(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	tr := NewMetricsTransport("testclient2", func(*http.Request) string { return "/rl" }, nil)
	c := &http.Client{Transport: tr}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/rl", nil)
	resp, err := c.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	body := dumpMetrics()
	assert.Contains(t, body, `seasonfill_external_http_requests_total{client="testclient2",endpoint="/rl",method="GET",status="429"}`)
}

func TestMetricsTransport_RoundTrip_502_Literal(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	tr := NewMetricsTransport("testclient3", func(*http.Request) string { return "/5xx" }, nil)
	c := &http.Client{Transport: tr}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/anything", nil)
	resp, err := c.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	body := dumpMetrics()
	assert.Contains(t, body, `seasonfill_external_http_requests_total{client="testclient3",endpoint="/5xx",method="GET",status="502"}`)
}

func TestMetricsTransport_RoundTrip_504_Literal(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	t.Cleanup(srv.Close)

	tr := NewMetricsTransport("testclient3b", func(*http.Request) string { return "/timeout" }, nil)
	c := &http.Client{Transport: tr}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/anything", nil)
	resp, err := c.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	body := dumpMetrics()
	assert.Contains(t, body, `seasonfill_external_http_requests_total{client="testclient3b",endpoint="/timeout",method="GET",status="504"}`)
}

func TestMetricsTransport_RoundTrip_OffSet_Bucketed_As_Other(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)

	tr := NewMetricsTransport("testclient3c", func(*http.Request) string { return "/teapot" }, nil)
	c := &http.Client{Transport: tr}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/anything", nil)
	resp, err := c.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	body := dumpMetrics()
	assert.Contains(t, body, `seasonfill_external_http_requests_total{client="testclient3c",endpoint="/teapot",method="GET",status="other"}`)
}

func TestMetricsTransport_RoundTrip_NetworkError_Classified_As_Error(t *testing.T) {
	t.Parallel()
	tr := NewMetricsTransport("testclient4", func(*http.Request) string { return "/neterr" }, &erringTransport{})
	c := &http.Client{Transport: tr}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.invalid/x", nil)
	_, err := c.Do(req)
	require.Error(t, err)

	body := dumpMetrics()
	assert.Contains(t, body, `seasonfill_external_http_requests_total{client="testclient4",endpoint="/neterr",method="GET",status="error"}`)
}

func TestMetricsTransport_InFlight_UpAndDown(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	t.Cleanup(srv.Close)

	tr := NewMetricsTransport("testclient5", func(*http.Request) string { return "/inflight" }, nil)
	c := &http.Client{Transport: tr}

	done := make(chan struct{})
	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/", nil)
		resp, _ := c.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body := dumpMetrics()
		if strings.Contains(body, `seasonfill_external_http_requests_in_flight{client="testclient5"} 1`) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	body := dumpMetrics()
	require.Contains(t, body, `seasonfill_external_http_requests_in_flight{client="testclient5"} 1`)

	close(release)
	<-done

	// Wait for the deferred Dec() to land — the goroutine returning is
	// not a synchronisation barrier on the gauge value sample we read.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body = dumpMetrics()
		if strings.Contains(body, `seasonfill_external_http_requests_in_flight{client="testclient5"} 0`) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.Contains(t, body, `seasonfill_external_http_requests_in_flight{client="testclient5"} 0`)
}

func TestMetricsTransport_NilInner_FallsBackToDefault(t *testing.T) {
	t.Parallel()
	tr := NewMetricsTransport("nilinner", func(*http.Request) string { return "/" }, nil)
	require.NotNil(t, tr.inner)
}

func TestMetricsTransport_NilEndpointFunc_FallsBackToUnknown(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	tr := NewMetricsTransport("nilfn", nil, nil)
	c := &http.Client{Transport: tr}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/any", nil)
	resp, _ := c.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	body := dumpMetrics()
	assert.Contains(t, body, `seasonfill_external_http_requests_total{client="nilfn",endpoint="/unknown",method="GET",status="200"}`)
}

func TestNormalizeStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		code int
		err  error
		want string
	}{
		{"err non-nil → error", 0, errors.New("boom"), "error"},
		{"200 → 200", 200, nil, "200"},
		{"304 → 304", 304, nil, "304"},
		{"401 → 401", 401, nil, "401"},
		{"404 → 404", 404, nil, "404"},
		{"429 → 429", 429, nil, "429"},
		{"500 → 500", 500, nil, "500"},
		{"502 → 502", 502, nil, "502"},
		{"503 → 503", 503, nil, "503"},
		{"504 → 504", 504, nil, "504"},
		{"204 → other", 204, nil, "other"},
		{"301 → other", 301, nil, "other"},
		{"418 → other", 418, nil, "other"},
		{"599 → other", 599, nil, "other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var resp *http.Response
			if tc.err == nil {
				resp = &http.Response{StatusCode: tc.code}
			}
			got := normalizeStatus(resp, tc.err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNormalizeStatus_NilResp_NilErr_BucketsAsError(t *testing.T) {
	t.Parallel()
	got := normalizeStatus(nil, nil)
	assert.Equal(t, "error", got)
}

// erringTransport always fails — for the "error" classification path.
type erringTransport struct{}

func (e *erringTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("simulated network error")
}

// dumpMetrics writes the global VictoriaMetrics set to a string.
// Direct dep on metrics package avoids any cycle with internal/observability.
func dumpMetrics() string {
	buf := &bytes.Buffer{}
	metrics.WritePrometheus(buf, true)
	return buf.String()
}
