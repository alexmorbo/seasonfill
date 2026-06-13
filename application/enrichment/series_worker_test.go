package enrichment

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/enrichment"
	"github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/infrastructure/tmdb"
)

// ---- fakes ---------------------------------------------------------

type fakeTMDB struct {
	tv       *tmdb.TVResponse
	tvErr    error
	seasons  map[int]*tmdb.SeasonResponse
	seasErr  map[int]error
	getTVHit int
	mu       sync.Mutex
	calls    []string
}

func (f *fakeTMDB) GetTV(ctx context.Context, id int64, language string) (*tmdb.TVResponse, error) {
	f.mu.Lock()
	f.getTVHit++
	f.calls = append(f.calls, "GetTV")
	f.mu.Unlock()
	return f.tv, f.tvErr
}

func (f *fakeTMDB) GetSeason(ctx context.Context, tvID int64, seasonNumber int, language string) (*tmdb.SeasonResponse, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "GetSeason")
	f.mu.Unlock()
	if f.seasErr != nil {
		if err, ok := f.seasErr[seasonNumber]; ok && err != nil {
			return nil, err
		}
	}
	if f.seasons == nil {
		return nil, nil
	}
	return f.seasons[seasonNumber], nil
}

func (f *fakeTMDB) GetPerson(ctx context.Context, id int64, language string) (*tmdb.PersonResponse, error) {
	return nil, nil
}

func (f *fakeTMDB) FindByTVDB(ctx context.Context, tvdbID int64) (*tmdb.FindResponse, error) {
	return nil, nil
}

// syncTransactor runs fn synchronously without opening a real tx —
// the tests don't need atomicity, only the call surface.
type syncTransactor struct{}

func (syncTransactor) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// callRecord captures (method, arg-fingerprint) for write-order
// assertions.
type callRecord struct {
	mu  sync.Mutex
	out []string
}

func (c *callRecord) add(s string) {
	c.mu.Lock()
	c.out = append(c.out, s)
	c.mu.Unlock()
}

func (c *callRecord) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.out))
	copy(out, c.out)
	return out
}

// fakeSeriesRepo + others - minimal in-memory stubs.
type fakeSeriesRepo struct {
	rec     *callRecord
	rows    map[int64]series.Canon
	nextID  int64
	upsertN int
}

func newFakeSeriesRepo(rec *callRecord) *fakeSeriesRepo {
	return &fakeSeriesRepo{rec: rec, rows: make(map[int64]series.Canon), nextID: 100}
}

func (f *fakeSeriesRepo) Get(ctx context.Context, id int64) (series.Canon, error) {
	if c, ok := f.rows[id]; ok {
		return c, nil
	}
	return series.Canon{}, ports.ErrNotFound
}

func (f *fakeSeriesRepo) Upsert(ctx context.Context, c series.Canon) (int64, error) {
	f.upsertN++
	f.rec.add("Series.Upsert")
	if c.ID == 0 {
		f.nextID++
		c.ID = f.nextID
	}
	f.rows[c.ID] = c
	return c.ID, nil
}

type fakeSeriesTextsRepo struct {
	rec  *callRecord
	rows []series.SeriesText
}

func (f *fakeSeriesTextsRepo) Upsert(ctx context.Context, t series.SeriesText) error {
	f.rec.add("SeriesTexts.Upsert")
	f.rows = append(f.rows, t)
	return nil
}

type fakeSeasonsRepo struct {
	rec    *callRecord
	rows   map[int]series.CanonSeason
	nextID int64
}

func newFakeSeasonsRepo(rec *callRecord) *fakeSeasonsRepo {
	return &fakeSeasonsRepo{rec: rec, rows: make(map[int]series.CanonSeason), nextID: 1000}
}

func (f *fakeSeasonsRepo) ListBySeries(ctx context.Context, seriesID int64) ([]series.CanonSeason, error) {
	return nil, nil
}

