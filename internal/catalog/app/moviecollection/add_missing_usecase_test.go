package moviecollection

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

func testLog() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

type fakeReader struct {
	parts []ports.MovieCollectionPart
	err   error
}

func (f *fakeReader) ListPartsWithMembership(_ context.Context, _ int, _ string) ([]ports.MovieCollectionPart, error) {
	return f.parts, f.err
}

type fakeAdder struct {
	added   []int
	failOn  int // tmdb id that errors
	already int // tmdb id returned as AlreadyAdded
}

func (f *fakeAdder) Add(_ context.Context, req AddMovieRequest) (AddMovieOutcome, error) {
	f.added = append(f.added, req.TMDBID)
	if req.TMDBID == f.failOn {
		return AddMovieOutcome{}, errors.New("radarr unreachable")
	}
	if req.TMDBID == f.already {
		return AddMovieOutcome{AlreadyAdded: true}, nil
	}
	return AddMovieOutcome{RadarrMovieID: req.TMDBID * 10}, nil
}

func TestAddAllMissing_SkipsInLibrary_AddsRest(t *testing.T) {
	reader := &fakeReader{parts: []ports.MovieCollectionPart{
		{MovieID: 1, TMDBID: 438631, Title: "Dune", InLibrary: true},
		{MovieID: 2, TMDBID: 693134, Title: "Dune: Part Two", InLibrary: false},
	}}
	adder := &fakeAdder{}
	uc := NewAddMissingUseCase(reader, adder, testLog())

	sum, err := uc.AddAllMissing(context.Background(), AddMissingRequest{
		InstanceName: "r1", TMDBCollectionID: 726871, MinimumAvailability: "released",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, sum.Requested)
	assert.Equal(t, 1, sum.Added)
	assert.Equal(t, 1, sum.AlreadyPresent)
	assert.Equal(t, 0, sum.Failed)
	assert.Equal(t, []int{693134}, adder.added, "only the missing part is added")
}

func TestAddAllMissing_PerPartFailureDoesNotAbort(t *testing.T) {
	reader := &fakeReader{parts: []ports.MovieCollectionPart{
		{TMDBID: 438631, Title: "Dune"},
		{TMDBID: 693134, Title: "Dune: Part Two"},
		{TMDBID: 700000, Title: "Dune: Part Three"},
	}}
	adder := &fakeAdder{failOn: 693134}
	uc := NewAddMissingUseCase(reader, adder, testLog())

	sum, err := uc.AddAllMissing(context.Background(), AddMissingRequest{InstanceName: "r1", TMDBCollectionID: 1})
	require.NoError(t, err)
	assert.Equal(t, 3, sum.Requested)
	assert.Equal(t, 2, sum.Added)
	assert.Equal(t, 1, sum.Failed)
	assert.Len(t, adder.added, 3, "the batch attempted all missing parts")
	// the failed part carries an Err.
	var failed PartOutcome
	for _, p := range sum.Parts {
		if p.TMDBID == 693134 {
			failed = p
		}
	}
	assert.NotEmpty(t, failed.Err)
}

func TestAddAllMissing_TmdblessPartSkippedAsFailed(t *testing.T) {
	reader := &fakeReader{parts: []ports.MovieCollectionPart{
		{MovieID: 5, TMDBID: 0, Title: "orphan"},
		{MovieID: 6, TMDBID: 438631, Title: "Dune"},
	}}
	adder := &fakeAdder{}
	uc := NewAddMissingUseCase(reader, adder, testLog())

	sum, err := uc.AddAllMissing(context.Background(), AddMissingRequest{InstanceName: "r1", TMDBCollectionID: 1})
	require.NoError(t, err)
	assert.Equal(t, 1, sum.Failed, "tmdb-less part cannot be added")
	assert.Equal(t, 1, sum.Added)
	assert.Equal(t, []int{438631}, adder.added, "orphan never passed to the adder")
}

func TestAddAllMissing_AlreadyAddedCountsAsAdded(t *testing.T) {
	reader := &fakeReader{parts: []ports.MovieCollectionPart{{TMDBID: 438631, Title: "Dune"}}}
	adder := &fakeAdder{already: 438631}
	uc := NewAddMissingUseCase(reader, adder, testLog())

	sum, err := uc.AddAllMissing(context.Background(), AddMissingRequest{InstanceName: "r1", TMDBCollectionID: 1})
	require.NoError(t, err)
	assert.Equal(t, 1, sum.Added)
	assert.True(t, sum.Parts[0].AlreadyAdded)
}

func TestAddAllMissing_ReaderError(t *testing.T) {
	uc := NewAddMissingUseCase(&fakeReader{err: errors.New("db down")}, &fakeAdder{}, testLog())
	_, err := uc.AddAllMissing(context.Background(), AddMissingRequest{InstanceName: "r1", TMDBCollectionID: 1})
	require.Error(t, err)
}

func TestAddAllMissing_ZeroCollectionID(t *testing.T) {
	uc := NewAddMissingUseCase(&fakeReader{}, &fakeAdder{}, testLog())
	_, err := uc.AddAllMissing(context.Background(), AddMissingRequest{InstanceName: "r1"})
	require.Error(t, err)
}

func TestNewAddMissingUseCase_NilDepsPanic(t *testing.T) {
	assert.Panics(t, func() { NewAddMissingUseCase(nil, &fakeAdder{}, testLog()) })
	assert.Panics(t, func() { NewAddMissingUseCase(&fakeReader{}, nil, testLog()) })
	assert.Panics(t, func() { NewAddMissingUseCase(&fakeReader{}, &fakeAdder{}, nil) })
}
