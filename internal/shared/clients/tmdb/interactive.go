package tmdb

import "context"

// interactiveCtxKey marks a ctx as belonging to an on-view / freshener
// (latency-sensitive) caller. Callers so marked draw from the FULL shared rate
// bucket; unmarked (batch / background) callers must additionally clear the
// cold gate so interactive keeps headroom under batch saturation. W110-5 (F-03).
type interactiveCtxKey struct{}

// WithInteractive tags ctx as interactive (on-view) priority. The ONLY setter
// is the series freshener's sync-dispatch path (cmd/server/adapters/
// series_freshener.go). Everything else — enrichment batch, discovery,
// SWR background refresh — stays unmarked and rides the batch cold gate.
func WithInteractive(ctx context.Context) context.Context {
	return context.WithValue(ctx, interactiveCtxKey{}, true)
}

// isInteractive reports whether ctx was tagged by WithInteractive. Bare /
// derived-but-not-reparented contexts inherit the tag; a fresh
// context.Background() does not.
func isInteractive(ctx context.Context) bool {
	v, _ := ctx.Value(interactiveCtxKey{}).(bool)
	return v
}

// Interactive-reserve fraction bounds. The fraction of the TMDB rps budget held
// exclusively for interactive callers. Authoritative copy; internal/config
// keeps a self-contained mirror for env parsing (no config→tmdb import).
const (
	// DefaultInteractiveReserveFrac reserves 25% of rps for on-view callers.
	// At defaultRPS=50 → ≥12.5 rps interactive floor under batch saturation.
	DefaultInteractiveReserveFrac = 0.25
	// MinInteractiveReserveFrac / MaxInteractiveReserveFrac clamp operator input.
	MinInteractiveReserveFrac = 0.05
	MaxInteractiveReserveFrac = 0.5
)

// ClampInteractiveReserveFrac maps a raw (env/config) fraction to a safe value:
// <=0 → default; below floor → floor; above ceil → ceil; else unchanged.
func ClampInteractiveReserveFrac(f float64) float64 {
	switch {
	case f <= 0:
		return DefaultInteractiveReserveFrac
	case f < MinInteractiveReserveFrac:
		return MinInteractiveReserveFrac
	case f > MaxInteractiveReserveFrac:
		return MaxInteractiveReserveFrac
	default:
		return f
	}
}