func (f *fakeSeasonsRepo) Upsert(ctx context.Context, s series.CanonSeason) (int64, error) {
	f.rec.add("Seasons.Upsert")
	f.nextID++
	s.ID = f.nextID
	f.rows[s.SeasonNumber] = s
	return s.ID, nil
}

type fakeEpisodesRepo struct {
	rec    *callRecord
	rows   []series.CanonEpisode
	nextID int64
}

func newFakeEpisodesRepo(rec *callRecord) *fakeEpisodesRepo {
	return &fakeEpisodesRepo{rec: rec, nextID: 5000}
}

func (f *fakeEpisodesRepo) ListBySeries(ctx context.Context, seriesID int64) ([]series.CanonEpisode, error) {
	return nil, nil
}

func (f *fakeEpisodesRepo) BatchUpsert(ctx context.Context, eps []series.CanonEpisode) ([]int64, error) {
	f.rec.add("Episodes.BatchUpsert")
	ids := make([]int64, 0, len(eps))
	for _, e := range eps {
		f.nextID++
		e.ID = f.nextID
		f.rows = append(f.rows, e)
		ids = append(ids, e.ID)
	}
	return ids, nil
}

type fakeEpisodeTextsRepo struct {
	rec  *callRecord
	rows []series.EpisodeText
}

func (f *fakeEpisodeTextsRepo) Upsert(ctx context.Context, t series.EpisodeText) error {
	f.rec.add("EpisodeTexts.Upsert")
	f.rows = append(f.rows, t)
	return nil
}

type fakePeopleRepo struct {
	rec    *callRecord
	rows   map[int]people.Person // keyed on tmdb_id
	nextID int64
}

func newFakePeopleRepo(rec *callRecord) *fakePeopleRepo {
	return &fakePeopleRepo{rec: rec, rows: make(map[int]people.Person), nextID: 9000}
}

func (f *fakePeopleRepo) GetByTMDBID(ctx context.Context, tmdbID int) (people.Person, error) {
	if p, ok := f.rows[tmdbID]; ok {
		return p, nil
	}
	return people.Person{}, ports.ErrNotFound
}

func (f *fakePeopleRepo) Upsert(ctx context.Context, p people.Person) (int64, error) {
	f.rec.add("People.Upsert")
	if p.TMDBID != nil {
		if existing, ok := f.rows[*p.TMDBID]; ok {
			return existing.ID, nil
		}
	}
	f.nextID++
	p.ID = f.nextID
	if p.TMDBID != nil {
		f.rows[*p.TMDBID] = p
	}
	return p.ID, nil
}

type fakeSeriesPeopleRepo struct {
	rec  *callRecord
	rows []people.SeriesCredit
}

func (f *fakeSeriesPeopleRepo) BatchUpsert(ctx context.Context, credits []people.SeriesCredit) ([]int64, error) {
	f.rec.add("SeriesPeople.BatchUpsert")
	ids := make([]int64, 0, len(credits))
	for i, c := range credits {
		ids = append(ids, int64(i+1))
		f.rows = append(f.rows, c)
	}
	return ids, nil
}

type fakeGenresRepo struct {
	rec     *callRecord
	rows    map[int]int64 // tmdb_id -> id
	nextID  int64
	setCall []int64
	i18n    []taxonomy.GenreI18n
}

func newFakeGenresRepo(rec *callRecord) *fakeGenresRepo {
	return &fakeGenresRepo{rec: rec, rows: make(map[int]int64), nextID: 200}
}

func (f *fakeGenresRepo) Upsert(ctx context.Context, g taxonomy.Genre) (int64, error) {
	f.rec.add("Genres.Upsert")
	if g.TMDBID != nil {
		if id, ok := f.rows[*g.TMDBID]; ok {
			return id, nil
		}
	}
	f.nextID++
	if g.TMDBID != nil {
		f.rows[*g.TMDBID] = f.nextID
	}
	return f.nextID, nil
}

