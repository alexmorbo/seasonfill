package grab

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// LinkSource records which path populated a download_links row.
type LinkSource string

const (
	LinkSourceWebhook          LinkSource = "webhook"
	LinkSourceArrPoll          LinkSource = "arr-poll"
	LinkSourceInstanceBackfill LinkSource = "instance-backfill"
)

// IsValid returns true when src matches one of the three canonical
// values the download_links_source_check constraint accepts.
func (s LinkSource) IsValid() bool {
	switch s {
	case LinkSourceWebhook, LinkSourceArrPoll, LinkSourceInstanceBackfill:
		return true
	default:
		return false
	}
}

// DownloadLink is the qbit-hash → (instance, series, episodes) bridge
// populated by Sonarr/Radarr `downloadId` capture. Phase 1 = webhook +
// arr-poll only. The instance-backfill job (PRD §5.4 Phase 4) lives in
// N-5 scope.
//
// The CHECK constraint download_links_type_id_check on the underlying
// table enforces (sonarr+external_series_id) XOR (radarr+external_movie_id).
// Phase 1 always emits Sonarr rows; ExternalMovieID stays nil.
// ExternalEpisodeIDs is a JSON-encoded []int64 that mirrors the
// series_extras helper pattern; an empty list serialises to "[]".
type DownloadLink struct {
	QbitHash           domain.QbitHash
	InstanceName       domain.InstanceName
	InstanceType       string
	ExternalSeriesID   *int64
	ExternalMovieID    *int64
	ExternalEpisodeIDs string
	GlobalSeriesID     *domain.SeriesID
	DiscoveredAt       time.Time
	Source             LinkSource
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
