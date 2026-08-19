package adapters

import (
	"context"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	mdengapp "github.com/alexmorbo/seasonfill/internal/mediadetail/app"
	mdengdomain "github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestMovieForceRefresherHolder_UnboundThenSet(t *testing.T) {
	h := NewMovieForceRefresherHolder()
	if err := h.HandleForced(context.Background(), 1); err == nil {
		t.Fatal("unbound holder should error")
	}
	ref := &countingRefresher{}
	h.Set(ref)
	if err := h.HandleForced(context.Background(), 1); err != nil {
		t.Fatalf("bound: %v", err)
	}
	if ref.calls != 1 {
		t.Fatalf("calls=%d want 1", ref.calls)
	}
}

func TestSeriesAllLangsRefresherHolder_ForceFalse(t *testing.T) {
	h := NewSeriesAllLangsRefresherHolder()
	if err := h.RefreshSeriesAllLangs(context.Background(), 1); err == nil {
		t.Fatal("unbound holder should error")
	}
	var gotForce bool
	var gotCalled bool
	h.Set(seriesForcedFunc(func(_ context.Context, _ domain.SeriesID, force bool) error {
		gotCalled, gotForce = true, force
		return nil
	}))
	if err := h.RefreshSeriesAllLangs(context.Background(), 2); err != nil {
		t.Fatalf("bound: %v", err)
	}
	if !gotCalled || gotForce {
		t.Fatalf("called=%v force=%v, want called with force=false", gotCalled, gotForce)
	}
}

type seriesForcedFunc func(context.Context, domain.SeriesID, bool) error

func (f seriesForcedFunc) RefreshSeriesAllLangs(ctx context.Context, id domain.SeriesID, force bool) error {
	return f(ctx, id, force)
}

func TestSeriesOverviewStalenessHolder_UnboundSafe(t *testing.T) {
	h := NewSeriesOverviewStalenessHolder()
	stale, reason, err := h.OverviewStale(context.Background(), 1, "ru")
	if err != nil || stale || reason != "unbound" {
		t.Fatalf("unbound = (%v,%q,%v), want (false,unbound,nil)", stale, reason, err)
	}
}

func TestMovieEngineFreshener_UnboundDegraded(t *testing.T) {
	a := NewMovieEngineFreshener()
	res := a.EnsureFresh(context.Background(), movie.Canon{ID: 5}, "ru")
	if !res.Degraded {
		t.Fatalf("unbound engine = %+v, want Degraded", res)
	}
}

func TestMovieEngineFreshener_ZeroCanonFresh(t *testing.T) {
	a := NewMovieEngineFreshener()
	res := a.EnsureFresh(context.Background(), movie.Canon{ID: 0}, "ru")
	if !res.Fresh {
		t.Fatalf("zero canon = %+v, want Fresh", res)
	}
}

func TestMovieEngineFreshener_DrivesEngine(t *testing.T) {
	ref := &countingRefresher{}
	p := NewMovieTextPlugin(fakeCanonReader{canon: movie.Canon{ID: 5, Hydration: movie.HydrationStub}}, fakeGapReader{}, ref, "en")
	reg := mdengapp.NewSectionRegistry()
	reg.Register(mdengdomain.MediaTypeMovie, p)
	fr := mdengapp.NewFreshener(reg, 5*time.Second, time.Now, nil)
	a := NewMovieEngineFreshener()
	a.Set(fr)

	tmdb := domain.TMDBID(5)
	res := a.EnsureFresh(context.Background(), movie.Canon{ID: 5, TMDBID: &tmdb}, "ru")
	if !res.Refreshed || ref.calls != 1 {
		t.Fatalf("res=%+v calls=%d, want Refreshed + 1 call", res, ref.calls)
	}
	_ = time.Now
}
