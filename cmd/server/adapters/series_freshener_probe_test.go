package adapters_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	catalogseries "github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// stubProbeSeries satisfies SeriesReader.
type stubProbeSeries struct {
	canon catalogseries.Canon
	err   error
}

func (s *stubProbeSeries) Get(_ context.Context, _ domain.SeriesID) (catalogseries.Canon, error) {
	if s.err != nil {
		return catalogseries.Canon{}, s.err
	}
	return s.canon, nil
}

// stubProbeTexts satisfies SeriesTextsReader.
type stubProbeTexts struct {
	row catalogseries.SeriesText
	err error
}

func (s *stubProbeTexts) GetWithFallback(_ context.Context, _ domain.SeriesID, _ string) (catalogseries.SeriesText, error) {
	if s.err != nil {
		return catalogseries.SeriesText{}, s.err
	}
	return s.row, nil
}

// stubCounter satisfies CountByID.
type stubCounter struct {
	n   int
	err error
}

func (s *stubCounter) CountBySeries(_ context.Context, _ domain.SeriesID) (int, error) {
	return s.n, s.err
}

func TestSeriesFreshenerProbe_RequiredFields(t *testing.T) {
	t.Parallel()
	_, err := adapters.NewSeriesFreshenerProbe(adapters.SeriesFreshenerProbeConfig{})
	require.Error(t, err)
}