func (f *fakeGenresRepo) UpsertI18n(ctx context.Context, genreID int64, language, name string) error {
	f.rec.add("Genres.UpsertI18n")
	f.i18n = append(f.i18n, taxonomy.GenreI18n{GenreID: genreID, Language: language, Name: name})
	return nil
}

func (f *fakeGenresRepo) Set(ctx context.Context, seriesID int64, ids []int64) error {
	f.rec.add("Genres.Set")
	f.setCall = ids
	return nil
}

type fakeKeywordsRepo struct {
	rec     *callRecord
	rows    map[int]int64
	nextID  int64
	setCall []int64
	i18n    []taxonomy.KeywordI18n
}

func newFakeKeywordsRepo(rec *callRecord) *fakeKeywordsRepo {
	return &fakeKeywordsRepo{rec: rec, rows: make(map[int]int64), nextID: 300}
}

func (f *fakeKeywordsRepo) Upsert(ctx context.Context, k taxonomy.Keyword) (int64, error) {
	f.rec.add("Keywords.Upsert")
	if k.TMDBID != nil {
		if id, ok := f.rows[*k.TMDBID]; ok {
			return id, nil
		}
	}
	f.nextID++
	if k.TMDBID != nil {
		f.rows[*k.TMDBID] = f.nextID
	}
	return f.nextID, nil
}

func (f *fakeKeywordsRepo) UpsertI18n(ctx context.Context, keywordID int64, language, name string) error {
	f.rec.add("Keywords.UpsertI18n")
	f.i18n = append(f.i18n, taxonomy.KeywordI18n{KeywordID: keywordID, Language: language, Name: name})
	return nil
}

func (f *fakeKeywordsRepo) Set(ctx context.Context, seriesID int64, ids []int64) error {
	f.rec.add("Keywords.Set")
	f.setCall = ids
	return nil
}

type fakeNetworksRepo struct {
	rec     *callRecord
	nextID  int64
	setCall []int64
}

func (f *fakeNetworksRepo) Upsert(ctx context.Context, n taxonomy.Network) (int64, error) {
	f.rec.add("Networks.Upsert")
	f.nextID++
	return f.nextID, nil
}

func (f *fakeNetworksRepo) Set(ctx context.Context, seriesID int64, ids []int64) error {
	f.rec.add("Networks.Set")
	f.setCall = ids
	return nil
}

type fakeCompaniesRepo struct {
	rec     *callRecord
	nextID  int64
	setCall []int64
}

func (f *fakeCompaniesRepo) Upsert(ctx context.Context, c taxonomy.ProductionCompany) (int64, error) {
	f.rec.add("Companies.Upsert")
	f.nextID++
	return f.nextID, nil
}

func (f *fakeCompaniesRepo) Set(ctx context.Context, seriesID int64, ids []int64) error {
	f.rec.add("Companies.Set")
	f.setCall = ids
	return nil
}

type fakeVideosRepo struct {
	rec  *callRecord
	rows []VideoRow
}

func (f *fakeVideosRepo) Upsert(ctx context.Context, v VideoRow) error {
	f.rec.add("Videos.Upsert")
	f.rows = append(f.rows, v)
	return nil
}

type fakeContentRatingsRepo struct {
	rec  *callRecord
	rows []string
}

func (f *fakeContentRatingsRepo) Upsert(ctx context.Context, seriesID int64, country, rating string) error {
	f.rec.add("ContentRatings.Upsert")
	f.rows = append(f.rows, country+":"+rating)
	return nil
}

type fakeExternalIDsRepo struct {
	rec  *callRecord
	rows []string
}

func (f *fakeExternalIDsRepo) Upsert(ctx context.Context, entityType enrichment.EntityType, entityID int64, provider, value string) error {
	f.rec.add("ExternalIDs.Upsert")
	f.rows = append(f.rows, provider+":"+value)
	return nil
}

