package omdb

import "fmt"

// APIError is the catch-all typed error for non-2xx HTTP responses
// AND for unknown envelope error strings (Status=0). The worker
// switches on *APIError vs the three sentinel errors to decide
// the sync_log outcome.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("omdb: api error: %s", e.Body)
	}
	return fmt.Sprintf("omdb: api error: status=%d body=%s", e.Status, e.Body)
}
