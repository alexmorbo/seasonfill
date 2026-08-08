package qbit

// Health is the derived actionability bucket surfaced on the
// torrents-tab read DTO (ADR-0013 Q3′). Unlike StateGroup (8
// buckets, chip colour), Health answers a single question the
// operator cares about: does this torrent need attention?
//
// Exactly three values:
//   - HealthError   — qBit reported error / missingFiles; a hard
//     fault the operator must resolve.
//   - HealthStalled — the transfer is залипло: connected but making
//     no progress (stalledDL / stalledUP). Actionable but not broken.
//   - HealthOK      — everything else. Downloading, seeding, queued,
//     paused, checking are all non-alarming; so is `unknown`.
//
// A future Q3″ follow-up will add `unregistered` (tracker rejected
// the torrent) — that needs a signal StateGroup does not carry, so
// it is deliberately out of scope here.
//
// Degrade-closed contract: any StateGroup we have NOT explicitly
// classified as bad — including StateGroupUnknown, an empty value,
// or a future/bogus bucket string — maps to HealthOK. A state we
// have not yet classified is not, by itself, a known-bad signal;
// alarming on it would train operators to ignore the field.
type Health string

const (
	HealthOK      Health = "ok"
	HealthStalled Health = "stalled"
	HealthError   Health = "error"
)

// HealthFor projects an 8-bucket StateGroup onto the 3-value Health
// enum. Pure function, no I/O — the single source of truth for the
// state→health mapping. See the Health doc comment for the
// degrade-closed default rationale.
func HealthFor(sg StateGroup) Health {
	switch sg {
	case StateGroupError:
		return HealthError
	case StateGroupStalled:
		return HealthStalled
	default:
		// downloading, seeding, queued, paused, checking, unknown,
		// and any unrecognized/empty value → non-alarming default.
		return HealthOK
	}
}