type fakeRecommendationsRepo struct {
	rec  *callRecord
	last []int64
}

func (f *fakeRecommendationsRepo) Set(ctx context.Context, seriesID int64, recommendedIDs []int64) error {
	f.rec.add("Recommendations.Set")
	f.last = recommendedIDs
	return nil
}

type fakeSyncLogRepo struct {
	mu      sync.Mutex
	entries []enrichment.SyncLog
	last    *enrichment.SyncLog
}

func (f *fakeSyncLogRepo) Upsert(ctx context.Context, e enrichment.SyncLog) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, e)
	return nil
}

func (f *fakeSyncLogRepo) GetLastSync(ctx context.Context, entityType enrichment.EntityType, entityID int64, source enrichment.Source) (enrichment.SyncLog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last != nil {
		return *f.last, nil
	}
	return enrichment.SyncLog{}, ports.ErrNotFound
}

func (f *fakeSyncLogRepo) StaleScan(ctx context.Context, source enrichment.Source, cutoff time.Time, limit int) ([]enrichment.SyncLog, error) {
	return nil, nil
}

func (f *fakeSyncLogRepo) RetryDue(ctx context.Context, source enrichment.Source, now time.Time, limit int) ([]enrichment.SyncLog, error) {
	return nil, nil
}

func (f *fakeSyncLogRepo) lastEntry() enrichment.SyncLog {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.entries) == 0 {
		return enrichment.SyncLog{}
	}
	return f.entries[len(f.entries)-1]
}

// ---- harness -------------------------------------------------------

type workerFixture struct {
	worker          *SeriesWorker
	tmdb            *fakeTMDB
	rec             *callRecord
	series          *fakeSeriesRepo
	seriesTexts     *fakeSeriesTextsRepo
	seasons         *fakeSeasonsRepo
	episodes        *fakeEpisodesRepo
	episodeTexts    *fakeEpisodeTextsRepo
	people          *fakePeopleRepo
	seriesPeople    *fakeSeriesPeopleRepo
	genres          *fakeGenresRepo
	keywords        *fakeKeywordsRepo
	networks        *fakeNetworksRepo
	companies       *fakeCompaniesRepo
	videos          *fakeVideosRepo
	contentRatings  *fakeContentRatingsRepo
	externalIDs     *fakeExternalIDsRepo
	recommendations *fakeRecommendationsRepo
	syncLog         *fakeSyncLogRepo
}

func newWorkerFixture(t *testing.T, tv *tmdb.TVResponse, seasonResp map[int]*tmdb.SeasonResponse) *workerFixture {
	t.Helper()
	rec := &callRecord{}
	f := &workerFixture{
		tmdb:            &fakeTMDB{tv: tv, seasons: seasonResp},
		rec:             rec,
		series:          newFakeSeriesRepo(rec),
		seriesTexts:     &fakeSeriesTextsRepo{rec: rec},
		seasons:         newFakeSeasonsRepo(rec),
		episodes:        newFakeEpisodesRepo(rec),
		episodeTexts:    &fakeEpisodeTextsRepo{rec: rec},
		people:          newFakePeopleRepo(rec),
		seriesPeople:    &fakeSeriesPeopleRepo{rec: rec},
		genres:          newFakeGenresRepo(rec),
		keywords:        newFakeKeywordsRepo(rec),
		networks:        &fakeNetworksRepo{rec: rec},
		companies:       &fakeCompaniesRepo{rec: rec},
		videos:          &fakeVideosRepo{rec: rec},
		contentRatings:  &fakeContentRatingsRepo{rec: rec},
		externalIDs:     &fakeExternalIDsRepo{rec: rec},
		recommendations: &fakeRecommendationsRepo{rec: rec},
		syncLog:         &fakeSyncLogRepo{},
	}
	w, err := NewSeriesWorker(SeriesWorkerDeps{
		TMDB:            f.tmdb,
		Tx:              syncTransactor{},
		Language:        "en-US",
		Series:          f.series,
		SeriesTexts:     f.seriesTexts,
		Seasons:         f.seasons,
		Episodes:        f.episodes,
		EpisodeTexts:    f.episodeTexts,
		People:          f.people,
		SeriesPeople:    f.seriesPeople,
		Genres:          f.genres,
		Keywords:        f.keywords,
		Networks:        f.networks,
		Companies:       f.companies,
		Videos:          f.videos,
		ContentRatings:  f.contentRatings,
		ExternalIDs:     f.externalIDs,
		Recommendations: f.recommendations,
		SyncLog:         f.syncLog,
		Logger:          quietLogger(),
		Clock:           func() time.Time { return time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC) },
	})
	require.NoError(t, err)
	f.worker = w
	return f
}

