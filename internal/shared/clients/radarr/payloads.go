package radarr

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Radarr /queue + /history surfaces consumed by the ADR-0023 B1.3 movie
// torrentsync reconciler. Mirror of sonarr/payloads.go's QueueAll +
// GrabHistoryPaged with movieId in place of seriesId/episodeId.

// Radarr MovieHistoryEventType values. The Servarr family agrees on
// 1 == grabbed and DIVERGES below it: Radarr's downloadFolderImported is 2
// while Sonarr's is 3 (Sonarr inserts seriesFolderImported at 2). Pinning
// both values here is the single point of truth — never inline the number.
const (
	// HistoryEventGrabbed is Radarr's "the indexer release was sent to the
	// download client BY Radarr" event. Membership in this event stream is
	// the ONLY provenance signal for radarr_search (B1.3 HARD semantics).
	HistoryEventGrabbed = 1
	// HistoryEventDownloadFolderImported is Radarr's "the download was
	// imported into the library" event. It carries downloadId -> movieId for
	// torrents Radarr never grabbed (manually added by the user), which is
	// the only way those ever reach torrent_movie_map.
	HistoryEventDownloadFolderImported = 2
)

// defaultHistoryPageSize mirrors Radarr's own /history default.
const defaultHistoryPageSize = 50

// queueDTO mirrors Radarr's GET /api/v3/queue response envelope. Identical
// shape to sonarr's queueDTO.
type queueDTO struct {
	Page          int              `json:"page"`
	PageSize      int              `json:"pageSize"`
	SortKey       string           `json:"sortKey"`
	SortDirection string           `json:"sortDirection"`
	TotalRecords  int              `json:"totalRecords"`
	Records       []queueRecordDTO `json:"records"`
}

// queueRecordDTO is one Radarr queue row.
//
// Size/SizeLeft are float64 and NOT int64: Radarr's QueueResource declares
// them as C# `decimal`, so a fractional value on the wire ("size": 1.5)
// would fail to unmarshal into an integer field and take the whole page
// down with it. The mapper narrows to int64.
type queueRecordDTO struct {
	ID                    int                        `json:"id"`
	MovieID               shareddomain.RadarrMovieID `json:"movieId,omitempty"`
	Title                 string                     `json:"title"`
	Status                string                     `json:"status,omitempty"`
	TrackedDownloadStatus string                     `json:"trackedDownloadStatus,omitempty"`
	TrackedDownloadState  string                     `json:"trackedDownloadState,omitempty"`
	DownloadID            string                     `json:"downloadId,omitempty"`
	DownloadClient        string                     `json:"downloadClient,omitempty"`
	Protocol              string                     `json:"protocol,omitempty"`
	Size                  float64                    `json:"size,omitempty"`
	SizeLeft              float64                    `json:"sizeleft,omitempty"`
}

// QueuePayload is the typed /api/v3/queue response. TotalRecords is the
// count of records WE returned (Radarr's own totalRecords is the
// pre-pagination global count and would disagree with len(Records)).
type QueuePayload struct {
	TotalRecords int
	Records      []QueueRecord
}

// QueueRecord is one queue entry — the movie twin of sonarr.QueueRecord.
// DownloadID is lower-cased at the client boundary: torrent_movie_map's
// natural key is a lower-cased hash, and every consumer would otherwise
// have to remember to normalise.
type QueueRecord struct {
	ID                    int
	MovieID               shareddomain.RadarrMovieID
	Title                 string
	Status                string
	TrackedDownloadStatus string
	TrackedDownloadState  string
	DownloadID            string
	DownloadClient        string
	Protocol              string
	Size                  int64 // bytes, 0 when unknown
	SizeLeft              int64 // bytes remaining, 0 when complete/unknown
}

// QueueAll calls GET /api/v3/queue without a movie filter, walking pages
// until the global queue is drained. The movie torrentsync reconciler
// (ADR-0023 B1.3 source 3) does ONE upstream fan-out per pass and matches
// against its in-memory unmapped-hash set locally; per-movie fan-out would
// multiply API load by the number of unmapped movies and starve the global
// rate limiter.
//
// includeUnknownMovieItems is pinned false: unknown items carry movieId=0
// and cannot produce a bridge row.
func (c *Client) QueueAll(ctx context.Context) (QueuePayload, error) {
	const pageSize = 1000
	const maxPages = 1000 // infinite-loop guard; Radarr never paginates 1M queue rows

	out := QueuePayload{Records: make([]QueueRecord, 0, pageSize)}
	fetched := 0
	for page := 1; page <= maxPages; page++ {
		q := url.Values{}
		q.Set("includeMovie", "false")
		q.Set("includeUnknownMovieItems", "false")
		q.Set("page", strconv.Itoa(page))
		q.Set("pageSize", strconv.Itoa(pageSize))
		var dto queueDTO
		if err := c.get(ctx, "/api/v3/queue", q, &dto); err != nil {
			return QueuePayload{}, fmt.Errorf("queue all: %w", err)
		}
		fetched += len(dto.Records)
		for _, r := range dto.Records {
			out.Records = append(out.Records, QueueRecord{
				ID:                    r.ID,
				MovieID:               r.MovieID,
				Title:                 r.Title,
				Status:                r.Status,
				TrackedDownloadStatus: r.TrackedDownloadStatus,
				TrackedDownloadState:  r.TrackedDownloadState,
				DownloadID:            strings.ToLower(r.DownloadID),
				DownloadClient:        r.DownloadClient,
				Protocol:              r.Protocol,
				Size:                  int64(r.Size),
				SizeLeft:              int64(r.SizeLeft),
			})
		}
		// Last page reached: a short/empty page (fewer than the effective
		// page size), or we walked past the reported total. Prefer the
		// server-reported PageSize when present (>0) so a server that clamps
		// our requested pageSize does not read a full clamped page as
		// "short" and silently revert to page-1-only behaviour. The
		// empty-page case (len==0 < effPageSize) also guards the loop.
		effPageSize := pageSize
		if dto.PageSize > 0 {
			effPageSize = dto.PageSize
		}
		if len(dto.Records) < effPageSize || page*effPageSize >= dto.TotalRecords {
			if fetched < dto.TotalRecords && c.logger != nil {
				c.logger.WarnContext(ctx, "radarr_queue_pagination_break_early",
					slog.String("instance", string(c.name)),
					slog.Int("fetched", fetched),
					slog.Int("total_records", dto.TotalRecords),
					slog.Int("last_page", page))
			}
			break
		}
	}
	out.TotalRecords = len(out.Records)
	return out, nil
}

