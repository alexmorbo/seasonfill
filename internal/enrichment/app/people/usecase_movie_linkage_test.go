package people

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	dompeople "github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// fakeMoviesByTMDB implements MovieCanonByTMDB. Resolves the movies canon row
// by TMDB id; an unknown id returns the joined MovieNotFoundError+ErrNotFound
// the production MovieRepository.GetByTMDBID surfaces on a miss. errs[id]
// overrides with an arbitrary error (non-NotFound path).
type fakeMoviesByTMDB struct {
	rows map[int]movie.Canon
	errs map[int]error
}

func (f *fakeMoviesByTMDB) GetByTMDBID(_ context.Context, tmdbID domain.TMDBID) (movie.Canon, error) {
	if err, ok := f.errs[int(tmdbID)]; ok {
		return movie.Canon{}, err
	}
	c, ok := f.rows[int(tmdbID)]
	if !ok {
		return movie.Canon{}, errors.Join(&sharedErrors.MovieNotFoundError{}, ports.ErrNotFound)
	}
	return c, nil
}

// TestClassifyCredit_MovieCanonLinkage covers F-20: a movie person_credit whose
// tmdb_id resolves to a movies canon row is classified CategoryCanon; every
// other movie shape (no canon row, nil dep, non-NotFound error) falls through
// to CategoryTMDB with no orphan. A tv credit keeps the unchanged series path.
func TestClassifyCredit_MovieCanonLinkage(t *testing.T) {
	t.Parallel()

	movieCredit := dompeople.PersonCredit{MediaType: "movie", TMDBMediaID: 438631}
	tvCredit := dompeople.PersonCredit{MediaType: "tv", TMDBMediaID: 1399}

	tests := []struct {
		name         string
		credit       dompeople.PersonCredit
		movies       MovieCanonByTMDB
		series       SeriesByTMDBLookup
		cache        SeriesCacheLookup
		wantCategory CreditCategory
		wantWarn     bool
	}{
		{
			name:         "movie credit with matching canon row → CategoryCanon",
			credit:       movieCredit,
			movies:       &fakeMoviesByTMDB{rows: map[int]movie.Canon{438631: {ID: 5, Title: "Dune"}}},
			wantCategory: CategoryCanon,
		},
		{
			name:         "movie credit without canon row → CategoryTMDB (no orphan)",
			credit:       movieCredit,
			movies:       &fakeMoviesByTMDB{rows: map[int]movie.Canon{}},
			wantCategory: CategoryTMDB,
		},
		{
			name:         "movie credit with nil MoviesByTMDB → CategoryTMDB (nil-tolerant)",
			credit:       movieCredit,
			movies:       nil,
			wantCategory: CategoryTMDB,
		},
		{
			name:         "movie credit with non-NotFound lookup error → CategoryTMDB + WARN",
			credit:       movieCredit,
			movies:       &fakeMoviesByTMDB{errs: map[int]error{438631: errors.New("db exploded")}},
			wantCategory: CategoryTMDB,
			wantWarn:     true,
		},
		{
			name:         "tv credit → unchanged series path (regression guard)",
			credit:       tvCredit,
			series:       &fakeSeriesByTMDB{rows: map[int]series.Canon{1399: {ID: 9}}},
			cache:        &fakeSeriesCache{rows: map[domain.SeriesID][]series.CacheEntry{}},
			wantCategory: CategoryCanon,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			deps := Deps{
				MoviesByTMDB: tc.movies,
				SeriesByTMDB: tc.series,
				SeriesCache:  tc.cache,
				Logger:       slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})),
			}
			uc := NewUseCase(deps)

			cat, canon, instances := uc.classifyCredit(t.Context(), tc.credit)
			if cat != tc.wantCategory {
				t.Fatalf("category = %v, want %v", cat, tc.wantCategory)
			}
			// R-3 keeps the movie-canon credit's series.Canon zero (the raw
			// person_credits title/poster is retained; R-6 adds the card).
			if tc.credit.MediaType == "movie" && canon.ID != 0 {
				t.Fatalf("movie credit series.Canon must stay zero, got id=%d", canon.ID)
			}
			if tc.credit.MediaType == "movie" && instances != nil {
				t.Fatalf("movie credit must carry no library instances, got %d", len(instances))
			}
			gotWarn := strings.Contains(buf.String(), "person_classify_movie_canon_lookup_failed")
			if gotWarn != tc.wantWarn {
				t.Fatalf("warn logged = %v, want %v (log: %q)", gotWarn, tc.wantWarn, buf.String())
			}
		})
	}
}
