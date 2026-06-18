package webhookinstall

import (
	"context"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// AggregateItem is one row of the GET /api/v1/webhooks/status payload —
// the existing per-instance Status plus the instance name. Defined here
// so the HTTP handler can read it without re-exporting the internals
// of Status (kept untyped at the application boundary).
type AggregateItem struct {
	InstanceName   domain.InstanceName
	Installed      bool
	Healthy        bool
	NotificationID *int
	URL            *string
	Error          *string
}

// Aggregate fans out GetStatus across every instance name with bounded
// concurrency. Per-instance errors are swallowed into the row's Error
// field rather than failing the whole call (the caller sees a partial
// payload populated; one bad Sonarr does not blank the sidebar pill).
// Concurrency cap is 5 — same as the watchdog rollup handler.
//
// Result ordering matches the input names slice so the caller controls
// presentation order.
func Aggregate(ctx context.Context, r *Reconciler, names []string) ([]AggregateItem, error) {
	items := make([]AggregateItem, len(names))
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5)

	for i, name := range names {
		i, name := i, name
		g.Go(func() error {
			st, err := r.GetStatus(gctx, name)
			item := AggregateItem{
				InstanceName:   domain.InstanceName(name),
				Installed:      st.Installed,
				NotificationID: st.NotificationID,
				URL:            st.InstalledURL,
				Error:          st.LastError,
			}
			if err != nil {
				// GetStatus returned a refresh error — surface as the
				// item's Error so the UI flags it.
				msg := err.Error()
				item.Error = &msg
			}
			item.Healthy = item.Installed && item.Error == nil
			mu.Lock()
			items[i] = item
			mu.Unlock()
			return nil
		})
	}

	// Aggregate never returns an error from the errgroup — per-instance
	// failures degrade to populated Error fields. The signature keeps
	// the error slot in case future callers want to short-circuit on
	// context cancellation; that's the only way err != nil here.
	_ = g.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return items, nil
}
