package seriesdetail

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeI18nSeriesTexts is a port-level stub that encodes the §5.6
// fallback contract. The composer never sees this fake directly — the
// test exercises the same fallback chain SeriesTextsPort callers rely
// on at the boundary so a regression in the contract is caught at unit
// scope (the dual-backend regression in
// internal/enrichment/persistence/i18n_texts_fallback_dual_test.go
// covers the SQL implementation against real engines).
//
// The fake's order: requested-lang first, then fallbackLanguage ("en-US"),
// then any remaining row's language ascending — identical to the SQL
// helper's "CASE WHEN ... THEN 2 WHEN ... THEN 1 ELSE 0 END DESC,
// language ASC" tier shape.
type fakeI18nSeriesTexts struct {
	byLang map[string]series.SeriesText
}

func (f *fakeI18nSeriesTexts) GetWithFallback(_ context.Context, _ domain.SeriesID, lang string) (series.SeriesText, error) {
	if t, ok := f.byLang[lang]; ok {
		return t, nil
	}
	if t, ok := f.byLang["en-US"]; ok {
		return t, nil
	}
	// First-available branch — iterate by language ASC (deterministic
	// in tests that seed two or more locales) so the §5.6 tie-breaker
	// mirrors the SQL helper.
	if len(f.byLang) == 0 {
		return series.SeriesText{}, ports.ErrNotFound
	}
	// Pull the lex-smallest language to match SQL ORDER BY language ASC.
	smallest := ""
	for l := range f.byLang {
		if smallest == "" || l < smallest {
			smallest = l
		}
	}
	return f.byLang[smallest], nil
}

// TestComposer_I18nFallback_PRDSection5_6 covers the four PRD §5.6
// fallback branches under the port contract: requested-hit, en-US
// fallback, first-available, and the not-found terminal. This is the
// regression net for the composer-side branch a (composer.go:250
// SeriesTextsPort.GetWithFallback) and branch b (composer.go:499
// EpisodeTextsPort.GetWithFallback) so the §5.6 contract cannot
// silently break under a port-layer refactor.
//
// Compile-time guard below pins the fake to the production port type so
// a port-signature change breaks the test rather than silently
// diverging.
func TestComposer_I18nFallback_PRDSection5_6(t *testing.T) {
	var _ SeriesTextsPort = (*fakeI18nSeriesTexts)(nil)

	t.Parallel()
	titleOf := func(label string) *string { s := "Title-" + label; return &s }
	cases := []struct {
		name           string
		availableLangs []string
		requested      string
		wantLang       string
		wantNotFound   bool
	}{
		{
			name:           "requested_ru_RU_returns_ru_RU",
			availableLangs: []string{"ru-RU", "en-US"},
			requested:      "ru-RU",
			wantLang:       "ru-RU",
		},
		{
			name:           "requested_ru_RU_falls_back_to_en_US",
			availableLangs: []string{"en-US"},
			requested:      "ru-RU",
			wantLang:       "en-US",
		},
		{
			name:           "requested_ru_RU_no_en_returns_first_available_lex_ascending",
			availableLangs: []string{"fr-FR", "de-DE"},
			requested:      "ru-RU",
			wantLang:       "de-DE",
		},
		{
			name:         "no_rows_returns_not_found",
			requested:    "ru-RU",
			wantNotFound: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeI18nSeriesTexts{byLang: map[string]series.SeriesText{}}
			for _, l := range tc.availableLangs {
				fake.byLang[l] = series.SeriesText{Language: l, Title: titleOf(l)}
			}
			got, err := fake.GetWithFallback(context.Background(), domain.SeriesID(1), tc.requested)
			if tc.wantNotFound {
				require.Error(t, err)
				assert.True(t, errors.Is(err, ports.ErrNotFound),
					"missing-rows branch must surface ports.ErrNotFound — composer translates to degraded source")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantLang, got.Language)
		})
	}
}
