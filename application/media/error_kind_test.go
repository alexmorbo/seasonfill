package media

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyFetchError_HTTPStatus(t *testing.T) {
	t.Parallel()
	for status, want := range map[int]ErrorKind{
		400: ErrorKindHTTP4xx,
		401: ErrorKindHTTP4xx,
		404: ErrorKindHTTP4xx,
		418: ErrorKindHTTP4xx,
		500: ErrorKindHTTP5xx,
		502: ErrorKindHTTP5xx,
		503: ErrorKindHTTP5xx,
		504: ErrorKindHTTP5xx,
	} {
		err := newHTTPStatusError(status, "https://example/x")
		assert.Equal(t, want, ClassifyFetchError(err), "status=%d", status)
		assert.Equal(t, status, HTTPStatus(err), "status=%d", status)
	}
}

func TestClassifyFetchError_Wrapped(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("http do: %w", newHTTPStatusError(503, "https://example/x"))
	assert.Equal(t, ErrorKindHTTP5xx, ClassifyFetchError(err))
	assert.Equal(t, 503, HTTPStatus(err))
}

func TestClassifyFetchError_DNS(t *testing.T) {
	t.Parallel()
	dnsErr := &net.DNSError{Err: "no such host", Name: "image.tmdb.org"}
	wrapped := fmt.Errorf("http do: %w", dnsErr)
	assert.Equal(t, ErrorKindDNS, ClassifyFetchError(wrapped))
}

func TestClassifyFetchError_ConnectRefused(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("http do: %w", syscall.ECONNREFUSED)
	assert.Equal(t, ErrorKindConnectRefused, ClassifyFetchError(wrapped))
}

func TestClassifyFetchError_TLS_StringMatch(t *testing.T) {
	t.Parallel()
	err := errors.New("http do: tls: handshake failure")
	assert.Equal(t, ErrorKindTLS, ClassifyFetchError(err))
}

func TestClassifyFetchError_X509_StringMatch(t *testing.T) {
	t.Parallel()
	err := errors.New("http do: x509: certificate signed by unknown authority")
	assert.Equal(t, ErrorKindTLS, ClassifyFetchError(err))
}

func TestClassifyFetchError_ContextCanceled(t *testing.T) {
	t.Parallel()
	assert.Equal(t, ErrorKindContextCancel, ClassifyFetchError(context.Canceled))
	assert.Equal(t, ErrorKindContextCancel, ClassifyFetchError(context.DeadlineExceeded))
	wrapped := fmt.Errorf("rate wait: %w", context.Canceled)
	assert.Equal(t, ErrorKindContextCancel, ClassifyFetchError(wrapped))
}

func TestClassifyFetchError_BodyRead(t *testing.T) {
	t.Parallel()
	err := errors.New("read body: unexpected EOF")
	assert.Equal(t, ErrorKindBodyRead, ClassifyFetchError(err))
}

func TestClassifyFetchError_RateWait(t *testing.T) {
	t.Parallel()
	err := errors.New("rate wait: context deadline exceeded")
	// context-canceled match wins (more specific) — order matters in the
	// classifier. This test pins that order so a future refactor does not
	// silently demote ctx_canceled to rate_wait_error.
	assert.Equal(t, ErrorKindContextCancel, ClassifyFetchError(err))
}

func TestClassifyFetchError_Other(t *testing.T) {
	t.Parallel()
	assert.Equal(t, ErrorKindOther, ClassifyFetchError(errors.New("something weird")))
}

func TestHTTPStatus_NonStatusReturnsZero(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 0, HTTPStatus(errors.New("plain")))
	assert.Equal(t, 0, HTTPStatus(nil))
}
