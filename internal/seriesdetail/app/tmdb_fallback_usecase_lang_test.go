package seriesdetail_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// TestTMDBFallbackGetRecommendations_LangLocalisesTitles — same bug
// symptom on the TMDB-only branch. Ensures the fallback UC honours
// ?lang= the way the composer does.
func TestTMDBFallbackGetRecommendations_LangLocalisesTitles(t *testing.T) {
	t.Parallel()
	canonByID := map[domain.SeriesID]series.Canon{
		42: {ID: 42, Title: "Source", Hydration: series.HydrationFull},
		10: {ID: 10, Title: "ER", Hydration: series.HydrationFull},
	}
	seriesPort := &fakeMapSeriesReader{rows: canonByID}
	texts := &fakeFallbackTexts{
		batch: map[domain.SeriesID]series.SeriesText{
			10: {SeriesID: 10, Language: "ru-RU", Title: new("Скорая помощь")},
		},
	}

	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:          seriesPort,
		SeriesTexts:     texts,
		Recommendations: &fakeFallbackRecsPort{ids: []domain.SeriesID{10}},
		Logger:          discardLogger(),
	})
	require.NoError(t, err)

	out, err := uc.GetRecommendations(t.Context(), 42, "ru-RU", 20, 0)
	require.NoError(t, err)
	require.Equal(t, 1, len(out.Items))
	require.Equal(t, "Скорая помощь", out.Items[0].Series.Title)
}
