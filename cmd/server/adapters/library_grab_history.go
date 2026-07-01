package adapters

import (
	"context"
	"fmt"

	grabdomain "github.com/alexmorbo/seasonfill/internal/grab/domain"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// grabLister is the narrow slice of GrabRepository the history adapter needs.
// *grabpersistence.GrabRepository satisfies it.
type grabLister interface {
	List(ctx context.Context, f ports.GrabFilter, p ports.Pagination) ([]grabdomain.Record, *ports.Cursor, error)
}

// GrabHistoryAdapter satisfies seriesdetail.LibraryGrabHistoryPort by projecting
// GrabRepository.List rows onto the composer-local GrabEvent. Story 577 / E-1-B2.
type GrabHistoryAdapter struct {
	repo grabLister
}

// NewGrabHistoryAdapter wires the adapter over the grab repository.
func NewGrabHistoryAdapter(repo grabLister) *GrabHistoryAdapter {
	return &GrabHistoryAdapter{repo: repo}
}

// RecentBySeries returns the newest-first grab_records for one
// (instance, sonarr_series_id), capped at limit. GrabRepository.List already
// orders created_at DESC and clamps the limit.
func (a *GrabHistoryAdapter) RecentBySeries(
	ctx context.Context,
	instanceName domain.InstanceName,
	sonarrSeriesID domain.SonarrSeriesID,
	limit int,
) ([]seriesdetail.GrabEvent, error) {
	if limit <= 0 {
		limit = 5
	}
	inst := instanceName
	sid := sonarrSeriesID
	filter := ports.GrabFilter{Instance: &inst, SeriesID: &sid}
	recs, _, err := a.repo.List(ctx, filter, ports.Pagination{Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("grab history recent by series: %w", err)
	}
	out := make([]seriesdetail.GrabEvent, 0, len(recs))
	for _, r := range recs {
		out = append(out, seriesdetail.GrabEvent{
			Status:       string(r.Status),
			SeasonNumber: r.SeasonNumber,
			ReleaseTitle: r.ReleaseTitle,
			Quality:      r.Quality,
			CreatedAt:    r.CreatedAt,
			UpdatedAt:    r.UpdatedAt,
		})
	}
	return out, nil
}