// seedCanon installs a canon row at id=1 with tmdbID=42.
func (f *workerFixture) seedCanon(id int64, tmdbID *int) {
	f.series.rows[id] = series.Canon{ID: id, TMDBID: tmdbID, Title: "Existing", InProduction: true}
}

// minimalTV constructs a TV payload with one season, one episode,
// one cast member, one crew member, one genre, one keyword, one
// network, one company, one video, one content_rating, one external
// id, one recommendation.
func minimalTV() *tmdb.TVResponse {
	tvdb := int64(99)
	return &tmdb.TVResponse{
		ID:           42,
		Name:         "Show",
		Overview:     "ov",
		Tagline:      "tag",
		Status:       "Returning Series",
		FirstAirDate: "2020-01-01",
		InProduction: true,
		Networks:     []tmdb.TVNetwork{{ID: 1, Name: "HBO"}},
		ProductionCompanies: []tmdb.TVCompany{
			{ID: 2, Name: "WB"},
		},
		Genres: []tmdb.TVGenre{{ID: 18, Name: "Drama"}},
		Seasons: []tmdb.TVSeasonStub{
			{ID: 100, SeasonNumber: 1, Name: "S1", AirDate: "2025-12-01"},
		},
		AggregateCredits: &tmdb.TVAggregateCredits{
			Cast: []tmdb.TVCastMember{
				{ID: 555, Name: "Actor", Order: 0, TotalEpisodeCount: 12, Roles: []tmdb.TVRole{
					{CreditID: "cast-credit-1", Character: "Hero", EpisodeCount: 12},
				}},
			},
			Crew: []tmdb.TVCrewMember{
				{ID: 666, Name: "Director", Department: "Directing", Jobs: []tmdb.TVJob{
					{CreditID: "crew-credit-1", Job: "Director", EpisodeCount: 3},
				}},
			},
		},
		Videos: &tmdb.TVVideos{
			Results: []tmdb.TVVideo{{ID: "v1", Name: "Trailer", Key: "abc", Site: "YouTube", Type: "Trailer", Official: true}},
		},
		ContentRatings: &tmdb.TVContentRatings{
			Results: []tmdb.TVContentRating{{ISO31661: "US", Rating: "TV-MA"}},
		},
		ExternalIDs: &tmdb.TVExternalIDs{IMDBID: "tt0001", TVDBID: &tvdb},
		Keywords:    &tmdb.TVKeywords{Results: []tmdb.TVKeyword{{ID: 7, Name: "drama"}}},
		Recommendations: &tmdb.TVRecommendations{
			Results: []tmdb.TVRecommendation{{ID: 999, Name: "Other Show", FirstAirDate: "2019-03-01", PosterPath: "/p.jpg"}},
		},
	}
}

func minimalSeason() *tmdb.SeasonResponse {
	return &tmdb.SeasonResponse{
		ID: 100, SeasonNumber: 1, AirDate: "2025-12-01",
		Episodes: []tmdb.SeasonEpisode{
			{ID: 10001, EpisodeNumber: 1, SeasonNumber: 1, Name: "Pilot", Overview: "pilot ov", EpisodeType: "standard"},
		},
	}
}

