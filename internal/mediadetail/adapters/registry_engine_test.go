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

func TestRegistry_ForReturnsRegisteredPlugins(t *testing.T) {
	reg := mdengapp.NewSectionRegistry()
	moviePlugin := NewMovieTextPlugin(fakeCanonReader{}, fakeGapReader{}, &countingRefresher{}, "en")
	seriesPlugin := NewSeriesTextPlugin(fakeOverview{}, &countingSeriesRefresher{})
	reg.Register(mdengdomain.MediaTypeMovie, moviePlugin)
	reg.Register(mdengdomain.MediaTypeSeries, seriesPlugin)

	movies := reg.For(mdengdomain.MediaTypeMovie)
	if len(movies) != 1 || movies[0].Section() != mdengdomain.SectionText {
		t.Fatalf("For(movie) = %d plugins, want 1×text", len(movies))
	}
	series := reg.For(mdengdomain.MediaTypeSeries)
	if len(series) != 1 || series[0].Section() != mdengdomain.SectionText {
		t.Fatalf("For(series) = %d plugins, want 1×text", len(series))
	}
}

// TestEngineDrivesMovieTextFreshenExactlyOnce is the F-03 no-double-fetch guard:
// one movie detail open on a stale movie triggers EXACTLY ONE HandleForced.
func TestEngineDrivesMovieTextFreshenExactlyOnce(t *testing.T) {
	ref := &countingRefresher{}
	// Stub canon → MovieProbe returns all-stale → the engine drives Refresh.
	stub := fakeCanonReader{canon: movie.Canon{ID: 9, Hydration: movie.HydrationStub}}
	p := NewMovieTextPlugin(stub, fakeGapReader{}, ref, "en")

	reg := mdengapp.NewSectionRegistry()
	reg.Register(mdengdomain.MediaTypeMovie, p)
	fr := mdengapp.NewFreshener(reg, 5*time.Second, time.Now, nil)

	id, err := mdengdomain.NewMediaID(mdengdomain.MediaTypeMovie, 9, domain.TMDBID(9))
	if err != nil {
		t.Fatalf("NewMediaID: %v", err)
	}
	res := fr.EnsureFresh(context.Background(), id, "ru")
	if !res.Refreshed {
		t.Fatalf("FreshenResult = %+v, want Refreshed", res)
	}
	if ref.calls != 1 {
		t.Fatalf("HandleForced calls = %d, want exactly 1 (no double-fetch)", ref.calls)
	}
}

// TestEngineFresh_NoRefreshWhenNotStale confirms a fresh movie triggers zero HandleForced.
func TestEngineFresh_NoRefreshWhenNotStale(t *testing.T) {
	ref := &countingRefresher{}
	p := NewMovieTextPlugin(fakeCanonReader{canon: fullCanon(9, nil)}, fakeGapReader{}, ref, "en")
	reg := mdengapp.NewSectionRegistry()
	reg.Register(mdengdomain.MediaTypeMovie, p)
	fr := mdengapp.NewFreshener(reg, 5*time.Second, time.Now, nil)
	id, _ := mdengdomain.NewMediaID(mdengdomain.MediaTypeMovie, 9, domain.TMDBID(9))
	res := fr.EnsureFresh(context.Background(), id, "en")
	if !res.Fresh || ref.calls != 0 {
		t.Fatalf("res=%+v calls=%d, want Fresh + 0 calls", res, ref.calls)
	}
}
