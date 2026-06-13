package people

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appenrich "github.com/alexmorbo/seasonfill/application/enrichment"
	"github.com/alexmorbo/seasonfill/application/ports"
	domenrich "github.com/alexmorbo/seasonfill/domain/enrichment"
	dompeople "github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/domain/series"
)

// --- inline fakes ---

type fakePeopleReader struct {
	byTMDB  map[int]dompeople.Person
	byID    map[int64]dompeople.Person
	errTMDB error
	errID   error
}

func (f *fakePeopleReader) GetByTMDBID(_ context.Context, tmdbID int) (dompeople.Person, error) {
	if f.errTMDB != nil {
		return dompeople.Person{}, f.errTMDB
	}
	p, ok := f.byTMDB[tmdbID]
	if !ok {
		return dompeople.Person{}, ports.ErrNotFound
	}
	return p, nil
}

func (f *fakePeopleReader) GetWithBio(_ context.Context, id int64, _ string) (dompeople.Person, error) {
	if f.errID != nil {
		return dompeople.Person{}, f.errID
	}
	p, ok := f.byID[id]
	if !ok {
		return dompeople.Person{}, ports.ErrNotFound
	}
	return p, nil
}

type fakePersonCredits struct {
	rows []dompeople.PersonCredit
	err  error
}

func (f *fakePersonCredits) ListByPerson(_ context.Context, _ int64) ([]dompeople.PersonCredit, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

type fakeSeriesByTMDB struct {
	rows map[int]series.Canon
	errs map[int]error
}

func (f *fakeSeriesByTMDB) GetByTMDBID(_ context.Context, tmdbID int) (series.Canon, error) {
	if err, ok := f.errs[tmdbID]; ok {
		return series.Canon{}, err
	}
	c, ok := f.rows[tmdbID]
	if !ok {
		return series.Canon{}, ports.ErrNotFound
	}
	return c, nil
}

type fakeSeriesCache struct {
	rows map[int64][]series.CacheEntry
	err  error
}

func (f *fakeSeriesCache) ListBySeriesID(_ context.Context, seriesID int64) ([]series.CacheEntry, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows[seriesID], nil
}

type fakeSyncLog struct {
	row domenrich.SyncLog
	err error
}

func (f *fakeSyncLog) GetLastSync(_ context.Context, _ domenrich.EntityType, _ int64, _ domenrich.Source) (domenrich.SyncLog, error) {
	if f.err != nil {
		return domenrich.SyncLog{}, f.err
	}
	return f.row, nil
}

type fakeEnqueuer struct {
	calls []enqueuedCall
}

type enqueuedCall struct {
	Kind appenrich.EntityKind
	ID   int64
	P    appenrich.Priority
}

func (f *fakeEnqueuer) Enqueue(kind appenrich.EntityKind, id int64, p appenrich.Priority) {
	f.calls = append(f.calls, enqueuedCall{Kind: kind, ID: id, P: p})
}

// --- helpers ---

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func ptr[T any](v T) *T { return &v }

func mkCanon(id int64, tmdbID int, title string, year int, lastAir time.Time) series.Canon {
	return series.Canon{
		ID:          id,
		TMDBID:      ptr(tmdbID),
		Title:       title,
		Year:        ptr(year),
		LastAirDate: ptr(lastAir),
	}
}

func mkCacheRow(instanceName string, seriesID int64) series.CacheEntry {
	return series.CacheEntry{
		InstanceName: instanceName,
		SeriesID:     &seriesID,
	}
}

func mkCredit(id int64, tmdbMediaID int64, mediaType, title string, kind dompeople.SeriesCreditKind, character *string, episodeCount *int) dompeople.PersonCredit {
	return dompeople.PersonCredit{
		ID:            id,
		PersonID:      1,
		MediaType:     mediaType,
		TMDBMediaID:   tmdbMediaID,
		TMDBCreditID:  "cr-" + title,
		Kind:          kind,
		Title:         title,
		CharacterName: character,
		EpisodeCount:  episodeCount,
	}
}

// happyFixture builds the standard Pedro Pascal fixture:
//   - person Pedro Pascal (tmdb_id=4495, full hydration, with bio)
//   - 4 credits: LoU (tv, in library, 9ep, last_air=2026-06-01),
//     GoT (tv, in library, 3ep, last_air=2019-05-19),
//     Narcos (tv, NOT in library, 4ep, last_air=2017-09-01),
//     Strange Way of Life (movie, never in library, 0ep)
func happyFixture(t *testing.T) Deps {
	t.Helper()
	person := dompeople.Person{
		ID:                1,
		TMDBID:            ptr(4495),
		Hydration:         dompeople.HydrationFull,
		Name:              "Pedro Pascal",
		Biography:         "Chilean-American actor...",
		BiographyLanguage: "en-US",
	}

	louLast := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	gotLast := time.Date(2019, 5, 19, 0, 0, 0, 0, time.UTC)

	louCanon := mkCanon(42, 100, "The Last of Us", 2023, louLast)
	gotCanon := mkCanon(43, 200, "Game of Thrones", 2011, gotLast)

	credits := []dompeople.PersonCredit{
		mkCredit(1, 100, "tv", "The Last of Us", dompeople.SeriesCreditCast, ptr("Joel Miller"), ptr(9)),
		mkCredit(2, 200, "tv", "Game of Thrones", dompeople.SeriesCreditCast, ptr("Oberyn Martell"), ptr(3)),
		mkCredit(3, 300, "tv", "Narcos", dompeople.SeriesCreditCast, ptr("Javier Peña"), ptr(4)),
		mkCredit(4, 400, "movie", "Strange Way of Life", dompeople.SeriesCreditCast, ptr("Silva"), nil),
	}

	syncedAt := time.Date(2026, 6, 10, 3, 14, 0, 0, time.UTC)

	return Deps{
		People: &fakePeopleReader{
			byTMDB: map[int]dompeople.Person{4495: person},
			byID:   map[int64]dompeople.Person{1: person},
		},
		PersonCredits: &fakePersonCredits{rows: credits},
		SeriesByTMDB: &fakeSeriesByTMDB{
			rows: map[int]series.Canon{
				100: louCanon,
				200: gotCanon,
			},
		},
		SeriesCache: &fakeSeriesCache{
			rows: map[int64][]series.CacheEntry{
				42: {mkCacheRow("alpha", 42)},
				43: {mkCacheRow("alpha", 43), mkCacheRow("4k", 43)},
			},
		},
		SyncLog: &fakeSyncLog{
			row: domenrich.SyncLog{
				EntityType: domenrich.EntityTypePerson,
				EntityID:   1,
				Source:     domenrich.SourceTMDBPerson,
				SyncedAt:   &syncedAt,
				Outcome:    domenrich.OutcomeOK,
			},
		},
		Enqueuer: &fakeEnqueuer{},
		Logger:   discardLogger(),
		Now:      func() time.Time { return time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC) },
	}
}

