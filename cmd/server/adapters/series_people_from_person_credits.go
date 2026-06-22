package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// SeriesCanonReader is the narrow Series.Get surface the
// SeriesPeopleFromPersonCredits adapter consumes. *enrichpersistence.
// SeriesRepository satisfies it directly.
//
// Kept as a local interface (not seriesdetail.SeriesPort) because the
// adapter needs only canon.TMDBID — no GetByTMDBID round-trip — and
// the narrower port keeps the adapter test free of the cast composer's
// fixture surface.
type SeriesCanonReader interface {
	Get(ctx context.Context, id domain.SeriesID) (canonView, error)
}

// canonView is the bridge struct the adapter consumes. The production
// adapter assembled in seriesdetail.go wraps *SeriesRepository via
// seriesCanonReaderAdapter; tests inject canned canonView rows
// directly.
//
// Title is carried through so a future read path can render the
// series label without a second canon Get; for the SeriesPeoplePort
// signature it is unused.
type canonView struct {
	TMDBID *domain.TMDBID
}

// seriesCanonReaderAdapter wraps *enrichpersistence.SeriesRepository to
// satisfy SeriesCanonReader. Kept local to this file so the
// SeriesPeopleFromPersonCredits constructor takes one concrete
// repository pointer (the same shape PersonCreditsAdapter consumes).
type seriesCanonReaderAdapter struct {
	inner *enrichpersistence.SeriesRepository
}

func (a seriesCanonReaderAdapter) Get(ctx context.Context, id domain.SeriesID) (canonView, error) {
	c, err := a.inner.Get(ctx, id)
	if err != nil {
		return canonView{}, err
	}
	return canonView{TMDBID: c.TMDBID}, nil
}

// SeriesPeopleFromPersonCredits adapts the polymorphic
// PersonCreditsRepository onto the seriesdetail SeriesPeoplePort. D-7
// replacement for the dropped series_people table: the canonical row
// now lives in person_credits(media_type=MediaTypeTV,
// tmdb_media_id=series.tmdb_id). The adapter resolves seriesID →
// canon.tmdb_id first (one canon row read), then ListByMedia + kind
// filter + adapt back to []people.SeriesCredit.
//
// MediaType discriminator stays as MediaTypeTV — identical to the
// value PersonWorker writes for /person/{id}/tv_credits rows — so a
// single ListByMedia(MediaTypeTV, tmdb_id) call covers both writers.
// The adapter does NOT touch media_type='movie' rows.
type SeriesPeopleFromPersonCredits struct {
	pc     *enrichpersistence.PersonCreditsRepository
	series SeriesCanonReader
}

// NewSeriesPeopleFromPersonCredits wires the adapter. Both deps
// required — nil dependencies surface as a panic on first call (this
// constructor is a wiring-time invariant, NOT a hot-path probe).
func NewSeriesPeopleFromPersonCredits(
	pc *enrichpersistence.PersonCreditsRepository,
	seriesRepo *enrichpersistence.SeriesRepository,
) *SeriesPeopleFromPersonCredits {
	return &SeriesPeopleFromPersonCredits{
		pc:     pc,
		series: seriesCanonReaderAdapter{inner: seriesRepo},
	}
}

// newSeriesPeopleFromPersonCreditsForTest is the test-only constructor
// that lets unit tests inject a canned SeriesCanonReader. The
// production constructor wraps *SeriesRepository to keep the wiring
// site free of the bridge interface.
func newSeriesPeopleFromPersonCreditsForTest(
	pc *enrichpersistence.PersonCreditsRepository,
	canonReader SeriesCanonReader,
) *SeriesPeopleFromPersonCredits {
	return &SeriesPeopleFromPersonCredits{pc: pc, series: canonReader}
}

// Assert interface satisfaction at compile time.
var _ seriesdetail.SeriesPeoplePort = (*SeriesPeopleFromPersonCredits)(nil)

// ListBySeries implements seriesdetail.SeriesPeoplePort. Resolves
// seriesID → canon.tmdb_id; returns an empty slice (NOT an error)
// when canon has no TMDB id (Sonarr-orphan series). Then ListByMedia +
// kind-filter + adapt.
//
// SeriesNotFoundError from the canon Get is returned untouched so the
// composer's typed-error middleware can still dispatch the right HTTP
// status. ports.ErrNotFound from the inner ListByMedia is treated as
// "no rows", NOT a 5xx (matches the composer-side empty-list
// posture).
func (a *SeriesPeopleFromPersonCredits) ListBySeries(
	ctx context.Context,
	seriesID domain.SeriesID,
	kind people.SeriesCreditKind,
) ([]people.SeriesCredit, error) {
	canon, err := a.series.Get(ctx, seriesID)
	if err != nil {
		// Preserve typed SeriesNotFoundError so the composer's
		// middleware dispatches series_not_found. Wrapping with %w
		// keeps errors.As reachable.
		var seriesNF *sharedErrors.SeriesNotFoundError
		if errors.As(err, &seriesNF) {
			return nil, err
		}
		return nil, fmt.Errorf("series_people adapter: canon lookup: %w", err)
	}
	if canon.TMDBID == nil {
		// Sonarr-orphan series — no TMDB cast available. NOT an error.
		return nil, nil
	}
	rows, err := a.pc.ListByMedia(ctx, tmdb.MediaTypeTV, int(*canon.TMDBID))
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("series_people adapter: list_by_media: %w", err)
	}
	out := make([]people.SeriesCredit, 0, len(rows))
	for _, r := range rows {
		if r.Kind != string(kind) {
			continue
		}
		out = append(out, people.SeriesCredit{
			ID:            r.ID,
			SeriesID:      seriesID,
			PersonID:      r.PersonID,
			Kind:          kind,
			TMDBCreditID:  r.TMDBCreditID,
			CharacterName: r.CharacterName,
			Department:    r.Department,
			Job:           r.Job,
			CreditOrder:   nil, // person_credits has no series-side billing index — read path orders by person_id ASC
			EpisodeCount:  r.EpisodeCount,
			CreatedAt:     r.CreatedAt,
			UpdatedAt:     r.UpdatedAt,
		})
	}
	return out, nil
}
