package scan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeSeriesTexts records InsertBaseLangIfAbsent calls and simulates a
// pre-existing row set so the only-if-absent contract can be asserted at
// the sync boundary. `existing` keyed by (series_id,language).
type fakeSeriesTexts struct {
	existing map[string]bool
	calls    []series.SeriesText
	writes   int
}

func (f *fakeSeriesTexts) key(sid domain.SeriesID, lang string) string {
	return string(rune(sid)) + "|" + lang
}

func (f *fakeSeriesTexts) InsertBaseLangIfAbsent(_ context.Context, t series.SeriesText) error {
	f.calls = append(f.calls, t)
	k := f.key(t.SeriesID, t.Language)
	if f.existing[k] {
		return nil // no-op, row present
	}
	if f.existing == nil {
		f.existing = map[string]bool{}
	}
	f.existing[k] = true
	f.writes++
	return nil
}

var _ SeriesTextsRepository = (*fakeSeriesTexts)(nil)

func TestSyncSeriesFromSonarr_BaseLangSeed(t *testing.T) {
	ctx := context.Background()

	// newBundle carries a stable TVDBID so the two-sync subtests resolve to
	// the SAME canonical series row (external-id keyed), which is what the
	// only-if-absent assertion depends on. TMDBID stays 0 (the base-lang
	// writer does not care about tmdb presence).
	newBundle := func(title string) SonarrPayloadBundle {
		return SonarrPayloadBundle{
			Series: sonarr.SeriesPayload{ID: 55, TVDBID: 550055, Title: title},
		}
	}

	t.Run("writes en-US when absent", func(t *testing.T) {
		deps, _ := newDerivationDeps(t)
		ftx := &fakeSeriesTexts{}
		deps.SeriesTexts = ftx
		_, err := SyncSeriesFromSonarr(ctx, deps, "sonarr-main", newBundle("Sonarr Title"))
		require.NoError(t, err)
		require.Len(t, ftx.calls, 1)
		assert.Equal(t, "en-US", ftx.calls[0].Language)
		require.NotNil(t, ftx.calls[0].Title)
		assert.Equal(t, "Sonarr Title", *ftx.calls[0].Title)
		assert.Equal(t, 1, ftx.writes)
	})

	t.Run("does not clobber existing TMDB row", func(t *testing.T) {
		deps, _ := newDerivationDeps(t)
		ftx := &fakeSeriesTexts{existing: map[string]bool{}}
		deps.SeriesTexts = ftx
		// First sync creates the row.
		sid, err := SyncSeriesFromSonarr(ctx, deps, "sonarr-main", newBundle("First"))
		require.NoError(t, err)
		_ = sid
		before := ftx.writes
		// Second sync (same series) must be a no-op write.
		_, err = SyncSeriesFromSonarr(ctx, deps, "sonarr-main", newBundle("Second"))
		require.NoError(t, err)
		assert.Equal(t, before, ftx.writes, "existing row must not be overwritten")
	})

	t.Run("nil SeriesTexts dep is a no-op (back-compat)", func(t *testing.T) {
		deps, _ := newDerivationDeps(t)
		deps.SeriesTexts = nil
		_, err := SyncSeriesFromSonarr(ctx, deps, "sonarr-main", newBundle("Whatever"))
		require.NoError(t, err)
	})

	t.Run("empty title → no write", func(t *testing.T) {
		deps, _ := newDerivationDeps(t)
		ftx := &fakeSeriesTexts{}
		deps.SeriesTexts = ftx
		_, err := SyncSeriesFromSonarr(ctx, deps, "sonarr-main", newBundle(""))
		// empty title fails the p.Title=="" guard — assert no series_texts call.
		require.NoError(t, err)
		assert.Empty(t, ftx.calls)
	})
}
