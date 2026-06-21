package tmdb

import (
	"errors"
	"fmt"
)

// APIError is the typed error every endpoint method returns when
// upstream surfaces a non-2xx response. Body is the raw response
// payload (typically a TMDB error JSON like
// `{"status_code":34,"status_message":"The resource you requested could not be found.","success":false}`).
// Callers switch on Status — `404` is terminal (not_found),
// `401`/`403` is the auth-failed signal the operator sees in the
// External Services UI, `5xx`/`429` get retried inside the client
// and only surface after exhaustion.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("tmdb: api error: status=%d body=%s", e.Status, e.Body)
}

// IsNotFound reports whether the error indicates the requested
// entity does not exist. C-2 records an enrichment_errors row with
// attempts=terminalAttempts (no retry) until a manual refresh clears it.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status == 404
	}
	return false
}
