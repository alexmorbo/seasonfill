package sonarr

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIsTransient_StatusError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status int
		want   bool
	}{
		{500, true},
		{502, true},
		{503, true},
		{599, true},
		{408, true}, // H-3
		{429, true}, // H-3
		{400, false},
		{401, false},
		{403, false},
		{404, false},
		{200, false},
		{301, false},
	}
	for _, tt := range tests {
		err := &StatusError{Endpoint: "/x", Status: tt.status, Body: "body"}
		assert.Equal(t, tt.want, IsTransient(err), "status=%d", tt.status)
	}
}

func TestIs4xx_StatusError(t *testing.T) {
	t.Parallel()
	assert.True(t, Is4xx(&StatusError{Status: 400}))
	assert.True(t, Is4xx(&StatusError{Status: 401}))
	assert.True(t, Is4xx(&StatusError{Status: 403}))
	assert.True(t, Is4xx(&StatusError{Status: 404}))
	assert.True(t, Is4xx(&StatusError{Status: 408}))
	assert.True(t, Is4xx(&StatusError{Status: 429}))
	assert.False(t, Is4xx(&StatusError{Status: 500}))
	assert.False(t, Is4xx(&StatusError{Status: 200}))
	assert.False(t, Is4xx(nil))
}

func TestIsAuth_StatusError(t *testing.T) {
	t.Parallel()
	assert.True(t, IsAuth(&StatusError{Status: 401}))
	assert.True(t, IsAuth(&StatusError{Status: 403}))
	assert.False(t, IsAuth(&StatusError{Status: 400}))
	assert.False(t, IsAuth(&StatusError{Status: 404}))
	assert.False(t, IsAuth(&StatusError{Status: 408}))
	assert.False(t, IsAuth(&StatusError{Status: 429}))
	assert.False(t, IsAuth(&StatusError{Status: 500}))
	assert.False(t, IsAuth(nil))
}

func TestIsTransient_NilAndUnknown(t *testing.T) {
	t.Parallel()
	assert.False(t, IsTransient(nil))
	assert.False(t, IsTransient(errors.New("random")))
}

// TestStatusError_Error_PreservesFullBody locks in the F-P2-4 fix: the
// previous implementation trimmed Body to 256 chars + "..." which dropped
// most of a typical Sonarr stack trace before persistence. Body is already
// bounded by SonarrBodyMaxBytes (4096) at the io.LimitReader call in
// client.go, so Error() emits it verbatim.
func TestStatusError_Error_PreservesFullBody(t *testing.T) {
	t.Parallel()
	body := make([]byte, 1024)
	for i := range body {
		body[i] = 'A'
	}
	e := &StatusError{Endpoint: "/x", Status: 500, Body: string(body)}
	msg := e.Error()
	assert.NotContains(t, msg, "...")
	// Full 1024-byte body present (plus the "sonarr /x returned
	// status=500 body=" prefix — 30+ bytes).
	assert.GreaterOrEqual(t, len(msg), 1024)
	assert.Contains(t, msg, string(body))
}

// TestStatusError_Error_4KBBodyPreserved exercises the realistic upstream
// case: 4 KiB of NzbDrone stack trace bytes flow end-to-end without
// truncation. Pairs with errtext.Clamp at the persistence layer (which
// only kicks in past 4 KiB).
func TestStatusError_Error_4KBBodyPreserved(t *testing.T) {
	t.Parallel()
	body := make([]byte, 4096)
	for i := range body {
		body[i] = 'B'
	}
	e := &StatusError{Endpoint: "/api/v3/release", Status: 500, Body: string(body)}
	msg := e.Error()
	assert.NotContains(t, msg, "...")
	assert.Contains(t, msg, string(body))
}

func TestIsTransient_DNSError(t *testing.T) {
	t.Parallel()
	dns := &net.DNSError{Err: "no such host", Name: "fake.invalid.tld"}
	assert.True(t, IsTransient(dns))
}

func TestIsTransient_URLErrorTimeout(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(time.Second)
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Timeout: 50 * time.Millisecond}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	_, err := client.Do(req)
	if err == nil {
		t.Skip("expected timeout did not occur")
	}
	assert.True(t, IsTransient(err))

	var ue *url.Error
	assert.True(t, errors.As(err, &ue))
}