// historyRecordDTO is one Radarr /history row. eventType arrives as the
// camelCase NAME ("grabbed"), not the numeric enum — the numeric form is
// only the query parameter. Same split as Sonarr.
type historyRecordDTO struct {
	ID          int                        `json:"id"`
	MovieID     shareddomain.RadarrMovieID `json:"movieId,omitempty"`
	EventType   string                     `json:"eventType"`
	DownloadID  string                     `json:"downloadId,omitempty"`
	SourceTitle string                     `json:"sourceTitle,omitempty"`
}

type historyPagedResponse struct {
	Page         int                `json:"page"`
	PageSize     int                `json:"pageSize"`
	TotalRecords int                `json:"totalRecords"`
	Records      []historyRecordDTO `json:"records"`
}

// HistoryPage is one page of paginated Radarr history.
//
// RawCount is len(records) AS RECEIVED, before the no-downloadId filter.
// End-of-data MUST be decided on RawCount, never on len(Records): a page
// full of usenet grabs (no downloadId) filters down to zero and would look
// like the end of the stream while more torrent grabs sit on the next page.
type HistoryPage struct {
	Page         int
	PageSize     int
	TotalRecords int
	RawCount     int
	Records      []HistoryRecord
}

// HistoryRecord is the per-record projection the movie reconciler reads:
// the downloadId -> movieId edge plus the event name for diagnostics.
// DownloadID is lower-cased.
type HistoryRecord struct {
	DownloadID string
	MovieID    shareddomain.RadarrMovieID
	EventType  string
}

// historyPaged is the shared /api/v3/history walker. page is 1-indexed
// (Radarr convention); sorted date-descending so page 1 is always the
// freshest events.
func (c *Client) historyPaged(ctx context.Context, eventType, page, pageSize int) (HistoryPage, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = defaultHistoryPageSize
	}
	q := url.Values{}
	q.Set("eventType", strconv.Itoa(eventType))
	q.Set("page", strconv.Itoa(page))
	q.Set("pageSize", strconv.Itoa(pageSize))
	q.Set("sortKey", "date")
	q.Set("sortDirection", "descending")
	var resp historyPagedResponse
	if err := c.get(ctx, "/api/v3/history", q, &resp); err != nil {
		return HistoryPage{}, fmt.Errorf("history page %d (eventType=%d): %w", page, eventType, err)
	}
	out := HistoryPage{
		Page:         resp.Page,
		PageSize:     resp.PageSize,
		TotalRecords: resp.TotalRecords,
		RawCount:     len(resp.Records),
		Records:      make([]HistoryRecord, 0, len(resp.Records)),
	}
	for _, r := range resp.Records {
		if r.DownloadID == "" {
			// Usenet grabs have no torrent hash; the reconciler only maps
			// torrents. Skip silently.
			continue
		}
		out.Records = append(out.Records, HistoryRecord{
			DownloadID: strings.ToLower(r.DownloadID),
			MovieID:    r.MovieID,
			EventType:  r.EventType,
		})
	}
	return out, nil
}

// GrabHistoryPaged returns one page of /api/v3/history?eventType=1
// (grabbed) for ALL movies. Membership in this stream is the provenance
// oracle for radarr_search.
//
// pageSize MUST be stable across calls in one walk — the walker keys on
// page numbers, not record offsets.
func (c *Client) GrabHistoryPaged(ctx context.Context, page, pageSize int) (HistoryPage, error) {
	return c.historyPaged(ctx, HistoryEventGrabbed, page, pageSize)
}

// ImportHistoryPaged returns one page of /api/v3/history?eventType=2
// (downloadFolderImported) for ALL movies. This is the ONLY surface that
// still carries downloadId -> movieId for a torrent the user added by hand
// and Radarr subsequently imported: it was never grabbed (absent from
// eventType=1) and Radarr drops it from /queue once imported.
func (c *Client) ImportHistoryPaged(ctx context.Context, page, pageSize int) (HistoryPage, error) {
	return c.historyPaged(ctx, HistoryEventDownloadFolderImported, page, pageSize)
}
