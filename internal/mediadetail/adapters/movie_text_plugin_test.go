package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	mdengdomain "github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeCanonReader struct {
	canon movie.Canon
	err   error
}

func (f fakeCanonReader) Get(_ context.Context, _ domain.MovieID) (movie.Canon, error) {
	return f.canon, f.err
}

type fakeGapReader struct {
	gap bool
	err error
}

func (f fakeGapReader) HasLocalizedTextGap(_ context.Context, _ domain.MovieID, _ string, _ time.Time) (bool, error) {
	return f.gap, f.err
}

type countingRefresher struct {
	calls int
	err   error
}

func (c *countingRefresher) HandleForced(_ context.Context, _ int64) error {
	c.calls++
	return c.err
}

func movieMediaID(t *testing.T, internal int64) mdengdomain.MediaID {
	t.Helper()
	id, err := mdengdomain.NewMediaID(mdengdomain.MediaTypeMovie, internal, domain.TMDBID(42))
	if err != nil {
		t.Fatalf("NewMediaID: %v", err)
	}
	return id
}

func fullCanon(id int64, textSynced *time.Time) movie.Canon {
	now := time.Now()
	if textSynced == nil {
		textSynced = &now
	}
	// A HydrationFull canon whose 5 section stamps are all recent → MovieProbe not stale.
	recent := time.Now()
	return movie.Canon{
		ID:                         domain.MovieID(id),
		Hydration:                  movie.HydrationFull,
		EnrichmentTextSyncedAt:     textSynced,
		EnrichmentCastSyncedAt:     &recent,
		EnrichmentRecsSyncedAt:     &recent,
		EnrichmentMediaSyncedAt:    &recent,
		EnrichmentKeywordsSyncedAt: &recent,
	}
}

func TestMovieTextPlugin_Coverage_NoOp(t *testing.T) {
	p := NewMovieTextPlugin(fakeCanonReader{}, fakeGapReader{}, &countingRefresher{}, "en")
	covered, total, err := p.Coverage(context.Background(), movieMediaID(t, 1), "ru")
	if err != nil || covered != 0 || total != 0 {
		t.Fatalf("Coverage no-op = (%d,%d,%v), want (0,0,nil)", covered, total, err)
	}
}

func TestMovieTextPlugin_Section(t *testing.T) {
	p := NewMovieTextPlugin(fakeCanonReader{}, fakeGapReader{}, &countingRefresher{}, "en")
	if got := p.Section(); got != mdengdomain.SectionText {
		t.Fatalf("Section = %q, want text", got.String())
	}
}

func TestMovieTextPlugin_Staleness(t *testing.T) {
	now := time.Now()
	base := "en"
	tests := []struct {
		name      string
		canon     movie.Canon
		canonErr  error
		gap       fakeGapReader
		lang      string
		wantStale bool
		wantErr   bool
	}{
		{
			name:      "stub canon → section stale",
			canon:     movie.Canon{ID: 7, Hydration: movie.HydrationStub},
			lang:      "ru",
			wantStale: true,
		},
		{
			name:      "full+fresh, base lang → not stale (gap arm skipped)",
			canon:     fullCanon(7, &now),
			lang:      base,
			wantStale: false,
		},
		{
			name:      "full+fresh, ru with gap hit → stale",
			canon:     fullCanon(7, &now),
			gap:       fakeGapReader{gap: true},
			lang:      "ru",
			wantStale: true,
		},
		{
			name:      "full+fresh, ru no gap → not stale",
			canon:     fullCanon(7, &now),
			gap:       fakeGapReader{gap: false},
			lang:      "ru",
			wantStale: false,
		},
		{
			name:     "canon read error → err (assess fails closed)",
			canonErr: errors.New("db down"),
			lang:     "ru",
			wantErr:  true,
		},
		{
			name:    "gap read error → err (assess fails closed)",
			canon:   fullCanon(7, &now),
			gap:     fakeGapReader{err: errors.New("gap read")},
			lang:    "ru",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewMovieTextPlugin(
				fakeCanonReader{canon: tc.canon, err: tc.canonErr},
				tc.gap,
				&countingRefresher{},
				base,
			)
			v, err := p.Staleness(context.Background(), movieMediaID(t, 7), tc.lang, now)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil (stale=%v)", v.Stale)
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

func TestMovieTextPlugin_Refresh_CallsHandleForcedOnce(t *testing.T) {
	ref := &countingRefresher{}
	p := NewMovieTextPlugin(fakeCanonReader{}, fakeGapReader{}, ref, "en")
	if err := p.Refresh(context.Background(), movieMediaID(t, 7), "ru"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if ref.calls != 1 {
		t.Fatalf("HandleForced calls = %d, want 1", ref.calls)
	}
}

func TestMovieTextPlugin_Refresh_PropagatesError(t *testing.T) {
	ref := &countingRefresher{err: errors.New("boom")}
	p := NewMovieTextPlugin(fakeCanonReader{}, fakeGapReader{}, ref, "en")
	if err := p.Refresh(context.Background(), movieMediaID(t, 7), "ru"); err == nil {
		t.Fatal("want error, got nil")
	}
}
