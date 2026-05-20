package sonarr

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
)

// StatusError carries the HTTP status returned by Sonarr alongside the body
// snippet for diagnostics. It is the canonical error type for non-2xx responses.
type StatusError struct {
	Endpoint string
	Status   int
	Body     string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("sonarr %s returned status=%d body=%s", e.Endpoint, e.Status, truncate(e.Body, 256))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// IsTransient reports whether a Sonarr error is retry-eligible. Covers:
//   - HTTP 5xx
//   - network/DNS/connect/refused
//   - timeouts (including context.DeadlineExceeded surfaced via url.Error)
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status >= 500 && se.Status <= 599
	}
	var nerr net.Error
	if errors.As(err, &nerr) {
		if nerr.Timeout() {
			return true
		}
	}
	var uerr *url.Error
	if errors.As(err, &uerr) {
		if uerr.Timeout() {
			return true
		}
		// connection refused / network unreachable / DNS errors surface as url.Error wrapping syscall errors.
		return true
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	return false
}

// Is4xx reports whether the error carries a 4xx HTTP status.
func Is4xx(err error) bool {
	if err == nil {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status >= 400 && se.Status < 500
	}
	return false
}

// Classifier is a struct-shaped adapter implementing application/grab.classifier.
type Classifier struct{}

func (Classifier) IsTransient(err error) bool { return IsTransient(err) }
func (Classifier) Is4xx(err error) bool       { return Is4xx(err) }
