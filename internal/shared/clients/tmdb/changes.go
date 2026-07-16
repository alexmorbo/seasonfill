package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// changesDateLayout is TMDB's date granularity for /tv/changes: start_date /
// end_date are CALENDAR DAYS (YYYY-MM-DD), not timestamps. The docs cap the
// window at 14 days and return 100 items per page (WAVE2 plan §2.1).
const changesDateLayout = "2006-01-02"

// GetTVChangesPage fetches one page of the global TV changes firehose
// (GET /tv/changes). start/end are interpreted as UTC calendar dates — the
// caller (the Wave 2 ChangesPoller) plans windows in UTC; we normalise to UTC
// here so a caller passing a non-UTC time still yields the correct calendar
// day. page is 1-based; TMDB defaults to 1 and returns total_pages for the
// walk. The firehose reports only {id, adult} per row — no change keys — so
// the mapper drops adult and hands back the tmdb_ids plus pagination cursors.
//
// The response is content/editorial-change only: rating aggregates
// (vote_average / vote_count / popularity) are NOT tracked as changes and never
// trigger a firehose entry (L-07 finding, confirmed against TMDB docs+forum).
func (c *Client) GetTVChangesPage(ctx context.Context, start, end time.Time, page int) (ChangedIDsPage, error) {
	q := url.Values{}
	q.Set("start_date", start.UTC().Format(changesDateLayout))
	q.Set("end_date", end.UTC().Format(changesDateLayout))
	q.Set("page", strconv.Itoa(pageOrOne(page)))

	body, err := c.do(ctx, "/tv/changes", q)
	if err != nil {
		return ChangedIDsPage{}, fmt.Errorf("tmdb: GetTVChangesPage(%s..%s p%d): %w",
			q.Get("start_date"), q.Get("end_date"), page, err)
	}
	var raw tvChangesResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return ChangedIDsPage{}, fmt.Errorf("tmdb: decode TVChanges(%s..%s p%d): %w",
			q.Get("start_date"), q.Get("end_date"), page, err)
	}
	return raw.toChangedIDsPage(), nil
}
