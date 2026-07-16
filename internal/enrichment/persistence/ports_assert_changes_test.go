package persistence

import (
	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
)

// Compile-time guarantees (W2-3): the persistence repos satisfy the poller ports.
// Break here — not in W2-4's ChangesPoller — if a signature drifts.
var (
	_ appenrich.ChangedSeriesMarker = (*SeriesRepository)(nil)
	_ appenrich.ChangesCursorStore  = (*TMDBChangesStateRepository)(nil)
)
