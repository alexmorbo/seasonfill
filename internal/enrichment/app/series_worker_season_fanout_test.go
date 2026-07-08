package enrichment

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// concurrencyTMDB is a TMDBClient fake for the Story 1096 (Fix B) bounded
// per-season fan-out. GetSeason records every requested season number and
// tracks the max number of GetSeason calls in-flight simultaneously so a
// test can assert the SetLimit bound is respected. An optional per-season
// error models a TMDB failure; an optional delay widens the concurrency
// window so the max-in-flight measurement is stable.
type concurrencyTMDB struct {
	tv      *tmdb.TVResponse
	seasons map[int]*tmdb.SeasonResponse
	errOn   int // season number to fail on; -1 = never
	delay   time.Duration

	mu          sync.Mutex
	fetched     map[int]bool
	inFlight    int
	maxInFlight int
}

func newConcurrencyTMDB(tv *tmdb.TVResponse, seasons map[int]*tmdb.SeasonResponse) *concurrencyTMDB {
	return &concurrencyTMDB{tv: tv, seasons: seasons, errOn: -1, fetched: map[int]bool{}}
}

func (f *concurrencyTMDB) GetTV(ctx context.Context, id int64, language string) (*tmdb.TVResponse, error) {
	return f.tv, nil
}

func (f *concurrencyTMDB) GetTVAllLangs(ctx context.Context, id int64) (*tmdb.TVResponse, error) {
	return f.tv, nil
}

func (f *concurrencyTMDB) GetSeason(ctx context.Context, tvID int64, seasonNumber int, language string) (*tmdb.SeasonResponse, error) {
	f.mu.Lock()
	f.fetched[seasonNumber] = true
	f.inFlight++
	if f.inFlight > f.maxInFlight {
		f.maxInFlight = f.inFlight
	}
	shouldErr := f.errOn == seasonNumber
	f.mu.Unlock()

	if f.delay > 0 {
		time.Sleep(f.delay)
	}

	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()

	if shouldErr {
		return nil, errors.New("tmdb 503")
	}
	return f.seasons[seasonNumber], nil
}

func (f *concurrencyTMDB) GetPerson(ctx context.Context, id int64, language string) (*tmdb.PersonResponse, error) {
	return nil, nil
}

func (f *concurrencyTMDB) FindByTVDB(ctx context.Context, tvdbID domain.TVDBID) (*tmdb.FindResponse, error) {
	return nil, nil
}

func (f *concurrencyTMDB) maxConcurrent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxInFlight
}

func (f *concurrencyTMDB) fetchedSeasons() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int, 0, len(f.fetched))
	for n := range f.fetched {
		out = append(out, n)
	}
	return out
}

// multiSeasonTV builds a TV payload with `count` real seasons (1..count),
// all with an AirDate so seasonNeedsFetch on refresh would keep them.
func multiSeasonTV(count int) *tmdb.TVResponse {
	tv := &tmdb.TVResponse{
		ID:           42,
		Name:         "Multi",
		Status:       "Returning Series",
		InProduction: true,
	}
	for n := 1; n <= count; n++ {
		tv.Seasons = append(tv.Seasons, tmdb.TVSeasonStub{
			ID:           int64(100 + n),
			SeasonNumber: n,
			Name:         fmt.Sprintf("S%d", n),
			AirDate:      "2025-01-01",
		})
	}
	return tv
}

func multiSeasonResponses(count int) map[int]*tmdb.SeasonResponse {
	m := make(map[int]*tmdb.SeasonResponse, count)
	for n := 1; n <= count; n++ {
		m[n] = &tmdb.SeasonResponse{
			ID: int64(100 + n), SeasonNumber: n, AirDate: "2025-01-01",
			Episodes: []tmdb.SeasonEpisode{
				{ID: int64(10000 + n), EpisodeNumber: 1, SeasonNumber: n, Name: "E1", EpisodeType: "standard"},
			},
		}
	}
	return m
}

// fanoutWorker builds a SeriesWorker wired to the given TMDB fake and season
// concurrency, reusing the standard in-memory fake repos from newWorkerFixture.
func fanoutWorker(t *testing.T, tmdbClient TMDBClient, seasonConcurrency int) *SeriesWorker {
	t.Helper()
	f := newWorkerFixture(t, nil, nil)
	w, err := NewSeriesWorker(SeriesWorkerDeps{
		TMDB:               tmdbClient,
		Tx:                 syncTransactor{},
		Languages:          []string{"en-US"},
		Series:             f.series,
		SeriesTexts:        f.seriesTexts,
		Seasons:            f.seasons,
		Episodes:           f.episodes,
		EpisodeTexts:       f.episodeTexts,
		SeasonTexts:        f.seasonTexts,
		People:             f.people,
		PersonCredits:      f.personCredits,
		PersonCreditsTexts: f.personCreditsTexts,
		Genres:             f.genres,
		Keywords:           f.keywords,
		Networks:           f.networks,
		Companies:          f.companies,
		Videos:             f.videos,
		ContentRatings:     f.contentRatings,
		ExternalIDs:        f.externalIDs,
		Recommendations:    f.recommendations,
		EnrichmentErrors:   f.enrichmentErrors,
		SeasonConcurrency:  seasonConcurrency,
		Logger:             quietLogger(),
		Clock:              func() time.Time { return time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC) },
	})
	require.NoError(t, err)
	return w
}