// --- tests ---

func TestUseCase_HappyPath_SortRecent(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	uc := NewUseCase(deps)
	out, err := uc.Get(context.Background(), 4495, "en-US", "recent")
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Len(t, out.LibraryCredits, 2, "LoU + GoT")
	// other = Narcos + movie
	assert.Len(t, out.OtherCredits, 2)
	assert.Empty(t, out.Degraded)
	require.NotNil(t, out.Sync)
	assert.Equal(t, domenrich.SourceTMDBPerson, out.Sync.Source)
	assert.Equal(t, "en-US", out.BioLanguage)
	assert.Equal(t, "Chilean-American actor...", out.Biography)
	// recent: LoU 2026 first, then GoT 2019
	assert.Equal(t, "The Last of Us", out.LibraryCredits[0].Canon.Title)
	assert.Equal(t, "Game of Thrones", out.LibraryCredits[1].Canon.Title)
}

func TestUseCase_SortEpisodes(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	uc := NewUseCase(deps)
	out, err := uc.Get(context.Background(), 4495, "", "episodes")
	require.NoError(t, err)
	require.Len(t, out.LibraryCredits, 2)
	// LoU 9ep, GoT 3ep — DESC by episodes
	assert.Equal(t, "The Last of Us", out.LibraryCredits[0].Canon.Title)
	assert.Equal(t, "Game of Thrones", out.LibraryCredits[1].Canon.Title)
}

func TestUseCase_SortTitle(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	uc := NewUseCase(deps)
	out, err := uc.Get(context.Background(), 4495, "", "title")
	require.NoError(t, err)
	require.Len(t, out.LibraryCredits, 2)
	// title ASC, case-insensitive: Game of Thrones, then The Last of Us
	assert.Equal(t, "Game of Thrones", out.LibraryCredits[0].Canon.Title)
	assert.Equal(t, "The Last of Us", out.LibraryCredits[1].Canon.Title)
}

func TestUseCase_SortUnknownDefaultsToRecent(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "foo"} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			deps := happyFixture(t)
			uc := NewUseCase(deps)
			out, err := uc.Get(context.Background(), 4495, "", raw)
			require.NoError(t, err)
			require.Len(t, out.LibraryCredits, 2)
			// same as recent: LoU 2026 first
			assert.Equal(t, "The Last of Us", out.LibraryCredits[0].Canon.Title)
		})
	}
}

