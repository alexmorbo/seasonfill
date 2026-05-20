package sonarr

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestStatusError_Error_Truncates(t *testing.T) {
	t.Parallel()
	body := make([]byte, 1024)
	for i := range body {
		body[i] = 'A'
	}
	e := &StatusError{Endpoint: "/x", Status: 500, Body: string(body)}
	msg := e.Error()
	assert.Contains(t, msg, "...")
	assert.Less(t, len(msg), 600)
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