// ---- tests ---------------------------------------------------------

func TestSeriesWorker_HappyPath_AllRepoesWritten(t *testing.T) {
	t.Parallel()
	tv := minimalTV()
	seasons := map[int]*tmdb.SeasonResponse{1: minimalSeason()}
	f := newWorkerFixture(t, tv, seasons)
	tmdbID := 42
	f.seedCanon(1, &tmdbID)

	require.NoError(t, f.worker.Handle(context.Background(), 1))

	// Verify the call list contains each repo's write.
	calls := f.rec.list()
	expect := []string{
		"Series.Upsert",
		"SeriesTexts.Upsert",
		"Seasons.Upsert",
		"Episodes.BatchUpsert",
		"EpisodeTexts.Upsert",
		"People.Upsert",
		"SeriesPeople.BatchUpsert",
		"Genres.Upsert",
		"Genres.Set",
		"Keywords.Upsert",
		"Keywords.Set",
		"Networks.Upsert",
		"Networks.Set",
		"Companies.Upsert",
		"Companies.Set",
		"Videos.Upsert",
		"ContentRatings.Upsert",
		"ExternalIDs.Upsert",
		"Recommendations.Set",
	}
	for _, e := range expect {
		assert.Contains(t, calls, e, "expected call %q recorded", e)
	}

	// Verify sync_log was journalled with outcome=ok.
	last := f.syncLog.lastEntry()
	assert.Equal(t, enrichment.OutcomeOK, last.Outcome)
	assert.Equal(t, 0, last.Attempts, "attempts reset to 0 on success per PRD §5.5")

	// Verify SeriesPeople rows got non-zero PersonID (LATENT RISK fix).
	require.NotEmpty(t, f.seriesPeople.rows, "series_people should be written")
	for _, sp := range f.seriesPeople.rows {
		assert.NotEqual(t, int64(0), sp.PersonID, "PersonID must be resolved")
	}
}

func TestSeriesWorker_TMDB5xxDuringSeasonFetch_NoHalfWrites(t *testing.T) {
	t.Parallel()
	tv := minimalTV()
	f := newWorkerFixture(t, tv, nil)
	f.tmdb.seasErr = map[int]error{1: &tmdb.APIError{Status: 500, Body: "boom"}}
	tmdbID := 42
	f.seedCanon(1, &tmdbID)

	require.NoError(t, f.worker.Handle(context.Background(), 1))

	calls := f.rec.list()
	// Series.Upsert MUST NOT have been called (no half-writes).
	assert.NotContains(t, calls, "Series.Upsert", "no DB writes after TMDB 5xx mid-fetch")
	last := f.syncLog.lastEntry()
	assert.Equal(t, enrichment.OutcomeError, last.Outcome)
	assert.Equal(t, 1, last.Attempts)
	require.NotNil(t, last.NextAttemptAt)
}

func TestSeriesWorker_TMDB404_TerminalNotFound(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t, nil, nil)
	f.tmdb.tvErr = &tmdb.APIError{Status: 404, Body: "not found"}
	tmdbID := 42
	f.seedCanon(1, &tmdbID)

	require.NoError(t, f.worker.Handle(context.Background(), 1))

	last := f.syncLog.lastEntry()
	assert.Equal(t, enrichment.OutcomeNotFound, last.Outcome)
	assert.Nil(t, last.NextAttemptAt, "not_found is terminal — no retry scheduled")
}

func TestSeriesWorker_FreshSkip_TTLNotExpired(t *testing.T) {
	t.Parallel()
	tv := minimalTV()
	f := newWorkerFixture(t, tv, map[int]*tmdb.SeasonResponse{1: minimalSeason()})
	tmdbID := 42
	f.seedCanon(1, &tmdbID)
	// Last sync 1h ago — TTL for continuing series is 24h.
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	syncedAt := now.Add(-1 * time.Hour)
	f.syncLog.last = &enrichment.SyncLog{Outcome: enrichment.OutcomeOK, SyncedAt: &syncedAt}

	require.NoError(t, f.worker.Handle(context.Background(), 1))

	calls := f.rec.list()
	assert.Empty(t, calls, "fresh TTL ⇒ no work")
	assert.Equal(t, 0, f.tmdb.getTVHit, "no TMDB call on fresh skip")
}