func TestUseCase_DisjointProperty(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	uc := NewUseCase(deps)
	out, err := uc.Get(context.Background(), 4495, "", "")
	require.NoError(t, err)
	// 4 credits total: 2 lib + 2 other = 4
	totalIn := 4
	assert.Equal(t, totalIn, len(out.LibraryCredits)+len(out.OtherCredits))
	// disjoint by credit ID
	seen := map[int64]int{}
	for _, lc := range out.LibraryCredits {
		seen[lc.Credit.ID]++
	}
	for _, oc := range out.OtherCredits {
		seen[oc.Credit.ID]++
	}
	for id, c := range seen {
		assert.Equal(t, 1, c, "credit id=%d appeared %d times", id, c)
	}
}

func TestUseCase_BioFallback(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	// Override the person to simulate the §5.6 fallback firing —
	// repository returns en-US even though we asked for ru-RU.
	pr := deps.People.(*fakePeopleReader)
	p := pr.byID[1]
	p.Biography = "English bio"
	p.BiographyLanguage = "en-US"
	pr.byID[1] = p
	pr.byTMDB[4495] = p

	uc := NewUseCase(deps)
	out, err := uc.Get(context.Background(), 4495, "ru-RU", "")
	require.NoError(t, err)
	assert.Equal(t, "English bio", out.Biography)
	assert.Equal(t, "en-US", out.BioLanguage)
}

func TestUseCase_SyncLineMissing(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	deps.SyncLog = &fakeSyncLog{err: ports.ErrNotFound}
	uc := NewUseCase(deps)
	out, err := uc.Get(context.Background(), 4495, "", "")
	require.NoError(t, err)
	assert.Nil(t, out.Sync)
	assert.Contains(t, out.Degraded, domenrich.SourceTMDBPerson)
}

func TestUseCase_SyncErrorOutcomeDegrades(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	syncedAt := time.Date(2026, 6, 10, 3, 14, 0, 0, time.UTC)
	deps.SyncLog = &fakeSyncLog{
		row: domenrich.SyncLog{
			EntityType: domenrich.EntityTypePerson,
			EntityID:   1,
			Source:     domenrich.SourceTMDBPerson,
			SyncedAt:   &syncedAt,
			Outcome:    domenrich.OutcomeError,
		},
	}
	uc := NewUseCase(deps)
	out, err := uc.Get(context.Background(), 4495, "", "")
	require.NoError(t, err)
	require.NotNil(t, out.Sync)
	assert.Contains(t, out.Degraded, domenrich.SourceTMDBPerson)
}

func TestUseCase_StubPerson_EnqueueAndDegraded(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	// Flip to stub.
	pr := deps.People.(*fakePeopleReader)
	p := pr.byID[1]
	p.Hydration = dompeople.HydrationStub
	pr.byID[1] = p
	pr.byTMDB[4495] = p

	enq := deps.Enqueuer.(*fakeEnqueuer)
	uc := NewUseCase(deps)
	out, err := uc.Get(context.Background(), 4495, "", "")
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Contains(t, out.Degraded, domenrich.SourceTMDBPerson)
	require.Len(t, enq.calls, 1, "Enqueue called exactly once for stub")
	assert.Equal(t, appenrich.EntityPerson, enq.calls[0].Kind)
	assert.Equal(t, int64(1), enq.calls[0].ID)
	assert.Equal(t, appenrich.PriorityHot, enq.calls[0].P)
	// Library classification still runs for stub.
	assert.Len(t, out.LibraryCredits, 2)
}

func TestUseCase_FullHydration_DoesNotEnqueue(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	enq := deps.Enqueuer.(*fakeEnqueuer)
	uc := NewUseCase(deps)
	_, err := uc.Get(context.Background(), 4495, "", "")
	require.NoError(t, err)
	assert.Empty(t, enq.calls, "Full person never triggers an enqueue")
}

func TestUseCase_NilEnqueuer_StubStillReturns200(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	pr := deps.People.(*fakePeopleReader)
	p := pr.byID[1]
	p.Hydration = dompeople.HydrationStub
	pr.byID[1] = p
	pr.byTMDB[4495] = p
	deps.Enqueuer = nil // cold boot path

	uc := NewUseCase(deps)
	out, err := uc.Get(context.Background(), 4495, "", "")
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Contains(t, out.Degraded, domenrich.SourceTMDBPerson)
}

func TestUseCase_UnknownTMDBID_PropagatesNotFound(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	uc := NewUseCase(deps)
	_, err := uc.Get(context.Background(), 9999, "", "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestUseCase_InvalidTMDBID_NotFound(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	uc := NewUseCase(deps)
	for _, raw := range []int{0, -5} {
		_, err := uc.Get(context.Background(), raw, "", "")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ports.ErrNotFound))
	}
}

