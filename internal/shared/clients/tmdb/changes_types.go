package tmdb

// ChangedIDsPage is the app-facing shape of one /tv/changes page (WAVE2 plan
// §4.2). The adult flag is dropped at the mapping boundary — the poller
// intersects by tmdb_id and we hold no adult TV, so it carries no signal.
// Page / TotalPages drive the caller's pagination walk.
type ChangedIDsPage struct {
	IDs        []int64 // tmdb_ids changed in the queried window (adult flag dropped)
	Page       int
	TotalPages int
}

// tvChangesResponse is the raw JSON envelope of GET /tv/changes. Package-private:
// callers only ever see ChangedIDsPage via the mapper below. The firehose list
// level carries NO change keys — those live on GET /tv/{id}/changes (a future
// Architecture C concern, plan §2.2).
type tvChangesResponse struct {
	Results      []tvChangesEntry `json:"results"`
	Page         int              `json:"page"`
	TotalPages   int              `json:"total_pages"`
	TotalResults int              `json:"total_results"`
}

// tvChangesEntry is one firehose row. adult is present in the wire shape but
// intentionally unused past the mapper.
type tvChangesEntry struct {
	ID    int64 `json:"id"`
	Adult bool  `json:"adult"`
}

// toChangedIDsPage projects the raw envelope into the app-facing page, dropping
// adult and preserving firehose order (dedup is the poller's concern, plan §6).
func (r tvChangesResponse) toChangedIDsPage() ChangedIDsPage {
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
