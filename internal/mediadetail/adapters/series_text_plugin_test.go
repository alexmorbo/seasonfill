package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	mdengdomain "github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeOverview struct {
	stale  bool
	reason string
	err    error
}

func (f fakeOverview) OverviewStale(_ context.Context, _ domain.SeriesID, _ string) (bool, string, error) {
	return f.stale, f.reason, f.err
}

type countingSeriesRefresher struct {
	calls  int
	lastID domain.SeriesID
	err    error
}

func (c *countingSeriesRefresher) RefreshSeriesAllLangs(_ context.Context, id domain.SeriesID) error {
	c.calls++
	c.lastID = id
	return c.err
}

func seriesMediaID(t *testing.T, internal int64) mdengdomain.MediaID {
	t.Helper()
	id, err := mdengdomain.NewMediaID(mdengdomain.MediaTypeSeries, internal, domain.TMDBID(0))
	if err != nil {
		t.Fatalf("NewMediaID: %v", err)
	}
	return id
}

func TestSeriesTextPlugin_Coverage_NoOp(t *testing.T) {
	p := NewSeriesTextPlugin(fakeOverview{}, &countingSeriesRefresher{})
	covered, total, err := p.Coverage(context.Background(), seriesMediaID(t, 1), "ru")
	if err != nil || covered != 0 || total != 0 {
		t.Fatalf("Coverage no-op = (%d,%d,%v), want (0,0,nil)", covered, total, err)
	}
}

func TestSeriesTextPlugin_Staleness(t *testing.T) {
	tests := []struct {
		name      string
		ov        fakeOverview
		wantStale bool
		wantErr   bool
	}{
		{name: "overview stale", ov: fakeOverview{stale: true, reason: "missing_lang"}, wantStale: true},
		{name: "overview fresh", ov: fakeOverview{stale: false, reason: "fresh"}, wantStale: false},
		{name: "probe error", ov: fakeOverview{err: errors.New("db")}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewSeriesTextPlugin(tc.ov, &countingSeriesRefresher{})
			v, err := p.Staleness(context.Background(), seriesMediaID(t, 3), "ru", time.Now())
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Stale != tc.wantStale {
				t.Fatalf("Stale = %v, want %v", v.Stale, tc.wantStale)
			}
			if v.Section != mdengdomain.SectionText {
				t.Fatalf("Section = %q, want text", v.Section.String())
			}
		})
	}
}

func TestSeriesTextPlugin_Refresh(t *testing.T) {
	ref := &countingSeriesRefresher{}
	p := NewSeriesTextPlugin(fakeOverview{}, ref)
	if err := p.Refresh(context.Background(), seriesMediaID(t, 55), "ru"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if ref.calls != 1 || ref.lastID != domain.SeriesID(55) {
		t.Fatalf("RefreshSeriesAllLangs calls=%d id=%d, want 1/55", ref.calls, ref.lastID)
	}
}