func fanoutCanon() series.Canon {
	tmdbID := domain.TMDBID(42)
	// EnrichmentTMDBSyncedAt nil → firstHydration=true → all seasons fetched.
	return series.Canon{ID: 1, TMDBID: &tmdbID, OriginalTitle: new("Multi"), InProduction: true}
}

// (a) all qualifying seasons fetched; result assembly is order-independent.
func TestRefreshOneLanguage_FanoutFetchesAllSeasons(t *testing.T) {
	t.Parallel()
	const seasons = 7
	fake := newConcurrencyTMDB(multiSeasonTV(seasons), multiSeasonResponses(seasons))
	w := fanoutWorker(t, fake, 4)

	pe, pw, oe := false, false, false
	err := w.refreshOneLanguage(context.Background(), fanoutCanon(), "en-US", false, &pe, &pw, &oe, quietLogger())
	require.NoError(t, err)

	got := fake.fetchedSeasons()
	assert.ElementsMatch(t, []int{1, 2, 3, 4, 5, 6, 7}, got)
}

// (b) a GetSeason error propagates with the preserved message shape.
func TestRefreshOneLanguage_FanoutFirstErrorPropagates(t *testing.T) {
	t.Parallel()
	const seasons = 6
	fake := newConcurrencyTMDB(multiSeasonTV(seasons), multiSeasonResponses(seasons))
	fake.errOn = 4
	w := fanoutWorker(t, fake, 3)

	pe, pw, oe := false, false, false
	err := w.refreshOneLanguage(context.Background(), fanoutCanon(), "ru-RU", false, &pe, &pw, &oe, quietLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetSeason(")
	assert.Contains(t, err.Error(), "GetSeason(4,ru-RU)")
}

// (c) max concurrent in-flight GetSeason never exceeds the configured limit.
func TestRefreshOneLanguage_FanoutRespectsConcurrencyLimit(t *testing.T) {
	t.Parallel()
	const seasons = 12
	const limit = 3
	fake := newConcurrencyTMDB(multiSeasonTV(seasons), multiSeasonResponses(seasons))
	fake.delay = 15 * time.Millisecond // widen the window so overlap is observable
	w := fanoutWorker(t, fake, limit)

	pe, pw, oe := false, false, false
	err := w.refreshOneLanguage(context.Background(), fanoutCanon(), "en-US", false, &pe, &pw, &oe, quietLogger())
	require.NoError(t, err)

	assert.LessOrEqual(t, fake.maxConcurrent(), limit, "max in-flight GetSeason must not exceed SetLimit")
	assert.Greater(t, fake.maxConcurrent(), 1, "expected real parallelism with limit>1 and 12 seasons")
}

// concurrency <1 clamps to sequential (1 in-flight) without deadlocking.
func TestRefreshOneLanguage_FanoutClampsToSequential(t *testing.T) {
	t.Parallel()
	const seasons = 5
	fake := newConcurrencyTMDB(multiSeasonTV(seasons), multiSeasonResponses(seasons))
	fake.delay = 5 * time.Millisecond
	w := fanoutWorker(t, fake, 0) // must clamp to 1

	pe, pw, oe := false, false, false
	err := w.refreshOneLanguage(context.Background(), fanoutCanon(), "en-US", false, &pe, &pw, &oe, quietLogger())
	require.NoError(t, err)
	assert.Equal(t, 1, fake.maxConcurrent(), "clamp-to-1 must serialise GetSeason")
	assert.ElementsMatch(t, []int{1, 2, 3, 4, 5}, fake.fetchedSeasons())
}

// seasonNeedsFetch gating (incl. season-0 skip) is preserved under the
// fan-out: on a refresh (force=false, already-synced) season 0 is not fetched.
func TestRefreshOneLanguage_FanoutPreservesSeasonGate(t *testing.T) {
	t.Parallel()
	// Recent air date so seasons 1-2 pass the refresh gate (within 365d);
	// season 0 must still be skipped regardless of its date.
	recent := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	tv := multiSeasonTV(2)
	for i := range tv.Seasons {
		tv.Seasons[i].AirDate = recent
	}
	tv.Seasons = append([]tmdb.TVSeasonStub{{ID: 100, SeasonNumber: 0, Name: "Specials", AirDate: recent}}, tv.Seasons...)
	resp := multiSeasonResponses(2)
	resp[0] = &tmdb.SeasonResponse{ID: 100, SeasonNumber: 0}
	fake := newConcurrencyTMDB(tv, resp)
	w := fanoutWorker(t, fake, 4)

	// Already-synced canon + force=false → firstHydration=false → gate active.
	synced := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	canon := fanoutCanon()
	canon.EnrichmentTMDBSyncedAt = &synced

	pe, pw, oe := false, false, false
	err := w.refreshOneLanguage(context.Background(), canon, "en-US", false, &pe, &pw, &oe, quietLogger())
	require.NoError(t, err)
	got := fake.fetchedSeasons()
	assert.NotContains(t, got, 0, "season 0 must be skipped on refresh")
	assert.ElementsMatch(t, []int{1, 2}, got, "recent real seasons must still be fetched")
}