func TestIsTransient_UrlErrorContextCanceled(t *testing.T) {
	t.Parallel()
	uerr := &url.Error{Op: "Get", URL: "http://example.com", Err: context.Canceled}
	assert.False(t, IsTransient(uerr), "url.Error wrapping context.Canceled must not be transient")
}

func TestIsTransient_UrlErrorTimeoutStillTransient(t *testing.T) {
	t.Parallel()
	// context.DeadlineExceeded satisfies Timeout() == true so it stays transient
	uerr := &url.Error{Op: "Get", URL: "http://example.com", Err: context.DeadlineExceeded}
	assert.True(t, IsTransient(uerr), "url.Error wrapping DeadlineExceeded (timeout) must remain transient")
}

func TestClassifier_AdapterMethods(t *testing.T) {
	t.Parallel()
	c := Classifier{}
	assert.True(t, c.IsTransient(&StatusError{Status: 502}))
	assert.True(t, c.IsTransient(&StatusError{Status: 408}))
	assert.True(t, c.IsTransient(&StatusError{Status: 429}))
	assert.True(t, c.Is4xx(&StatusError{Status: 404}))
	assert.True(t, c.IsAuth(&StatusError{Status: 401}))
	assert.True(t, c.IsAuth(&StatusError{Status: 403}))
	assert.False(t, c.IsAuth(&StatusError{Status: 404}))
}

func TestIsReleaseGone(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "404", err: &StatusError{Endpoint: "/api/v3/release", Status: 404, Body: ""}, want: true},
		{name: "410", err: &StatusError{Endpoint: "/api/v3/release", Status: 410, Body: ""}, want: true},
		{name: "400", err: &StatusError{Status: 400}, want: false},
		{name: "401", err: &StatusError{Status: 401}, want: false},
		{name: "403", err: &StatusError{Status: 403}, want: false},
		{name: "500", err: &StatusError{Status: 500}, want: false},
		{name: "503", err: &StatusError{Status: 503}, want: false},
		{name: "wrapped 404", err: fmt.Errorf("call: %w", &StatusError{Status: 404}), want: true},
		{name: "non-status", err: errors.New("generic"), want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, IsReleaseGone(tc.err))
		})
	}
}

func TestIsReleaseAlreadyAdded(t *testing.T) {
	t.Parallel()
	conflictBody := "Failed to connect to qBittorrent at http://qbit.local:8080 " +
		"[409:Conflict] [POST] /api/v2/torrents/add"
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{
			name: "500 with full conflict body",
			err:  &StatusError{Endpoint: "/api/v3/release", Status: 500, Body: conflictBody},
			want: true,
		},
		{
			name: "500 with mixed-case markers still matches",
			err:  &StatusError{Endpoint: "/api/v3/release", Status: 500, Body: strings.ToUpper(conflictBody)},
			want: true,
		},
		{
			name: "500 wrapped via fmt.Errorf still classifies",
			err: fmt.Errorf("call: %w",
				&StatusError{Endpoint: "/api/v3/release", Status: 500, Body: conflictBody}),
			want: true,
		},
		{
			name: "500 with unrelated body (no qbit markers)",
			err:  &StatusError{Endpoint: "/api/v3/release", Status: 500, Body: "NullReferenceException at NzbDrone.Core..."},
			want: false,
		},
		{
			name: "500 with [409:Conflict] but no qBit marker",
			err:  &StatusError{Endpoint: "/api/v3/release", Status: 500, Body: "[409:Conflict] something else"},
			want: false,
		},
		{
			name: "503 with conflict body (wrong status)",
			err:  &StatusError{Status: 503, Body: conflictBody},
			want: false,
		},
		{name: "404 release gone", err: &StatusError{Status: 404, Body: ""}, want: false},
		{name: "200 (not an error in production but defensive)", err: &StatusError{Status: 200}, want: false},
		{name: "plain error", err: errors.New("dial sonarr: refused"), want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, IsReleaseAlreadyAdded(tc.err))
		})
	}
}