func TestSeriesFreshenerProbe_IsStale(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	freshTime := now.Add(-time.Hour)
	staleTime := now.Add(-8 * 24 * time.Hour)

	type fakes struct {
		series      *stubProbeSeries
		texts       *stubProbeTexts
		seasonsCnt  *stubCounter
		peopleCount *stubCounter
	}

	makeProbe := func(f fakes) *adapters.SeriesFreshenerProbe {
		p, err := adapters.NewSeriesFreshenerProbe(adapters.SeriesFreshenerProbeConfig{
			Series:       f.series,
			SeriesTexts:  f.texts,
			SeasonsCount: f.seasonsCnt,
			PeopleCount:  f.peopleCount,
			CanonTTL:     7 * 24 * time.Hour,
		})
		require.NoError(t, err)
		return p
	}

	cases := []struct {
		name       string
		f          fakes
		lang       string
		wantStale  bool
		wantReason string
	}{
		{
			name: "no_canon when series Get returns error",
			f: fakes{
				series:      &stubProbeSeries{err: ports.ErrNotFound},
				texts:       &stubProbeTexts{},
				seasonsCnt:  &stubCounter{n: 5},
				peopleCount: &stubCounter{n: 5},
			},
			lang:       "en-US",
			wantStale:  true,
			wantReason: "no_canon",
		},
		{
			name: "stub when Hydration is not Full",
			f: fakes{
				series:      &stubProbeSeries{canon: catalogseries.Canon{ID: 1, Hydration: catalogseries.HydrationStub}},
				texts:       &stubProbeTexts{},
				seasonsCnt:  &stubCounter{n: 5},
				peopleCount: &stubCounter{n: 5},
			},
			lang:       "en-US",
			wantStale:  true,
			wantReason: "stub",
		},
		{
			name: "never when synced_at is nil",
			f: fakes{
				series:      &stubProbeSeries{canon: catalogseries.Canon{ID: 1, Hydration: catalogseries.HydrationFull}},
				texts:       &stubProbeTexts{},
				seasonsCnt:  &stubCounter{n: 5},
				peopleCount: &stubCounter{n: 5},
			},
			lang:       "en-US",
			wantStale:  true,
			wantReason: "never",
		},
		{
			name: "ttl when canon synced 8d ago",
			f: fakes{
				series: &stubProbeSeries{canon: catalogseries.Canon{
					ID: 1, Hydration: catalogseries.HydrationFull,
					EnrichmentTMDBSyncedAt: &staleTime,
				}},
				texts:       &stubProbeTexts{},
				seasonsCnt:  &stubCounter{n: 5},
				peopleCount: &stubCounter{n: 5},
			},
			lang:       "en-US",
			wantStale:  true,
			wantReason: "ttl",
		},
		{
			name: "empty_seasons when seasons count is zero",
			f: fakes{
				series: &stubProbeSeries{canon: catalogseries.Canon{
					ID: 1, Hydration: catalogseries.HydrationFull,
					EnrichmentTMDBSyncedAt: &freshTime,
				}},
				texts:       &stubProbeTexts{},
				seasonsCnt:  &stubCounter{n: 0},
				peopleCount: &stubCounter{n: 5},
			},
			lang:       "en-US",
			wantStale:  true,
			wantReason: "empty_seasons",
		},
		{
			name: "empty_people when people count is zero",
			f: fakes{
				series: &stubProbeSeries{canon: catalogseries.Canon{
					ID: 1, Hydration: catalogseries.HydrationFull,
					EnrichmentTMDBSyncedAt: &freshTime,
				}},
				texts:       &stubProbeTexts{},
				seasonsCnt:  &stubCounter{n: 5},
				peopleCount: &stubCounter{n: 0},
			},
			lang:       "en-US",
			wantStale:  true,
			wantReason: "empty_people",
		},
		{
			name: "missing_lang when ru row falls back to en",
			f: fakes{
				series: &stubProbeSeries{canon: catalogseries.Canon{
					ID: 1, Hydration: catalogseries.HydrationFull,
					EnrichmentTMDBSyncedAt: &freshTime,
				}},
				texts:       &stubProbeTexts{row: catalogseries.SeriesText{Language: "en-US"}},
				seasonsCnt:  &stubCounter{n: 5},
				peopleCount: &stubCounter{n: 5},
			},
			lang:       "ru-RU",
			wantStale:  true,
			wantReason: "missing_lang",
		},
		{
			name: "missing_lang when ru lookup returns ErrNotFound",
			f: fakes{
				series: &stubProbeSeries{canon: catalogseries.Canon{
					ID: 1, Hydration: catalogseries.HydrationFull,
					EnrichmentTMDBSyncedAt: &freshTime,
				}},
				texts:       &stubProbeTexts{err: ports.ErrNotFound},
				seasonsCnt:  &stubCounter{n: 5},
				peopleCount: &stubCounter{n: 5},
			},
			lang:       "ru-RU",
			wantStale:  true,
			wantReason: "missing_lang",
		},
		{
			name: "en-US lang skips missing_lang branch",
			f: fakes{
				series: &stubProbeSeries{canon: catalogseries.Canon{
					ID: 1, Hydration: catalogseries.HydrationFull,
					EnrichmentTMDBSyncedAt: &freshTime,
				}},
				texts:       &stubProbeTexts{err: ports.ErrNotFound},
				seasonsCnt:  &stubCounter{n: 5},
				peopleCount: &stubCounter{n: 5},
			},
			lang:       "en-US",
			wantStale:  false,
			wantReason: "fresh",
		},
		{
			name: "empty lang skips missing_lang branch",
			f: fakes{
				series: &stubProbeSeries{canon: catalogseries.Canon{
					ID: 1, Hydration: catalogseries.HydrationFull,
					EnrichmentTMDBSyncedAt: &freshTime,
				}},
				texts:       &stubProbeTexts{err: ports.ErrNotFound},
				seasonsCnt:  &stubCounter{n: 5},
				peopleCount: &stubCounter{n: 5},
			},
			lang:       "",
			wantStale:  false,
			wantReason: "fresh",
		},
		{
			name: "fresh when all signals pass and ru row is present",
			f: fakes{
				series: &stubProbeSeries{canon: catalogseries.Canon{
					ID: 1, Hydration: catalogseries.HydrationFull,
					EnrichmentTMDBSyncedAt: &freshTime,
				}},
				texts:       &stubProbeTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
				seasonsCnt:  &stubCounter{n: 5},
				peopleCount: &stubCounter{n: 5},
			},
			lang:       "ru-RU",
			wantStale:  false,
			wantReason: "fresh",
		},
		{
			name: "permissive when peopleCount returns error",
			f: fakes{
				series: &stubProbeSeries{canon: catalogseries.Canon{
					ID: 1, Hydration: catalogseries.HydrationFull,
					EnrichmentTMDBSyncedAt: &freshTime,
				}},
				texts:       &stubProbeTexts{row: catalogseries.SeriesText{Language: "en-US"}},
				seasonsCnt:  &stubCounter{n: 5},
				peopleCount: &stubCounter{n: 0, err: errors.New("boom")},
			},
			lang:       "en-US",
			wantStale:  false,
			wantReason: "fresh",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			probe := makeProbe(tc.f)
			stale, reason := probe.IsStale(context.Background(), domain.SeriesID(1), tc.lang)
			assert.Equal(t, tc.wantStale, stale)
			assert.Equal(t, tc.wantReason, reason)
		})
	}
}
