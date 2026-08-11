package arrcore

import "fmt"

// BodyMaxBytes is the upper bound on bytes captured from an arr non-2xx
// response body. 4096 matches application/errtext.MaxBytes so the
// operator-visible drawer text matches what the network layer actually saw.
// Anything past this is dropped at io.ReadAll time. Previously
// sonarr.SonarrBodyMaxBytes (story 092 / F-P2-4).
const BodyMaxBytes = 4096

// StatusError carries the HTTP status returned by the arr instance alongside
// the body snippet for diagnostics. It is the canonical error type for non-2xx
// responses. Ф6-R-2 moved it here from the sonarr package; sonarr.StatusError
// is now a type alias of this type so external errors.As / errors.Is chains are
// unchanged.
//
// The Body field holds at most BodyMaxBytes (4096) bytes — the network layer
// bounds the read with io.LimitReader so the field cannot blow up logs or DB
// rows. Error() emits the full body verbatim; persistence sites cap downstream
// via errtext.Clamp (story 092 / F-P2-4).
//
// NOTE: the "sonarr" literal in Error() is intentionally preserved for ZERO
// behavior change — errtext/clamp_test.go and grab/.../error_detail_test.go
// assert the exact string. R-3 must parameterize the prefix before wiring a
// Radarr client that surfaces this error to users.
type StatusError struct {
	Endpoint string
	Status   int
	Body     string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("sonarr %s returned status=%d body=%s", e.Endpoint, e.Status, e.Body)
}
