package webhook

import "time"

// EventType is the domain classification of a Sonarr webhook event after
// the infrastructure DTO has been parsed. Phase 4 consumes three event
// types; everything else maps to EventTypeUnsupported.
type EventType string

const (
	// EventTypeGrabbed — Sonarr's "Grab". Sonarr handed a release to
	// the download client. Phase 4 records but does not mutate state.
	EventTypeGrabbed EventType = "grabbed"

	// EventTypeImported — Sonarr's "Download" (v4) or "Import" (alias).
	// File finished importing. Triggers grabbed -> imported.
	EventTypeImported EventType = "imported"

	// EventTypeImportFailed — Sonarr's "ManualInteractionRequired" (v4)
	// or "DownloadFailure"/"ImportFailure" (v3 aliases). Triggers
	// grabbed -> import_failed AND a 48h guid-scope cooldown.
	EventTypeImportFailed EventType = "import_failed"

	// EventTypeUnsupported — catch-all for Test/Rename/Health/SeriesAdd/
	// SeriesDelete/EpisodeFileDelete/ApplicationUpdate/HealthRestored
	// and any future enum value. Handler returns 200 OK + INFO log.
	EventTypeUnsupported EventType = "unsupported"
)

// IsConsumed reports whether the event type triggers a state mutation
// in the 007b use case. Test, Rename, Health, etc. return false.
func (t EventType) IsConsumed() bool {
	switch t {
	case EventTypeGrabbed, EventTypeImported, EventTypeImportFailed:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether the event represents a final outcome for
// a grab_records row (imported / import_failed). Grab is not terminal
// — the row is already in "grabbed" status when the Grab event arrives.
func (t EventType) IsTerminal() bool {
	switch t {
	case EventTypeImported, EventTypeImportFailed:
		return true
	default:
		return false
	}
}

// Event is the domain projection of a Sonarr webhook payload. Plain Go
// types only — no JSON tags, no DB tags, no Sonarr knowledge.
type Event struct {
	Type EventType

	// InstanceName — seasonfill-known Sonarr that emitted this. Sourced
	// from the URL :instance_name path param (Q-8), NOT the payload's
	// instanceName field (operator-set in Sonarr UI, untrustworthy).
	InstanceName string

	// DownloadID — Sonarr's download-client hash. Primary match key
	// (R-3); stable across Grab/Download/ManualInteraction lifecycle.
	DownloadID string

	// ReleaseTitle — fallback match key when DownloadID is empty.
	ReleaseTitle string

	// Indexer — source indexer name. Populated on Grab/ImportFailure;
	// empty on Download (Sonarr drops the release block from imports).
	Indexer string

	// SeriesID + SeasonNumber — secondary match key. SeasonNumber is
	// Episodes[0].SeasonNumber; cross-season packs handled in 007b.
	SeriesID     int
	SeasonNumber int

	// Message — failure reason from DownloadStatusMessages
	// (ManualInteractionRequired only). Empty on success.
	Message string

	// OccurredAt — from payload's eventTimestamp; falls back to now.
	OccurredAt time.Time

	// RawEventType — Sonarr's original eventType string. Logging only.
	RawEventType string
}
