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
// NOTE: the arr prefix in Error() is parameterized via the Arr field (Ф6-R-3).
// A zero-value Arr ("") renders as "sonarr" for ZERO behavior change — the
// sonarr client never sets Arr, so errtext/clamp_test.go and
// grab/.../error_detail_test.go keep asserting the exact "sonarr …" string.
// The radarr client constructs arrcore with WithArrName("radarr"), which stamps
// Arr="radarr" onto every StatusError it surfaces.
type StatusError struct {
	Endpoint string
	Status   int
	Body     string
	Arr      string // "" ⇒ "sonarr" (zero-value compat: keeps errtext/grab test strings byte-identical)
}

func (e *StatusError) Error() string {
	arr := e.Arr
	if arr == "" {
		arr = "sonarr"
	}
	return fmt.Sprintf("%s %s returned status=%d body=%s", arr, e.Endpoint, e.Status, e.Body)
}