func TestSeriesWorker_NoTMDBID_TerminalNotFound(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t, nil, nil)
	f.seedCanon(1, nil) // tmdb_id is nil

	require.NoError(t, f.worker.Handle(context.Background(), 1))

	last := f.syncLog.lastEntry()
	assert.Equal(t, enrichment.OutcomeNotFound, last.Outcome)
	assert.Equal(t, 0, f.tmdb.getTVHit, "no TMDB call when no tmdb_id")
}

func TestSeriesWorker_BackoffIncrements(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t, nil, nil)
	f.tmdb.tvErr = errors.New("network kaboom")
	tmdbID := 42
	f.seedCanon(1, &tmdbID)

	// First failure.
	require.NoError(t, f.worker.Handle(context.Background(), 1))
	first := f.syncLog.lastEntry()
	require.Equal(t, 1, first.Attempts)

	// Second failure — install previous attempt count.
	f.syncLog.last = &enrichment.SyncLog{Outcome: enrichment.OutcomeError, Attempts: 1}
	require.NoError(t, f.worker.Handle(context.Background(), 1))
	second := f.syncLog.lastEntry()
	assert.Equal(t, 2, second.Attempts, "attempts increments across failures")
	require.NotNil(t, second.NextAttemptAt)
	require.NotNil(t, first.NextAttemptAt)
	assert.True(t, second.NextAttemptAt.After(*first.NextAttemptAt) || second.NextAttemptAt.Equal(*first.NextAttemptAt))
}

func TestSeriesWorker_DeterministicWriteOrder(t *testing.T) {
	t.Parallel()
	tv := minimalTV()
	f := newWorkerFixture(t, tv, map[int]*tmdb.SeasonResponse{1: minimalSeason()})
	tmdbID := 42
	f.seedCanon(1, &tmdbID)

	require.NoError(t, f.worker.Handle(context.Background(), 1))

	calls := f.rec.list()
	// Find indices of key milestones.
	idx := func(name string) int {
		for i, c := range calls {
			if c == name {
				return i
			}
		}
		return -1
	}
	mustOrder := func(a, b string) {
		ai, bi := idx(a), idx(b)
		require.GreaterOrEqual(t, ai, 0, "missing call %q", a)
		require.GreaterOrEqual(t, bi, 0, "missing call %q", b)
		assert.Less(t, ai, bi, "%q must precede %q", a, b)
	}
	mustOrder("Series.Upsert", "SeriesTexts.Upsert")
	mustOrder("SeriesTexts.Upsert", "Seasons.Upsert")
	mustOrder("Seasons.Upsert", "Episodes.BatchUpsert")
	mustOrder("Episodes.BatchUpsert", "EpisodeTexts.Upsert")
	mustOrder("EpisodeTexts.Upsert", "People.Upsert")
	mustOrder("People.Upsert", "SeriesPeople.BatchUpsert")
	mustOrder("SeriesPeople.BatchUpsert", "Genres.Upsert")
	mustOrder("Genres.Set", "Videos.Upsert")
	mustOrder("Videos.Upsert", "ContentRatings.Upsert")
	mustOrder("ContentRatings.Upsert", "ExternalIDs.Upsert")
	mustOrder("ExternalIDs.Upsert", "Recommendations.Set")
}

func TestSeriesWorker_RejectsMissingDeps(t *testing.T) {
	t.Parallel()
	_, err := NewSeriesWorker(SeriesWorkerDeps{})
	assert.Error(t, err)
}
