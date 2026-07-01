package seriesdetail

// EnsureFreshMode controls A5 EnsureFreshScope driver dispatch shape.
// Sync blocks the caller until narrow methods complete (SyncTimeout).
// Async fires goroutines + returns immediately.
//
// Sync is the default for composer cold-path scenarios where downstream
// errgroup branches need the committed narrow-method tx to be visible
// (e.g. Composer.Get needs series_texts.ru-RU row written by
// RefreshSeriesText before the series_texts branch reads it).
//
// Async is used by warm-reload paths + Phase 4 ChangesSyncer where the
// caller doesn't need the fresh row inside the same request cycle.
type EnsureFreshMode int

const (
	// ModeSync — blocking. Waits for all dispatched narrow methods or
	// SyncTimeout. Partial success possible (some sections OK, others
	// timed out).
	ModeSync EnsureFreshMode = iota

	// ModeAsync — fire-and-forget. Goroutines dispatched with detached
	// ctx + longer budget (asyncFollowupTimeout). Returns immediately.
	// Failures logged, no error surfaced to caller.
	ModeAsync
)