func TestUseCase_CanonLookupHiccupNonFatal(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	// Inject a transient error for GoT only — the credit lands in
	// other_credits instead of library_credits; no 5xx.
	deps.SeriesByTMDB = &fakeSeriesByTMDB{
		rows: map[int]series.Canon{
			100: mkCanon(42, 100, "The Last of Us", 2023, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		},
		errs: map[int]error{
			200: errors.New("transient db error"),
		},
	}
	uc := NewUseCase(deps)
	out, err := uc.Get(context.Background(), 4495, "", "")
	require.NoError(t, err)
	// LoU stays library; GoT lands in other; Narcos in other; movie in other = 1 lib, 3 other
	assert.Len(t, out.LibraryCredits, 1)
	assert.Len(t, out.OtherCredits, 3)
}

func TestUseCase_InstanceDedup(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	// Duplicate cache rows under same instance — adapter must dedup.
	deps.SeriesCache = &fakeSeriesCache{
		rows: map[int64][]series.CacheEntry{
			42: {
				mkCacheRow("alpha", 42),
				mkCacheRow("alpha", 42), // duplicate
				mkCacheRow("4k", 42),
			},
			43: {mkCacheRow("alpha", 43)},
		},
	}
	uc := NewUseCase(deps)
	out, err := uc.Get(context.Background(), 4495, "", "title")
	require.NoError(t, err)
	require.Len(t, out.LibraryCredits, 2)
	var louEntry *LibraryCredit
	for i := range out.LibraryCredits {
		if out.LibraryCredits[i].Canon.Title == "The Last of Us" {
			louEntry = &out.LibraryCredits[i]
			break
		}
	}
	require.NotNil(t, louEntry)
	assert.Equal(t, []string{"4k", "alpha"}, louEntry.Instances, "deduped and sorted")
	// Verify the sort.Strings invariant: instances must be sorted.
	require.True(t, sort.StringsAreSorted(louEntry.Instances))
}

func TestUseCase_SortRecent_NilsLast(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	// Swap GoT canon to have nil LastAirDate.
	sb := deps.SeriesByTMDB.(*fakeSeriesByTMDB)
	gotCanon := sb.rows[200]
	gotCanon.LastAirDate = nil
	sb.rows[200] = gotCanon

	uc := NewUseCase(deps)
	out, err := uc.Get(context.Background(), 4495, "", "recent")
	require.NoError(t, err)
	require.Len(t, out.LibraryCredits, 2)
	// LoU (non-nil) first, GoT (nil) last
	assert.Equal(t, "The Last of Us", out.LibraryCredits[0].Canon.Title)
	assert.Equal(t, "Game of Thrones", out.LibraryCredits[1].Canon.Title)
}

func TestUseCase_SortEpisodes_NilsLast(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	// Make GoT's credit have nil EpisodeCount.
	pc := deps.PersonCredits.(*fakePersonCredits)
	for i := range pc.rows {
		if pc.rows[i].Title == "Game of Thrones" {
			pc.rows[i].EpisodeCount = nil
		}
	}
	uc := NewUseCase(deps)
	out, err := uc.Get(context.Background(), 4495, "", "episodes")
	require.NoError(t, err)
	require.Len(t, out.LibraryCredits, 2)
	// LoU (9 ep) first, GoT (nil) last
	assert.Equal(t, "The Last of Us", out.LibraryCredits[0].Canon.Title)
	assert.Equal(t, "Game of Thrones", out.LibraryCredits[1].Canon.Title)
}

func TestUseCase_PersonExistsZeroCredits(t *testing.T) {
	t.Parallel()
	deps := happyFixture(t)
	deps.PersonCredits = &fakePersonCredits{rows: nil}
	uc := NewUseCase(deps)
	out, err := uc.Get(context.Background(), 4495, "", "")
	require.NoError(t, err)
	assert.Empty(t, out.LibraryCredits)
	assert.Empty(t, out.OtherCredits)
}

func TestUseCase_CanonExistsNoSeriesCache_IsOtherCredit(t *testing.T) {
	t.Parallel()
	// Canon row exists (stub from recommendation maybe) but no live
	// series_cache references — must land in other_credits.
	deps := happyFixture(t)
	deps.SeriesCache = &fakeSeriesCache{rows: map[int64][]series.CacheEntry{}}
	uc := NewUseCase(deps)
	out, err := uc.Get(context.Background(), 4495, "", "")
	require.NoError(t, err)
	// All 4 credits land in other_credits.
	assert.Empty(t, out.LibraryCredits)
	assert.Len(t, out.OtherCredits, 4)
}
