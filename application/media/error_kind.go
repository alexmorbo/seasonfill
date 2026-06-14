package media

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
)

// ErrorKind is the structured-log label the downloader emits on every failure.
// Operator-grepable: `error_kind=http_5xx` distinguishes upstream errors from
// `error_kind=s3_write_error` (mediastore.Put failure) without parsing the
// wrapped string. Story 312.
type ErrorKind string

const (
	ErrorKindDNS            ErrorKind = "dns_error"
	ErrorKindTimeout        ErrorKind = "network_timeout"
	ErrorKindConnectRefused ErrorKind = "connect_refused"
	ErrorKindTLS            ErrorKind = "tls_error"
	ErrorKindHTTP4xx        ErrorKind = "http_4xx"
	ErrorKindHTTP5xx        ErrorKind = "http_5xx"
	ErrorKindBodyRead       ErrorKind = "body_read_error"
	ErrorKindS3Write        ErrorKind = "s3_write_error"
	ErrorKindDBWrite        ErrorKind = "db_update_error"
	ErrorKindRateWait       ErrorKind = "rate_wait_error"
	ErrorKindContextCancel  ErrorKind = "ctx_canceled"
	ErrorKindOther          ErrorKind = "other"
)

// String implements fmt.Stringer.
func (k ErrorKind) String() string { return string(k) }

// httpStatusError carries the status code so ClassifyFetchError can split
// 4xx vs 5xx without re-parsing the error string. The downloader's
// fetchOnce constructs this when it knows the upstream returned an HTTP
// status (vs a network-level failure with no status).
type httpStatusError struct {
	Status int
	URL    string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("upstream status %d for %s", e.Status, e.URL)
}

// newHTTPStatusError is the helper the downloader uses to mint a typed status
// error. Exported for tests.
func newHTTPStatusError(status int, url string) error {
	return &httpStatusError{Status: status, URL: url}
}

// HTTPStatus returns the embedded status, or 0 when err is not a status error.
func HTTPStatus(err error) int {
	if err == nil {
		return 0
	}
	var s *httpStatusError
	if errors.As(err, &s) {
		return s.Status
	}
	return 0
}

// ClassifyFetchError returns the ErrorKind that best describes err. The
// classifier inspects errors.As / errors.Is matches first, then falls back to
// substring matches on the wrapped string for transport-level errors that
// don't carry a sentinel. Story 312.
func ClassifyFetchError(err error) ErrorKind {
	if err == nil {
		return ErrorKindOther
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ErrorKindContextCancel
	}
	if status := HTTPStatus(err); status > 0 {
		if status >= 500 {
			return ErrorKindHTTP5xx
		}
		if status >= 400 {
			return ErrorKindHTTP4xx
		}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ErrorKindDNS
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Timeout() {
			return ErrorKindTimeout
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return ErrorKindTimeout
		}
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return ErrorKindConnectRefused
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "context canceled"), strings.Contains(s, "context deadline exceeded"):
		// "context canceled" / "context deadline exceeded" are the Go
		// std stringifications of the sentinel context errors. When they
		// appear in an unwrapped (string-only) error they still describe
		// a context-cancel outcome — preserve that semantic over the
		// generic network_timeout / rate_wait classifications.
		return ErrorKindContextCancel
	case strings.Contains(s, "tls"), strings.Contains(s, "x509"), strings.Contains(s, "certificate"):
		return ErrorKindTLS
	case strings.Contains(s, "no such host"), strings.Contains(s, "dns"):
		return ErrorKindDNS
	case strings.Contains(s, "connection refused"):
		return ErrorKindConnectRefused
	case strings.Contains(s, "timeout"), strings.Contains(s, "deadline exceeded"):
		return ErrorKindTimeout
	case strings.Contains(s, "read body"):
		return ErrorKindBodyRead
	case strings.Contains(s, "rate wait"):
		return ErrorKindRateWait
	}
	return ErrorKindOther
}
