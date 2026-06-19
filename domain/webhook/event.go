package webhook

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

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

	// EventTypeSeriesAdd — Sonarr's "SeriesAdd" (v4+). Operator added
	// a series via UI/API; we upsert series_cache so the queue UI sees
	// metadata without waiting for the next scan (Phase 11 / D66).
	EventTypeSeriesAdd EventType = "series_add"

	// EventTypeSeriesDeleted — Sonarr's "SeriesDelete" (v4+). Operator
	// removed a series; soft-delete the cache row so the queue UI stops
	// showing it. Hard-delete is left to the SonarrInstance cascade so
	// grab_records FK references stay valid.
	EventTypeSeriesDeleted EventType = "series_deleted"

	// EventTypeUnsupported — catch-all for Test/Rename/Health/
	// EpisodeFileDelete/ApplicationUpdate/HealthRestored
	// and any future enum value. Handler returns 200 OK + INFO log.
	EventTypeUnsupported EventType = "unsupported"
)

// IsConsumed reports whether the event type triggers a state mutation
// in the 007b use case. Test, Rename, Health, etc. return false.
func (t EventType) IsConsumed() bool {
	switch t {
	case EventTypeGrabbed, EventTypeImported, EventTypeImportFailed,
		EventTypeSeriesAdd, EventTypeSeriesDeleted:
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
	InstanceName domain.InstanceName

	// DownloadID — Sonarr's download-client hash. Primary match key
	// (R-3); stable across Grab/Download/ManualInteraction lifecycle.
	DownloadID string

	// ReleaseTitle — fallback match key when DownloadID is empty.
	ReleaseTitle string

	// Indexer — source indexer name. Populated on Grab/ImportFailure;
	// empty on Download (Sonarr drops the release block from imports).
	Indexer string

	// ReleaseSize — Sonarr's `release.size` from the OnGrab payload.
	// 0 = absent (Sonarr omitted, or event type isn't Grab). int64
	// fits any realistic release size. handleGrabbed treats 0 as "no
	// update" — we never write 0 to the row.
	ReleaseSize int64

	// SeriesID + SeasonNumber — secondary match key. SeasonNumber is
	// Episodes[0].SeasonNumber; cross-season packs handled in 007b.
	SeriesID     domain.SonarrSeriesID
	SeasonNumber int

	// Series metadata — populated by the mapper when dto.Series is
	// present. SeriesAdd carries the rich set; SeriesDelete only
	// SeriesID + SeriesTitle. Other event types may carry SeriesID
	// only. All optional; zero-value = "Sonarr omitted".
	SeriesTitle     string
	SeriesTitleSlug string
	SeriesTVDBID    domain.TVDBID
	SeriesIMDBID    domain.IMDBID

	// Message — failure reason from DownloadStatusMessages
	// (ManualInteractionRequired only). Empty on success.
	Message string

	// OccurredAt — from payload's eventTimestamp; falls back to now.
	OccurredAt time.Time

	// RawEventType — Sonarr's original eventType string. Logging only.
	RawEventType string
}
