package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// movie_changes.go — GET /movie/changes firehose (Ф6-R-4a L3-2). Movie analog
// of changes.go (GetTVChangesPage); do NOT touch changes.go. Reuses the shared
// ChangedIDsPage app-facing shape, changesDateLayout, and pageOrOne (same
// package). The movie changes poller (wired over the generic ChangesPoller with
// movie deps) walks this the same way the /tv/changes poller walks GetTVChangesPage.

// GetMovieChangesPage fetches one page of the global movie changes firehose
// (GET /movie/changes). start/end are interpreted as UTC calendar dates (day
// granularity, ≤14d window); page is 1-based. The firehose reports only
// {id, adult} per row — the mapper drops adult and returns the tmdb_ids plus
// pagination cursors, identical to GetTVChangesPage.
func (c *Client) GetMovieChangesPage(ctx context.Context, start, end time.Time, page int) (ChangedIDsPage, error) {
	q := url.Values{}
	q.Set("start_date", start.UTC().Format(changesDateLayout))
	q.Set("end_date", end.UTC().Format(changesDateLayout))
	q.Set("page", strconv.Itoa(pageOrOne(page)))

	body, err := c.do(ctx, "/movie/changes", q)
	if err != nil {
		return ChangedIDsPage{}, fmt.Errorf("tmdb: GetMovieChangesPage(%s..%s p%d): %w",
			q.Get("start_date"), q.Get("end_date"), page, err)
	}
	var raw movieChangesResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return ChangedIDsPage{}, fmt.Errorf("tmdb: decode MovieChanges(%s..%s p%d): %w",
			q.Get("start_date"), q.Get("end_date"), page, err)
	}
	return raw.toChangedIDsPage(), nil
}

// movieChangesResponse is the raw JSON envelope of GET /movie/changes. Package-
// private: callers only ever see ChangedIDsPage via the mapper below. Structural
// mirror of tvChangesResponse.
type movieChangesResponse struct {
	Results      []movieChangesEntry `json:"results"`
	Page         int                 `json:"page"`
	TotalPages   int                 `json:"total_pages"`
	TotalResults int                 `json:"total_results"`
}

// movieChangesEntry is one firehose row. adult is present in the wire shape but
// intentionally unused past the mapper.
type movieChangesEntry struct {
	ID    int64 `json:"id"`
	Adult bool  `json:"adult"`
}

// toChangedIDsPage projects the raw envelope into the shared app-facing page,
// dropping adult and preserving firehose order (dedup is the poller's concern).
func (r movieChangesResponse) toChangedIDsPage() ChangedIDsPage {
	ids := make([]int64, 0, len(r.Results))
	for _, e := range r.Results {
		ids = append(ids, e.ID)
	}
	return ChangedIDsPage{
		IDs:        ids,
		Page:       r.Page,
		TotalPages: r.TotalPages,
	}
}
