package seriesdetail_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// stubSeriesReader satisfies seriesdetail.SeriesPort (Get +
// GetByTMDBID). The fallback only uses Get; GetByTMDBID returns
// not-found to keep the surface complete.
type stubSeriesReader struct {
	canon series.Canon
	err   error
}

func (s *stubSeriesReader) Get(_ context.Context, _ domain.SeriesID) (series.Canon, error) {
	if s.err != nil {
		return series.Canon{}, s.err
	}
	return s.canon, nil
}

func (s *stubSeriesReader) GetByTMDBID(_ context.Context, _ domain.TMDBID) (series.Canon, error) {
	return series.Canon{}, ports.ErrNotFound
}

func TestTMDBFallbackUseCase_FullHydration(t *testing.T) {
	title := "Rick and Morty"
	canon := series.Canon{ID: 140, Title: title, Hydration: series.HydrationFull}
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{canon: canon},
	})
	require.NoError(t, err)
	detail, err := uc.GetCanonical(context.Background(), 140, "en-US")
	require.NoError(t, err)
	assert.Equal(t, title, detail.Canon.Title)
	assert.Empty(t, detail.Degraded, "full hydration has no tmdb_series degraded")
	assert.NotZero(t, detail.SyncedAt)
	assert.Equal(t, []domain.InstanceName{}, detail.InLibraryInstances)
	assert.Equal(t, domain.SeriesID(140), detail.SeriesID)
}

func TestTMDBFallbackUseCase_StubHydration_DegradesTMDBSeries(t *testing.T) {
	canon := series.Canon{ID: 140, Title: "Stub", Hydration: series.HydrationStub}
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{canon: canon},
	})
	require.NoError(t, err)
	detail, err := uc.GetCanonical(context.Background(), 140, "en-US")
	require.NoError(t, err)
	assert.Equal(t, []enrichment.Source{enrichment.SourceTMDBSeries}, detail.Degraded)
}

func TestTMDBFallbackUseCase_EmptyHydration_DegradesTMDBSeries(t *testing.T) {
	// Empty hydration treated as stub by the defensive default.
	canon := series.Canon{ID: 140, Title: "Empty"}
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{canon: canon},
	})
	detail, err := uc.GetCanonical(context.Background(), 140, "en-US")
	require.NoError(t, err)
	assert.Equal(t, []enrichment.Source{enrichment.SourceTMDBSeries}, detail.Degraded)
}

func TestTMDBFallbackUseCase_CanonNotFound(t *testing.T) {
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{err: ports.ErrNotFound},
	})
	_, err := uc.GetCanonical(context.Background(), 99999, "en-US")
	assert.ErrorIs(t, err, ports.ErrNotFound)
}

func TestTMDBFallbackUseCase_SeriesReaderError_Propagates(t *testing.T) {
	wantErr := errors.New("db boom")
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{err: wantErr},
	})
	_, err := uc.GetCanonical(context.Background(), 140, "en-US")
	assert.ErrorIs(t, err, wantErr)
}

func TestNewTMDBFallbackUseCase_NilSeriesReturnsError(t *testing.T) {
	_, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{})
	require.Error(t, err)
}

func TestTMDBFallbackUseCase_EmptyLang_DefaultsToEnUS(t *testing.T) {
	canon := series.Canon{ID: 140, Title: "T", Hydration: series.HydrationFull}
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{canon: canon},
	})
	detail, err := uc.GetCanonical(context.Background(), 140, "")
	require.NoError(t, err)
	assert.Equal(t, "en-US", detail.Lang)
}
