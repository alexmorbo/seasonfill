package scan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/release"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/grab/app/evaluate"
	"github.com/alexmorbo/seasonfill/internal/grab/domain/decision"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
)

type fakeSonarr struct {
	name           string
	series         []series.Series
	episodes       func(seriesID domain.SonarrSeriesID, season int) []series.Episode
	releases       []release.Release
	tags           []ports.Tag
	tagsErr        error
	grabErr        error
	grabCnt        int
	grabMu         sync.Mutex
	seriesCacheErr error
}

func (f *fakeSonarr) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{Version: "test"}, nil
}
func (f *fakeSonarr) ListSeries(_ context.Context) ([]series.Series, error) { return f.series, nil }
func (f *fakeSonarr) GetSeries(_ context.Context, _ domain.SonarrSeriesID) (series.Series, error) {
	return series.Series{}, nil
}
func (f *fakeSonarr) ListEpisodes(_ context.Context, sID domain.SonarrSeriesID, sn int) ([]series.Episode, error) {
	if f.episodes != nil {
		return f.episodes(sID, sn), nil
	}
	return []series.Episode{
		{Number: 1, SeasonNumber: sn, Monitored: true, HasFile: true, QualityID: 19, QualityName: "WEBDL-2160p"},
		{Number: 2, SeasonNumber: sn, Monitored: true, HasFile: true, QualityID: 19, QualityName: "WEBDL-2160p"},
		{Number: 3, SeasonNumber: sn, Monitored: true, HasFile: false},
	}, nil
}

func (f *fakeSonarr) ListEpisodesBySeries(_ context.Context, _ domain.SonarrSeriesID) ([]series.Episode, error) {
	return nil, nil
}
func (f *fakeSonarr) ListEpisodeFiles(_ context.Context, _ domain.SonarrSeriesID) (map[int]int, error) {
	return map[int]int{}, nil
}
func (f *fakeSonarr) ListEpisodeFilesBySeason(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]ports.EpisodeFileDetail, error) {
	return nil, nil
}
func (f *fakeSonarr) SearchReleases(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]release.Release, error) {
	if len(f.releases) > 0 {
		return f.releases, nil
	}
	return []release.Release{
		{
			GUID:                 "g1",
			Title:                "S02 pack",
			IndexerID:            7,
			QualityID:            19,
			QualityName:          "WEBDL-2160p",
			IndexerName:          "RT",
			MappedEpisodeNumbers: []int{1, 2, 3},
			CustomFormatScore:    500,
			Rejections:           []string{"Full season pack"},
		},
	}, nil
}
func (f *fakeSonarr) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{
		ID:   14,
		Name: "Any",
		Items: []ports.QualityItem{
			{ID: 19, Name: "WEBDL-2160p", Order: 9},
		},
	}, nil
}
func (f *fakeSonarr) ListIndexers(_ context.Context) ([]ports.Indexer, error) { return nil, nil }
func (f *fakeSonarr) ListTags(_ context.Context) ([]ports.Tag, error) {
	return f.tags, f.tagsErr
}
func (f *fakeSonarr) ListQualityProfiles(_ context.Context) ([]ports.QualityProfile, error) {
	return nil, nil
}
func (f *fakeSonarr) ListRootFolders(_ context.Context) ([]ports.RootFolder, error) { return nil, nil }
func (f *fakeSonarr) LookupSeries(_ context.Context, _ string) ([]ports.SonarrLookupResult, error) {
	return nil, nil
}
func (f *fakeSonarr) CreateTag(_ context.Context, _ string) (ports.Tag, error) {
	return ports.Tag{}, nil
}
func (f *fakeSonarr) AddSeries(_ context.Context, _ ports.AddSeriesPayload) (ports.AddSeriesResult, error) {
	return ports.AddSeriesResult{}, nil
}
func (f *fakeSonarr) GrabHistory(_ context.Context, _ domain.SonarrSeriesID) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (f *fakeSonarr) ParseRelease(_ context.Context, _ string) (ports.ParseResult, error) {
	return ports.ParseResult{}, nil
}
func (f *fakeSonarr) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	f.grabMu.Lock()
	defer f.grabMu.Unlock()
	f.grabCnt++
	return "", f.grabErr
}
func (f *fakeSonarr) ListSeriesCache(_ context.Context, instanceName domain.InstanceName) ([]series.CacheEntry, error) {
	if f.seriesCacheErr != nil {
		return nil, f.seriesCacheErr
	}
	out := make([]series.CacheEntry, 0, len(f.series))
	for _, s := range f.series {
		out = append(out, series.CacheEntry{
			InstanceName:   instanceName,
			SonarrSeriesID: s.ID,
			Title:          s.Title,
			TitleSlug:      "slug-" + s.Title,
			Monitored:      s.Monitored,
		})
	}
	return out, nil
}
func (f *fakeSonarr) Name() string { return f.name }

type fakeScanRepo struct {
	mu      sync.Mutex
	created []ports.ScanRecord
	updated []ports.ScanRecord
}

func (r *fakeScanRepo) Create(_ context.Context, rec ports.ScanRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.created = append(r.created, rec)
	return nil
}
func (r *fakeScanRepo) Update(_ context.Context, rec ports.ScanRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updated = append(r.updated, rec)
	return nil
}
func (r *fakeScanRepo) GetByID(_ context.Context, id uuid.UUID) (ports.ScanRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.updated {
		if rec.ID == id {
			return rec, nil
		}
	}
	for _, rec := range r.created {
		if rec.ID == id {
			return rec, nil
		}
	}
	return ports.ScanRecord{}, errors.New("not found")
}
func (r *fakeScanRepo) MarkAborted(_ context.Context, id uuid.UUID, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.updated {
		if r.updated[i].ID == id {
			r.updated[i].Status = "aborted"
			r.updated[i].ErrorMessage = reason
		}
	}
	return nil
}

func (r *fakeScanRepo) IncrementSeriesScanned(_ context.Context, id uuid.UUID, by int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Mirror real repo semantics: mutate the most recent matching row in
	// `updated` (or `created` as a fallback) so test assertions see the
	// running counter, not just the final Update snapshot.
	for _, v := range slices.Backward(r.updated) {
		if v.ID == id {
			v.SeriesScanned += by
			return nil
		}
	}
	for _, v := range slices.Backward(r.created) {
		if v.ID == id {
			v.SeriesScanned += by
			// Promote to updated so subsequent GetByID lookups read the new value.
			r.updated = append(r.updated, v)
			return nil
		}
	}
	return ports.ErrNotFound
}

func (r *fakeScanRepo) List(_ context.Context, _ ports.ScanFilter, _ ports.Pagination) ([]ports.ScanRecord, *ports.Cursor, error) {
	panic("fake List unexpectedly called - this stub is not configured for List queries")
}

type fakeDecRepo struct {
	mu sync.Mutex
	d  []decision.Decision
}

func (r *fakeDecRepo) Save(_ context.Context, d decision.Decision) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.d = append(r.d, d)
	return nil
}

func (r *fakeDecRepo) GetByID(_ context.Context, _ uuid.UUID) (decision.Decision, error) {
	return decision.Decision{}, ports.ErrNotFound
}

func (r *fakeDecRepo) List(_ context.Context, _ ports.DecisionFilter, _ ports.Pagination) ([]decision.Decision, *ports.Cursor, error) {
	panic("fake List unexpectedly called - this stub is not configured for List queries")
}

func (r *fakeDecRepo) UpdateSupersededBy(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (r *fakeDecRepo) ClearSupersededBy(context.Context, uuid.UUID) error {
	return nil
}

func (r *fakeDecRepo) UpdateIntent(context.Context, uuid.UUID, *decision.Intent) error {
	return nil
}

type fakeCDRepo struct {
	mu     sync.Mutex
	sets   []cooldown.Cooldown
	active map[string]bool // key = scope + ":" + key
}

func (r *fakeCDRepo) Set(_ context.Context, c cooldown.Cooldown) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sets = append(r.sets, c)
	if r.active == nil {
		r.active = make(map[string]bool)
	}
	r.active[string(c.Scope)+":"+c.Key] = true
	return nil
}
func (r *fakeCDRepo) Get(_ context.Context, _ cooldown.Scope, _ string) (cooldown.Cooldown, bool, error) {
	return cooldown.Cooldown{}, false, nil
}
func (r *fakeCDRepo) FilterActive(_ context.Context, scope cooldown.Scope, keys []string, _ time.Time) ([]cooldown.Cooldown, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []cooldown.Cooldown
	for _, k := range keys {
		if r.active[string(scope)+":"+k] {
			out = append(out, cooldown.Cooldown{Scope: scope, Key: k})
		}
	}
	return out, nil
}
func (r *fakeCDRepo) Sweep(_ context.Context, _ time.Time) (int64, error) { return 0, nil }

func makeUseCase(t *testing.T) (*UseCase, *fakeScanRepo, *fakeDecRepo) {
	t.Helper()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sonarr := &fakeSonarr{
		name: "main",
		series: []series.Series{
			{
				ID: 122, Title: "Hijack", Type: series.SeriesTypeStandard, Monitored: true, QualityProfile: 14,
				Seasons: []series.Season{{Number: 2, Monitored: true}},
			},
		},
	}
	decRepo := &fakeDecRepo{}
	evalUC := evaluate.NewUseCase(sonarr, decRepo, lg)
	scanRepo := &fakeScanRepo{}
	uc := NewUseCase([]Instance{{
		Config: config.SonarrInstance{
			Name: "main",
			Search: config.SearchConfig{
				SkipSpecials: true,
				SkipAnime:    true,
			},
			Limits:  config.LimitsConfig{ScanMaxSeries: 100, MaxGrabsPerScan: 10},
			Ranking: config.RankingConfig{OriginBonus: 1.0},
			Retry:   config.RetryConfig{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
			Cooldown: config.CooldownConfig{
				Mode:                "smart",
				SeriesAfterGrab:     24 * time.Hour,
				GUIDAfterFailedGrab: 72 * time.Hour,
			},
		},
		Client: sonarr,
	}}, evalUC, scanRepo, lg, true)
	return uc, scanRepo, decRepo
}

func TestScan_RunSuccess(t *testing.T) {
	uc, scanRepo, decRepo := makeUseCase(t)
	results, err := uc.Run(context.Background(), TriggerManual)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "completed", results[0].Status)
	assert.Equal(t, 1, results[0].Series)
	assert.NotEmpty(t, scanRepo.created)
	assert.NotEmpty(t, scanRepo.updated)
	assert.NotEmpty(t, decRepo.d)
}

func TestBuildTagFilter(t *testing.T) {
	ctx := context.Background()
	t.Run("empty config returns empty filter", func(t *testing.T) {
		inst := Instance{
			Config: config.SonarrInstance{Name: "x"},
			Client: &fakeSonarr{name: "x"},
		}
		f, err := buildTagFilter(ctx, inst)
		require.NoError(t, err)
		assert.Empty(t, f.include)
		assert.Empty(t, f.exclude)
	})

	t.Run("resolves labels to ids", func(t *testing.T) {
		inst := Instance{
			Config: config.SonarrInstance{
				Name: "x",
				Tags: config.TagsConfig{
					Mode:    "any",
					Include: []string{"keep"},
					Exclude: []string{"skip"},
				},
			},
			Client: &fakeSonarr{
				name: "x",
				tags: []ports.Tag{{ID: 1, Label: "keep"}, {ID: 2, Label: "skip"}},
			},
		}
		f, err := buildTagFilter(ctx, inst)
		require.NoError(t, err)
		_, hasKeep := f.include[1]
		_, hasSkip := f.exclude[2]
		assert.True(t, hasKeep)
		assert.True(t, hasSkip)
	})

	t.Run("all include labels unresolved errors fail-closed", func(t *testing.T) {
		inst := Instance{
			Config: config.SonarrInstance{
				Name: "x",
				Tags: config.TagsConfig{Include: []string{"missing"}},
			},
			Client: &fakeSonarr{name: "x", tags: []ports.Tag{{ID: 1, Label: "keep"}}},
		}
		_, err := buildTagFilter(ctx, inst)
		require.Error(t, err, "M-6: all include labels unresolved must fail-closed")
		assert.Contains(t, err.Error(), "no labels matched")
	})

	t.Run("partial include match still resolves (one label hits)", func(t *testing.T) {
		inst := Instance{
			Config: config.SonarrInstance{
				Name: "x",
				Tags: config.TagsConfig{Include: []string{"keep", "missing"}},
			},
			Client: &fakeSonarr{name: "x", tags: []ports.Tag{{ID: 1, Label: "keep"}}},
		}
		f, err := buildTagFilter(ctx, inst)
		require.NoError(t, err)
		_, hasKeep := f.include[1]
		assert.True(t, hasKeep)
		assert.Len(t, f.include, 1, "only the matched label is included")
	})

	t.Run("exclude only with unresolved label is fail-open", func(t *testing.T) {
		inst := Instance{
			Config: config.SonarrInstance{
				Name: "x",
				Tags: config.TagsConfig{Exclude: []string{"ghost"}},
			},
			Client: &fakeSonarr{name: "x", tags: []ports.Tag{{ID: 1, Label: "keep"}}},
		}
		f, err := buildTagFilter(ctx, inst)
		require.NoError(t, err)
		assert.Empty(t, f.exclude, "unresolved exclude is harmless — can't expand scope")
	})

	t.Run("empty sonarr tag list with non-empty include errors fail-closed", func(t *testing.T) {
		inst := Instance{
			Config: config.SonarrInstance{
				Name: "x",
				Tags: config.TagsConfig{Include: []string{"any"}},
			},
			Client: &fakeSonarr{name: "x", tags: nil},
		}
		_, err := buildTagFilter(ctx, inst)
		require.Error(t, err)
	})

	t.Run("propagates list tags error", func(t *testing.T) {
		inst := Instance{
			Config: config.SonarrInstance{
				Name: "x",
				Tags: config.TagsConfig{Include: []string{"any"}},
			},
			Client: &fakeSonarr{name: "x", tagsErr: errors.New("boom")},
		}
		_, err := buildTagFilter(ctx, inst)
		require.Error(t, err)
	})
}

type listSeriesCounter struct {
	*fakeSonarr
	mu    sync.Mutex
	calls int
}

func (l *listSeriesCounter) ListSeries(ctx context.Context) ([]series.Series, error) {
	l.mu.Lock()
	l.calls++
	l.mu.Unlock()
	return l.fakeSonarr.ListSeries(ctx)
}

func monSeries(id domain.SonarrSeriesID, title string) series.Series {
	// Partial-pack stats (Aired=10 > Existing=3) keep the series out of
	// the all-complete fast-path so the rest of the scan pipeline runs.
	return series.Series{ID: id, Title: title, Type: series.SeriesTypeStandard, Monitored: true,
		QualityProfile: 14,
		Seasons: []series.Season{{
			Number: 1, Monitored: true,
			Statistics: series.Statistics{Total: 10, Aired: 10, EpisodeFileCount: 3},
		}},
	}
}

func instCfg(name, mode string) config.SonarrInstance {
	return config.SonarrInstance{Name: name, Mode: mode,
		Limits: config.LimitsConfig{ScanMaxSeries: 100, MaxGrabsPerScan: 10}}
}

// newScanUCFor builds a NewUseCase for the given instances + logger.
// Always wires fresh fakeDecRepo + fakeScanRepo; first client backs
// the evaluator (sufficient because tests never cross instances).
func newScanUCFor(t *testing.T, lg *slog.Logger, insts []Instance) *UseCase {
	t.Helper()
	require.NotEmpty(t, insts)
	evalUC := evaluate.NewUseCase(insts[0].Client, &fakeDecRepo{}, lg)
	return NewUseCase(insts, evalUC, &fakeScanRepo{}, lg, true)
}

func TestScan_CronSkipsManualInstance(t *testing.T) {
	t.Parallel()
	autoWrap := &listSeriesCounter{fakeSonarr: &fakeSonarr{name: "auto1", series: []series.Series{monSeries(1, "A")}}}
	manualWrap := &listSeriesCounter{fakeSonarr: &fakeSonarr{name: "manual1", series: []series.Series{monSeries(2, "B")}}}
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	uc := newScanUCFor(t, lg, []Instance{
		{Config: instCfg("auto1", "auto"), Client: autoWrap},
		{Config: instCfg("manual1", "manual"), Client: manualWrap},
	})

	results, err := uc.Run(context.Background(), TriggerCron)
	require.NoError(t, err)
	require.Len(t, results, 1, "cron must skip the manual instance")
	assert.Equal(t, domain.InstanceName("auto1"), results[0].InstanceName)
	assert.Equal(t, 1, autoWrap.calls)
	assert.Equal(t, 0, manualWrap.calls, "manual instance must be skipped on cron")
}

func TestScan_ManualTriggerHitsManualInstance(t *testing.T) {
	t.Parallel()
	wrap := &listSeriesCounter{fakeSonarr: &fakeSonarr{name: "manual1", series: []series.Series{monSeries(2, "B")}}}
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	uc := newScanUCFor(t, lg, []Instance{{Config: instCfg("manual1", "manual"), Client: wrap}})

	res, err := uc.RunInstance(context.Background(), "manual1", TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, "completed", res.Status)
	assert.Equal(t, 1, wrap.calls)
}

func TestScan_RespectsSeriesIDsFilter(t *testing.T) {
	t.Parallel()
	sn := &fakeSonarr{name: "main", series: []series.Series{
		monSeries(1, "Keep"), monSeries(2, "Drop"), monSeries(3, "AlsoKeep")}}
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	uc := newScanUCFor(t, lg, []Instance{{Config: instCfg("main", "auto"), Client: sn}})

	res, err := uc.RunInstance(context.Background(), "main", TriggerManual, 1, 3)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Series, "filter narrows ListSeries to two series")
}

func TestScan_SeriesIDsFilter_UnknownIDsSkippedWithWarn(t *testing.T) {
	t.Parallel()
	sn := &fakeSonarr{name: "main", series: []series.Series{monSeries(1, "Known")}}
	var buf strings.Builder
	lg := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	uc := newScanUCFor(t, lg, []Instance{{Config: instCfg("main", "auto"), Client: sn}})

	res, err := uc.RunInstance(context.Background(), "main", TriggerManual, 1, 999)
	require.NoError(t, err, "unknown IDs must not block the scan")
	assert.Equal(t, "completed", res.Status)
	assert.Equal(t, 1, res.Series)
	assert.Contains(t, buf.String(), "scan_series_ids_unknown_skipped")
	assert.Contains(t, buf.String(), "999")
}

func TestDominantIndexer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		history []ports.HistoryEvent
		want    string
	}{
		{name: "empty returns empty", history: nil, want: ""},
		{name: "single record", history: []ports.HistoryEvent{{IndexerName: "RT"}}, want: "RT"},
		{
			name: "majority wins",
			history: []ports.HistoryEvent{
				{IndexerName: "RT"},
				{IndexerName: "RT"},
				{IndexerName: "KZ"},
			},
			want: "RT",
		},
		{
			name: "blank indexer skipped",
			history: []ports.HistoryEvent{
				{IndexerName: ""},
				{IndexerName: "KZ"},
			},
			want: "KZ",
		},
		{
			name: "tie broken by name ascending",
			history: []ports.HistoryEvent{
				{IndexerName: "Zeta"},
				{IndexerName: "Alpha"},
			},
			want: "Alpha",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := dominantIndexer(tt.history)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTagFilter_Skip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mode    string
		include []int
		exclude []int
		tags    []int
		skip    bool
	}{
		{name: "no filter passes", tags: []int{1, 2}, skip: false},
		{name: "exclude hit skips", exclude: []int{1}, tags: []int{1, 2}, skip: true},
		{name: "exclude miss passes", exclude: []int{99}, tags: []int{1, 2}, skip: false},
		{name: "include any hit passes", mode: "any", include: []int{1, 5}, tags: []int{1}, skip: false},
		{name: "include any miss skips", mode: "any", include: []int{5, 6}, tags: []int{1}, skip: true},
		{name: "include all subset skips", mode: "all", include: []int{1, 2, 3}, tags: []int{1, 2}, skip: true},
		{name: "include all match passes", mode: "all", include: []int{1, 2}, tags: []int{1, 2, 3}, skip: false},
		{name: "exclude beats include", mode: "any", include: []int{1}, exclude: []int{2}, tags: []int{1, 2}, skip: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := tagFilter{mode: tt.mode}
			if len(tt.include) > 0 {
				f.include = make(map[int]struct{}, len(tt.include))
				for _, id := range tt.include {
					f.include[id] = struct{}{}
				}
			}
			if len(tt.exclude) > 0 {
				f.exclude = make(map[int]struct{}, len(tt.exclude))
				for _, id := range tt.exclude {
					f.exclude[id] = struct{}{}
				}
			}
			_, skipped := f.skip(tt.tags)
			assert.Equal(t, tt.skip, skipped)
		})
	}
}

// barrier parks the first goroutine inside runOne (after acquire, while
// holding the inflight slot) until the test calls Release. This guarantees
// goroutine 2's acquire races against a held slot and deterministically
// returns ErrScanAlreadyRunning regardless of scheduler timing or coverage
// instrumentation overhead.
type barrier struct {
	mu      sync.Mutex
	entered bool
	enterCh chan struct{}
	leaveCh chan struct{}
}

func newBarrier() *barrier {
	return &barrier{enterCh: make(chan struct{}), leaveCh: make(chan struct{})}
}

func (b *barrier) Reached(_ domain.InstanceName) {
	b.mu.Lock()
	first := !b.entered
	b.entered = true
	b.mu.Unlock()
	if first {
		close(b.enterCh)
		<-b.leaveCh
	}
}

func (b *barrier) WaitForFirstEntry() { <-b.enterCh }
func (b *barrier) Release()           { close(b.leaveCh) }

func TestScan_ConcurrentSameInstanceReturnsConflict(t *testing.T) {
	uc, _, _ := makeUseCase(t)
	bar := newBarrier()
	uc.WithBarrier(bar)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var (
		err1, err2 error
		wg         sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err1 = uc.RunInstance(ctx, "main", TriggerCron)
	}()
	bar.WaitForFirstEntry()

	g2Done := make(chan struct{})
	go func() {
		defer wg.Done()
		defer close(g2Done)
		_, err2 = uc.RunInstance(ctx, "main", TriggerManual)
	}()
	<-g2Done
	bar.Release()
	wg.Wait()

	gotConflict := errors.Is(err1, ErrScanAlreadyRunning) || errors.Is(err2, ErrScanAlreadyRunning)
	gotSuccess := err1 == nil || err2 == nil
	assert.True(t, gotConflict, "expected one call to fail with ErrScanAlreadyRunning")
	assert.True(t, gotSuccess, "expected the other call to succeed")
}

func TestScan_TagFilterFailClosed_WhenIncludeSet(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sonarr := &fakeSonarr{
		name:    "main",
		tagsErr: errors.New("boom"),
		series: []series.Series{
			{ID: 1, Title: "X", Type: series.SeriesTypeStandard, Monitored: true,
				Seasons: []series.Season{{Number: 1, Monitored: true}}},
		},
	}
	decRepo := &fakeDecRepo{}
	evalUC := evaluate.NewUseCase(sonarr, decRepo, lg)
	uc := NewUseCase([]Instance{{
		Config: config.SonarrInstance{
			Name:   "main",
			Tags:   config.TagsConfig{Include: []string{"seasonfill"}},
			Limits: config.LimitsConfig{ScanMaxSeries: 10},
		},
		Client: sonarr,
	}}, evalUC, &fakeScanRepo{}, lg, true)

	res, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.Error(t, err)
	assert.Equal(t, "failed", res.Status)
}

func TestScan_TagFilterFailOpen_WhenIncludeEmpty(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sonarr := &fakeSonarr{
		name:    "main",
		tagsErr: errors.New("boom"),
		series: []series.Series{
			{ID: 1, Title: "X", Type: series.SeriesTypeStandard, Monitored: true, QualityProfile: 14,
				Seasons: []series.Season{{Number: 1, Monitored: true}}},
		},
	}
	decRepo := &fakeDecRepo{}
	evalUC := evaluate.NewUseCase(sonarr, decRepo, lg)
	uc := NewUseCase([]Instance{{
		Config: config.SonarrInstance{
			Name:   "main",
			Tags:   config.TagsConfig{Exclude: []string{"skip-me"}},
			Limits: config.LimitsConfig{ScanMaxSeries: 10},
		},
		Client: sonarr,
	}}, evalUC, &fakeScanRepo{}, lg, true)

	res, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, "completed", res.Status)
}

func TestScan_SeriesCooldown_Skips(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sonarr := &fakeSonarr{
		name: "main",
		series: []series.Series{
			{ID: 122, Title: "Hijack", Type: series.SeriesTypeStandard, Monitored: true, QualityProfile: 14,
				Seasons: []series.Season{{
					Number:    2,
					Monitored: true,
					Statistics: series.Statistics{
						EpisodeCount:     10,
						EpisodeFileCount: 8,
						Total:            10,
						Aired:            10,
					},
				}}},
		},
	}
	decRepo := &fakeDecRepo{}
	evalUC := evaluate.NewUseCase(sonarr, decRepo, lg)

	cdRepo := &fakeCDRepo{}
	_ = cdRepo.Set(context.Background(), cooldown.Cooldown{
		Scope:     cooldown.ScopeSeries,
		Key:       cooldown.SeriesKey("main", 122, 2),
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	})

	uc := NewUseCase([]Instance{{
		Config: config.SonarrInstance{
			Name:   "main",
			Limits: config.LimitsConfig{ScanMaxSeries: 10},
		},
		Client: sonarr,
	}}, evalUC, &fakeScanRepo{}, lg, true).WithCooldowns(cdRepo)

	res, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, "completed", res.Status)
	// Decision NOT persisted via evaluator for a cooldowned season — scan loop skips.
	assert.Empty(t, decRepo.d, "no evaluator decision should be persisted for a cooldowned season")
}

func TestInstanceDryRun_OverrideWins(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	uc := NewUseCase(nil, nil, &fakeScanRepo{}, lg, true) // global dry-run=true

	// Instance override = false: real grab for this instance only.
	inst := Instance{Config: config.SonarrInstance{Name: "x", DryRun: new(false)}}
	assert.False(t, uc.instanceDryRun(inst, nil))

	// No instance override, no request override: inherits global dry-run=true.
	inst2 := Instance{Config: config.SonarrInstance{Name: "x"}}
	assert.True(t, uc.instanceDryRun(inst2, nil))

	// Explicit instance override = true is also honored.
	inst3 := Instance{Config: config.SonarrInstance{Name: "x", DryRun: new(true)}}
	assert.True(t, uc.instanceDryRun(inst3, nil))

	// Request override = true beats instance=false.
	assert.True(t, uc.instanceDryRun(inst, new(true)))

	// Request override = false beats instance=true (the "Force real grab"
	// path from 033c). This is the load-bearing case for the new feature.
	assert.False(t, uc.instanceDryRun(inst3, new(false)))

	// Request override = false beats global=true with no instance setting.
	assert.False(t, uc.instanceDryRun(inst2, new(false)))
}

// TestScanUseCase_SwapDryRun_LiveOverride verifies that SwapDryRun
// atomically replaces the global dry-run default consulted by
// instanceDryRun when neither a per-call override nor a per-instance
// Config.DryRun is set — i.e. that toggling global dry_run via the
// runtime config UI takes effect without a process restart.
func TestScanUseCase_SwapDryRun_LiveOverride(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	uc := NewUseCase(nil, nil, &fakeScanRepo{}, lg, false) // global dry-run=false

	inst := Instance{Config: config.SonarrInstance{Name: "x"}} // no per-instance setting

	// Initial: neither override nor per-instance Config.DryRun -> global=false.
	assert.False(t, uc.instanceDryRun(inst, nil),
		"initial global dryRun=false must fall through to false")

	// Swap global default to true (mimics runtime-config publish).
	uc.SwapDryRun(true)
	assert.True(t, uc.instanceDryRun(inst, nil),
		"after SwapDryRun(true), no override + no instance setting must return true")

	// Swap back to verify it's not a one-way switch.
	uc.SwapDryRun(false)
	assert.False(t, uc.instanceDryRun(inst, nil),
		"after SwapDryRun(false), must return false again")
}

// TestScan_TagFilter_AllIncludeLabelsUnresolved covers M-6 (fail-CLOSED) at
// the scan-loop level: when none of the configured `tags.include` labels
// resolve to any Sonarr tag ID, the scan must abort with `Status=failed`
// rather than silently expanding scope to every series.
func TestScan_TagFilter_AllIncludeLabelsUnresolved(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sonarr := &fakeSonarr{
		name: "main",
		// Sonarr returns tags, but none of them match `include`.
		tags: []ports.Tag{{ID: 1, Label: "other-label"}},
		series: []series.Series{
			{ID: 1, Title: "X", Type: series.SeriesTypeStandard, Monitored: true, QualityProfile: 14,
				Seasons: []series.Season{{Number: 1, Monitored: true}}},
		},
	}
	decRepo := &fakeDecRepo{}
	evalUC := evaluate.NewUseCase(sonarr, decRepo, lg)
	uc := NewUseCase([]Instance{{
		Config: config.SonarrInstance{
			Name:   "main",
			Tags:   config.TagsConfig{Include: []string{"typo-label"}},
			Limits: config.LimitsConfig{ScanMaxSeries: 10},
		},
		Client: sonarr,
	}}, evalUC, &fakeScanRepo{}, lg, true)

	res, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.Error(t, err)
	assert.Equal(t, "failed", res.Status)
	assert.Contains(t, err.Error(), "no labels matched")
}

// TestScan_SeriesCooldown_BatchesFilterActive — deferred-item #8.
// One series with 3 monitored seasons must produce a single
// FilterActive call that includes all three season keys.
func TestScan_SeriesCooldown_BatchesFilterActive(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sonarr := &fakeSonarr{
		name: "main",
		series: []series.Series{
			// Each season carries partial-pack stats (aired > existing) so
			// the series-level all-complete fast-path doesn't short-circuit
			// before cooldown lookup. Cooldown lookup batches all 3 keys
			// in a single FilterActive call.
			{ID: 200, Title: "Show", Type: series.SeriesTypeStandard, Monitored: true, QualityProfile: 14,
				Seasons: []series.Season{
					{Number: 1, Monitored: true, Statistics: series.Statistics{Total: 10, Aired: 10, EpisodeFileCount: 3}},
					{Number: 2, Monitored: true, Statistics: series.Statistics{Total: 10, Aired: 10, EpisodeFileCount: 3}},
					{Number: 3, Monitored: true, Statistics: series.Statistics{Total: 10, Aired: 10, EpisodeFileCount: 3}},
				}},
		},
	}
	decRepo := &fakeDecRepo{}
	evalUC := evaluate.NewUseCase(sonarr, decRepo, lg)

	cdRepo := &batchCountingCDRepo{}
	uc := NewUseCase([]Instance{{
		Config: config.SonarrInstance{
			Name:   "main",
			Limits: config.LimitsConfig{ScanMaxSeries: 10},
		},
		Client: sonarr,
	}}, evalUC, &fakeScanRepo{}, lg, true).WithCooldowns(cdRepo)

	_, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)

	cdRepo.mu.Lock()
	defer cdRepo.mu.Unlock()
	require.Equal(t, 1, cdRepo.calls, "FilterActive must be called exactly once per series for ScopeSeries")
	require.Len(t, cdRepo.lastKeys, 3, "all three season keys must be in the same batch call")
	expected := map[string]bool{
		cooldown.SeriesKey("main", 200, 1): true,
		cooldown.SeriesKey("main", 200, 2): true,
		cooldown.SeriesKey("main", 200, 3): true,
	}
	for _, k := range cdRepo.lastKeys {
		require.True(t, expected[k], "unexpected key in batch: %s", k)
	}
}

// batchCountingCDRepo records call count + the keys passed in the
// most-recent ScopeSeries FilterActive call.
type batchCountingCDRepo struct {
	mu       sync.Mutex
	calls    int
	lastKeys []string
}

func (r *batchCountingCDRepo) Set(_ context.Context, _ cooldown.Cooldown) error { return nil }
func (r *batchCountingCDRepo) Get(_ context.Context, _ cooldown.Scope, _ string) (cooldown.Cooldown, bool, error) {
	return cooldown.Cooldown{}, false, nil
}

func (r *batchCountingCDRepo) FilterActive(_ context.Context, scope cooldown.Scope, keys []string, _ time.Time) ([]cooldown.Cooldown, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if scope == cooldown.ScopeSeries {
		r.calls++
		r.lastKeys = append([]string{}, keys...)
	}
	return nil, nil
}

func (r *batchCountingCDRepo) Sweep(_ context.Context, _ time.Time) (int64, error) { return 0, nil }

func TestSwapInstances_RebuildsRunSet(t *testing.T) {
	t.Parallel()
	uc := NewUseCase(nil, nil, nil, slog.Default(), true)

	// Before swap: empty.
	require.Empty(t, uc.loadInstances())

	// Swap in two instances.
	uc.SwapInstances([]Instance{
		{Config: config.SonarrInstance{Name: "alpha"}},
		{Config: config.SonarrInstance{Name: "beta"}},
	})
	require.Len(t, uc.loadInstances(), 2)

	// Swap in a different set.
	uc.SwapInstances([]Instance{{Config: config.SonarrInstance{Name: "gamma"}}})
	got := uc.loadInstances()
	require.Len(t, got, 1)
	require.Equal(t, "gamma", got[0].Config.Name)
}

// slowSonarr blocks ListSeries until release is closed, then returns the
// series list. Used to assert StartInstance returns before completion.
type slowSonarr struct {
	*fakeSonarr
	release   chan struct{}
	listCalls atomic.Int32
}

func (s *slowSonarr) ListSeries(ctx context.Context) ([]series.Series, error) {
	s.listCalls.Add(1)
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.fakeSonarr.ListSeries(ctx)
}

func TestStartInstance_ReturnsBeforeCompletion(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sn := &slowSonarr{
		fakeSonarr: &fakeSonarr{name: "main", series: []series.Series{monSeries(1, "A")}},
		release:    make(chan struct{}),
	}
	uc := newScanUCFor(t, lg, []Instance{{Config: instCfg("main", "auto"), Client: sn}})

	deadline := time.Now().Add(500 * time.Millisecond)
	res, err := uc.StartInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)
	assert.Less(t, time.Since(deadline), 500*time.Millisecond, "StartInstance must return synchronously")
	assert.Equal(t, "running", res.Status)
	assert.NotEqual(t, uuid.Nil, res.ScanRunID)

	close(sn.release)
	// Wait for the async goroutine to finish so test cleanup is deterministic.
	require.Eventually(t, func() bool { return !uc.IsAnyRunning() },
		2*time.Second, 10*time.Millisecond, "scan goroutine did not finish")
}

func TestStart_AllInstancesParallel(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	n := 3
	rels := make([]chan struct{}, n)
	insts := make([]Instance, n)
	for i := range n {
		rels[i] = make(chan struct{})
		insts[i] = Instance{
			Config: instCfg(fmt.Sprintf("inst%d", i), "auto"),
			Client: &slowSonarr{
				fakeSonarr: &fakeSonarr{name: fmt.Sprintf("inst%d", i),
					series: []series.Series{monSeries(domain.SonarrSeriesID(1+i), "X")}},
				release: rels[i],
			},
		}
	}
	uc := newScanUCFor(t, lg, insts)

	start := time.Now()
	results, err := uc.Start(context.Background(), TriggerManual)
	require.NoError(t, err)
	require.Len(t, results, n)
	assert.Less(t, time.Since(start), 200*time.Millisecond, "Start must not wait for any scan")
	for _, r := range results {
		assert.Equal(t, "running", r.Status)
	}

	// All three goroutines must already be blocked inside slowSonarr.ListSeries.
	require.Eventually(t, func() bool { return len(uc.InflightScans()) == n },
		1*time.Second, 10*time.Millisecond, "all instances should be inflight")

	for _, ch := range rels {
		close(ch)
	}
	require.Eventually(t, func() bool { return !uc.IsAnyRunning() },
		2*time.Second, 10*time.Millisecond, "scans did not all drain")
}

func TestIncrementSeriesScanned_BatchedWrites(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	// 12 series → expected flushes at 5 and 10; residue (2) lands via final Update.
	sers := make([]series.Series, 0, 12)
	for i := 1; i <= 12; i++ {
		sers = append(sers, monSeries(domain.SonarrSeriesID(i), fmt.Sprintf("S%d", i)))
	}
	sn := &fakeSonarr{name: "main", series: sers}

	// Spying repo wraps fakeScanRepo to count IncrementSeriesScanned calls.
	spy := &incSpyScanRepo{fakeScanRepo: &fakeScanRepo{}}
	evalUC := evaluate.NewUseCase(sn, &fakeDecRepo{}, lg)
	uc := NewUseCase([]Instance{{Config: instCfg("main", "auto"), Client: sn}},
		evalUC, spy, lg, true)

	res, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, "completed", res.Status)
	assert.Equal(t, 12, res.Series)
	assert.Equal(t, 2, int(spy.incCalls.Load()),
		"expected 2 IncrementSeriesScanned flushes for 12 series at N=5")
	// Final Update writes the full row including the residue, so the persisted
	// counter ends at exactly 12.
	require.NotEmpty(t, spy.updated)
	last := spy.updated[len(spy.updated)-1]
	assert.Equal(t, 12, last.SeriesScanned)
}

// incSpyScanRepo wraps fakeScanRepo to count IncrementSeriesScanned calls.
type incSpyScanRepo struct {
	*fakeScanRepo
	incCalls atomic.Int32
}

func (s *incSpyScanRepo) IncrementSeriesScanned(ctx context.Context, id uuid.UUID, by int) error {
	s.incCalls.Add(1)
	return s.fakeScanRepo.IncrementSeriesScanned(ctx, id, by)
}

func TestStartInstance_ConflictWhenInflight(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sn := &slowSonarr{
		fakeSonarr: &fakeSonarr{name: "main", series: []series.Series{monSeries(1, "A")}},
		release:    make(chan struct{}),
	}
	uc := newScanUCFor(t, lg, []Instance{{Config: instCfg("main", "auto"), Client: sn}})

	res1, err1 := uc.StartInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err1)
	assert.Equal(t, "running", res1.Status)

	// Second call before the first goroutine releases the lock.
	_, err2 := uc.StartInstance(context.Background(), "main", TriggerManual)
	require.ErrorIs(t, err2, ErrScanAlreadyRunning)

	close(sn.release)
	require.Eventually(t, func() bool { return !uc.IsAnyRunning() },
		2*time.Second, 10*time.Millisecond)
}

func TestStartInstance_BgWaitGroupDrains(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sn := &slowSonarr{
		fakeSonarr: &fakeSonarr{name: "main", series: []series.Series{monSeries(1, "A")}},
		release:    make(chan struct{}),
	}
	uc := newScanUCFor(t, lg, []Instance{{Config: instCfg("main", "auto"), Client: sn}})
	var wg sync.WaitGroup
	uc.WithWaitGroup(&wg)

	_, err := uc.StartInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)

	// wg.Wait() must NOT return before we release the slow Sonarr — i.e. the
	// goroutine genuinely holds wg.Add(1) for its full lifetime.
	doneEarly := make(chan struct{})
	go func() { wg.Wait(); close(doneEarly) }()
	select {
	case <-doneEarly:
		t.Fatal("WaitGroup released before scan finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(sn.release)
	select {
	case <-doneEarly:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitGroup did not release after scan completion")
	}
}

// cancelOnListSonarr fires Cancel from inside ListSeries, then returns
// the series. processScan resumes, sees ctx.Canceled at the
// per-iteration check, breaks the loop, and the post-loop branch
// (§1.5) routes to finalizeScanCancelled with status="cancelled".
//
// ready must be closed by the test AFTER setting scanID. ListSeries
// blocks on ready so the goroutine cannot race past the scanID write —
// otherwise the goroutine may call ListSeries before the test stores the
// scan ID and completes naturally with status="completed".
type cancelOnListSonarr struct {
	*fakeSonarr
	uc     *UseCase
	scanID *uuid.UUID
	mu     sync.Mutex
	ready  chan struct{} // closed by test after scanID is set
}

func (s *cancelOnListSonarr) ListSeries(ctx context.Context) ([]series.Series, error) {
	if s.ready != nil {
		select {
		case <-s.ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	s.mu.Lock()
	id := s.scanID
	s.mu.Unlock()
	if id != nil {
		_ = s.uc.Cancel(context.Background(), *id)
	}
	return s.fakeSonarr.ListSeries(ctx)
}

func TestScan_Cancel_RunningScan(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sn := &slowSonarr{
		fakeSonarr: &fakeSonarr{name: "main", series: []series.Series{monSeries(1, "A")}},
		release:    make(chan struct{}),
	}
	uc := newScanUCFor(t, lg, []Instance{{Config: instCfg("main", "auto"), Client: sn}})

	res, err := uc.StartInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)
	require.Equal(t, "running", res.Status)

	require.NoError(t, uc.Cancel(context.Background(), res.ScanRunID))
	require.Eventually(t, func() bool { return !uc.IsAnyRunning() },
		2*time.Second, 10*time.Millisecond, "scan goroutine did not finish")
	close(sn.release) // unblock for cleanup determinism (no-op if ctx win)
}

func TestScan_Cancel_TerminalStatusIsCancelled(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	wrap := &cancelOnListSonarr{
		fakeSonarr: &fakeSonarr{name: "main", series: []series.Series{
			monSeries(1, "A"), monSeries(2, "B"),
		}},
		ready: make(chan struct{}),
	}
	scanRepo := &fakeScanRepo{}
	evalUC := evaluate.NewUseCase(wrap, &fakeDecRepo{}, lg)
	uc := NewUseCase([]Instance{{Config: instCfg("main", "auto"), Client: wrap}},
		evalUC, scanRepo, lg, true)
	wrap.uc = uc

	res, err := uc.StartInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)

	// Set scanID first, then open the gate so the goroutine is guaranteed
	// to see a non-nil scanID on its first (and only) call to ListSeries.
	wrap.mu.Lock()
	id := res.ScanRunID
	wrap.scanID = &id
	wrap.mu.Unlock()
	close(wrap.ready) // release the goroutine

	require.Eventually(t, func() bool { return !uc.IsAnyRunning() },
		2*time.Second, 10*time.Millisecond)

	scanRepo.mu.Lock()
	defer scanRepo.mu.Unlock()
	require.NotEmpty(t, scanRepo.updated)
	last := scanRepo.updated[len(scanRepo.updated)-1]
	assert.Equal(t, res.ScanRunID, last.ID)
	assert.Equal(t, "cancelled", last.Status)
	assert.Equal(t, "user requested cancellation", last.ErrorMessage)
	assert.NotNil(t, last.FinishedAt)
}

func TestScan_Cancel_NotRunning(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	uc := newScanUCFor(t, lg, []Instance{
		{Config: instCfg("main", "auto"), Client: &fakeSonarr{name: "main"}},
	})
	require.ErrorIs(t, uc.Cancel(context.Background(), uuid.New()), ErrScanNotRunning)
}

func TestScan_Cancel_RaceWithCompletion(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sn := &fakeSonarr{name: "main", series: []series.Series{monSeries(1, "A")}}
	uc := newScanUCFor(t, lg, []Instance{{Config: instCfg("main", "auto"), Client: sn}})

	res, err := uc.StartInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return !uc.IsAnyRunning() },
		2*time.Second, 10*time.Millisecond)

	// Late Cancel must not panic; must surface ErrScanNotRunning.
	require.ErrorIs(t, uc.Cancel(context.Background(), res.ScanRunID), ErrScanNotRunning)
}

// blockingUpdateRepo lets the test pin the goroutine inside the
// terminal-status Update call so a Cancel can be injected at exactly
// the gap between the series-loop exit and the Update write. Captures
// the passed ctx on Update entry so the test can re-check its Err()
// AFTER releasing the goroutine — proves whether the call uses the
// detached writeCtx or the cancellable scan ctx.
type blockingUpdateRepo struct {
	*fakeScanRepo
	enterCh    chan struct{} // closed when Update is first reached
	releaseCh  chan struct{} // closed by the test to let Update proceed
	once       sync.Once
	capturedMu sync.Mutex
	capturedCx context.Context //nolint:containedctx // captured for assertion
}

func (r *blockingUpdateRepo) Update(ctx context.Context, rec ports.ScanRecord) error {
	// Only block on the terminal Update (Status != "running"). Earlier
	// in-progress Updates (if any) shouldn't trip the gate.
	if rec.Status != "running" {
		r.once.Do(func() {
			r.capturedMu.Lock()
			r.capturedCx = ctx
			r.capturedMu.Unlock()
			close(r.enterCh)
			<-r.releaseCh
		})
	}
	return r.fakeScanRepo.Update(ctx, rec)
}

// TestScan_NormalCompletion_SurvivesCtxCancelDuringFinalize pins the
// goroutine inside the terminal Update call, fires Cancel, then
// releases Update. With the detached-writeCtx fix in place the row
// must transition to "completed" and Update must see a non-cancelled
// ctx. Pre-fix, ctx would carry context.Canceled and the row would be
// stuck at "running" (GORM aborts the write).
func TestScan_NormalCompletion_SurvivesCtxCancelDuringFinalize(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sn := &fakeSonarr{name: "main", series: []series.Series{monSeries(1, "A")}}
	repo := &blockingUpdateRepo{
		fakeScanRepo: &fakeScanRepo{},
		enterCh:      make(chan struct{}),
		releaseCh:    make(chan struct{}),
	}
	evalUC := evaluate.NewUseCase(sn, &fakeDecRepo{}, lg)
	uc := NewUseCase([]Instance{{Config: instCfg("main", "auto"), Client: sn}},
		evalUC, repo, lg, true)

	res, err := uc.StartInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)
	require.Equal(t, "running", res.Status)

	// Wait for the goroutine to reach the terminal Update.
	select {
	case <-repo.enterCh:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine never reached terminal Update")
	}

	// Inject Cancel while the goroutine is parked inside Update. With
	// the fix the writeCtx is detached, so this Cancel cannot poison
	// the write. Without the fix the row would stay "running".
	require.NoError(t, uc.Cancel(context.Background(), res.ScanRunID))

	close(repo.releaseCh)

	require.Eventually(t, func() bool { return !uc.IsAnyRunning() },
		2*time.Second, 10*time.Millisecond, "scan goroutine did not finish")

	// The ctx passed to Update must NOT be cancellable from uc.Cancel.
	// If processScan handed Update the scan's cancellable ctx, the
	// Cancel we issued above would have set ctx.Err() != nil by now.
	// The detached writeCtx is rooted in context.Background() and so
	// must still be live.
	repo.capturedMu.Lock()
	capCtx := repo.capturedCx
	repo.capturedMu.Unlock()
	require.NotNil(t, capCtx, "Update was never entered")
	assert.NoError(t, capCtx.Err(),
		"terminal Update must use detached writeCtx, not the cancellable scan ctx")

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.NotEmpty(t, repo.updated, "no terminal Update recorded")
	last := repo.updated[len(repo.updated)-1]
	assert.Equal(t, "completed", last.Status,
		"row must finalize to completed even when Cancel arrives during terminal Update")
}

// TestStartInstanceWithDryRun_NilUsesInstanceDefault — when the request
// passes no override, the persisted ScanRecord.DryRun must equal the
// per-instance / global computation. Instance has DryRun=true here, so
// the record must be DryRun=true.
func TestStartInstanceWithDryRun_NilUsesInstanceDefault(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sonarr := &fakeSonarr{name: "main"} // empty series list -> scan completes instantly
	evalUC := evaluate.NewUseCase(sonarr, &fakeDecRepo{}, lg)
	repo := &fakeScanRepo{}
	uc := NewUseCase([]Instance{{
		Config: config.SonarrInstance{
			Name:   "main",
			DryRun: new(true),
			Limits: config.LimitsConfig{ScanMaxSeries: 10},
		},
		Client: sonarr,
	}}, evalUC, repo, lg, false /* global=false, so only instance override applies */)

	res, err := uc.StartInstanceWithDryRun(context.Background(), "main", TriggerManual, nil)
	require.NoError(t, err)
	require.Equal(t, "running", res.Status)

	// Drain the async goroutine.
	waitForScanRecord(t, repo, res.ScanRunID, "completed")

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.created, 1)
	assert.True(t, repo.created[0].DryRun,
		"nil override + instance.DryRun=true -> ScanRecord.DryRun must be true")
}

// TestStartInstanceWithDryRun_ForceTrue — request override = &true must
// flip ScanRecord.DryRun=true even when the instance config says false.
func TestStartInstanceWithDryRun_ForceTrue(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sonarr := &fakeSonarr{name: "main"}
	evalUC := evaluate.NewUseCase(sonarr, &fakeDecRepo{}, lg)
	repo := &fakeScanRepo{}
	uc := NewUseCase([]Instance{{
		Config: config.SonarrInstance{
			Name:   "main",
			DryRun: new(false), // instance says real grab
			Limits: config.LimitsConfig{ScanMaxSeries: 10},
		},
		Client: sonarr,
	}}, evalUC, repo, lg, false)

	res, err := uc.StartInstanceWithDryRun(context.Background(), "main", TriggerManual, new(true))
	require.NoError(t, err)
	require.Equal(t, "running", res.Status)
	waitForScanRecord(t, repo, res.ScanRunID, "completed")

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.created, 1)
	assert.True(t, repo.created[0].DryRun,
		"request override *true must beat instance.DryRun=false")
}

// TestStartInstanceWithDryRun_ForceFalse — request override = &false
// must flip ScanRecord.DryRun=false even when the instance config says
// true. This is the "Force real grab" path from 033c.
func TestStartInstanceWithDryRun_ForceFalse(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sonarr := &fakeSonarr{name: "main"}
	evalUC := evaluate.NewUseCase(sonarr, &fakeDecRepo{}, lg)
	repo := &fakeScanRepo{}
	uc := NewUseCase([]Instance{{
		Config: config.SonarrInstance{
			Name:   "main",
			DryRun: new(true), // instance is dry
			Limits: config.LimitsConfig{ScanMaxSeries: 10},
		},
		Client: sonarr,
	}}, evalUC, repo, lg, true /* global is also dry */)

	res, err := uc.StartInstanceWithDryRun(context.Background(), "main", TriggerManual, new(false))
	require.NoError(t, err)
	require.Equal(t, "running", res.Status)
	waitForScanRecord(t, repo, res.ScanRunID, "completed")

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.created, 1)
	assert.False(t, repo.created[0].DryRun,
		"request override *false must beat instance.DryRun=true")
}

// waitForScanRecord polls the fake repo until ScanRecord.Status reaches
// `want` or the budget expires. Mirrors the polling pattern used by
// TestStartInstance_BgWaitGroupDrains — the async goroutine writes the
// terminal Update from a detached ctx, so we cannot use sync.WaitGroup
// here without instrumenting the use case further.
func waitForScanRecord(t *testing.T, repo *fakeScanRepo, id uuid.UUID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		for _, rec := range repo.updated {
			if rec.ID == id && rec.Status == want {
				repo.mu.Unlock()
				return
			}
		}
		repo.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("scan record %s did not reach status=%s within 2s", id, want)
}

type fakeSeriesCache struct {
	mu        sync.Mutex
	upserted  []series.CacheEntry
	upsertErr error
}

func (f *fakeSeriesCache) Get(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID) (series.CacheEntry, error) {
	return series.CacheEntry{}, ports.ErrNotFound
}
func (f *fakeSeriesCache) Upsert(_ context.Context, e series.CacheEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upserted = append(f.upserted, e)
	return nil
}
func (f *fakeSeriesCache) SoftDelete(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID) error {
	return nil
}
func (f *fakeSeriesCache) ListActiveByInstance(_ context.Context, _ domain.InstanceName) ([]series.CacheEntry, error) {
	return nil, nil
}
func (f *fakeSeriesCache) ListByFilter(_ context.Context, _ domain.InstanceName, _ ports.SeriesCacheFilter, _ ports.SeriesCacheSort, _ ports.Pagination) ([]series.CacheEntry, int, bool, *ports.Cursor, error) {
	return nil, 0, false, nil, nil
}
func (f *fakeSeriesCache) FetchLastGrabInfo(_ context.Context, _ domain.InstanceName, _ []domain.SonarrSeriesID) (map[domain.SonarrSeriesID]ports.LastGrabInfo, error) {
	return make(map[domain.SonarrSeriesID]ports.LastGrabInfo), nil
}
func (f *fakeSeriesCache) ListDistinctNetworks(_ context.Context, _ domain.InstanceName) ([]string, error) {
	return nil, nil
}
func (f *fakeSeriesCache) GetInstancesBySeriesID(_ context.Context, _ domain.SeriesID) ([]domain.InstanceName, error) {
	return nil, nil
}
func (f *fakeSeriesCache) GetInstancesBySeriesIDs(_ context.Context, _ []domain.SeriesID) (map[domain.SeriesID][]domain.InstanceName, error) {
	return map[domain.SeriesID][]domain.InstanceName{}, nil
}
func (f *fakeSeriesCache) ListBySeriesID(_ context.Context, _ domain.SeriesID) ([]series.CacheEntry, error) {
	return nil, nil
}
func (f *fakeSeriesCache) ListBySeriesIDs(_ context.Context, _ []domain.SeriesID) (map[domain.SeriesID][]series.CacheEntry, error) {
	return map[domain.SeriesID][]series.CacheEntry{}, nil
}

var _ ports.SeriesCacheRepository = (*fakeSeriesCache)(nil)

func twoMonitoredSeries() []series.Series {
	return []series.Series{monSeries(1, "S1"), monSeries(2, "S2")}
}

func newScanUseCaseForTest(t *testing.T, client ports.SonarrClient) *UseCase {
	t.Helper()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return newScanUCFor(t, lg, []Instance{{
		Config: instCfg("main", "auto"),
		Client: client,
	}})
}

func TestScanUseCase_SeriesCache_UpsertsEverySeries(t *testing.T) {
	t.Parallel()
	sonarrFake := &fakeSonarr{name: "main", series: twoMonitoredSeries()}
	cache := &fakeSeriesCache{}
	uc := newScanUseCaseForTest(t, sonarrFake).WithSeriesCache(cache)

	_, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)

	cache.mu.Lock()
	defer cache.mu.Unlock()
	require.Len(t, cache.upserted, 2)
	for _, e := range cache.upserted {
		assert.Equal(t, domain.InstanceName("main"), e.InstanceName)
	}
}

func TestScanUseCase_SeriesCache_UpsertErrorDoesNotFailScan(t *testing.T) {
	t.Parallel()
	sonarrFake := &fakeSonarr{name: "main", series: twoMonitoredSeries()}
	cache := &fakeSeriesCache{upsertErr: errors.New("disk full")}
	uc := newScanUseCaseForTest(t, sonarrFake).WithSeriesCache(cache)

	res, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, "completed", res.Status)
}

func TestScanUseCase_SeriesCache_NilRepo_NoCall(t *testing.T) {
	t.Parallel()
	sonarrFake := &fakeSonarr{name: "main", series: twoMonitoredSeries()}
	uc := newScanUseCaseForTest(t, sonarrFake) // no WithSeriesCache
	res, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, "completed", res.Status)
}

// fakeSeasonStats is a minimal SeasonStatsRepository stand-in for the
// story 380 wiring test. Records every Upsert call so the test can
// assert per-season fanout.
type fakeSeasonStats struct {
	mu        sync.Mutex
	upserted  []series.SeasonStat
	upsertErr error
}

func (f *fakeSeasonStats) Upsert(_ context.Context, s series.SeasonStat) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upserted = append(f.upserted, s)
	return nil
}

func (f *fakeSeasonStats) SoftDeleteBySeries(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID) (int, error) {
	return 0, nil
}

var _ SeasonStatsRepository = (*fakeSeasonStats)(nil)

// multiSeasonSeries returns a single series with two seasons whose
// statistics differ so the fanout assertion can distinguish them.
func multiSeasonSeries(id domain.SonarrSeriesID, title string) series.Series {
	return series.Series{
		ID: id, Title: title, Type: series.SeriesTypeStandard, Monitored: true,
		QualityProfile: 14,
		Seasons: []series.Season{
			{
				Number: 1, Monitored: true,
				Statistics: series.Statistics{
					EpisodeCount: 10, EpisodeFileCount: 10,
					Total: 10, Aired: 10, SizeOnDisk: 1_000_000,
				},
			},
			{
				Number: 2, Monitored: true,
				Statistics: series.Statistics{
					EpisodeCount: 8, EpisodeFileCount: 3,
					Total: 10, Aired: 8, SizeOnDisk: 500_000,
				},
			},
		},
	}
}

// TestScanUseCase_SeasonStats_UpsertsEverySeason — story 380 fix:
// fillSeriesCache must write one season_stats row per Sonarr season per
// series, not only via the webhook E-1 path.
func TestScanUseCase_SeasonStats_UpsertsEverySeason(t *testing.T) {
	t.Parallel()
	sonarrFake := &fakeSonarr{name: "main", series: []series.Series{
		multiSeasonSeries(140, "Rick"),
		multiSeasonSeries(369, "FROM"),
	}}
	stats := &fakeSeasonStats{}
	uc := newScanUseCaseForTest(t, sonarrFake).WithSeasonStats(stats)

	_, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)

	stats.mu.Lock()
	defer stats.mu.Unlock()
	require.Len(t, stats.upserted, 4, "two series x two seasons each = four rows")

	// Spot-check one season for correct field projection.
	var s140s2 *series.SeasonStat
	for i, s := range stats.upserted {
		if s.SonarrSeriesID == 140 && s.SeasonNumber == 2 {
			s140s2 = &stats.upserted[i]
			break
		}
	}
	require.NotNil(t, s140s2)
	assert.Equal(t, domain.InstanceName("main"), s140s2.InstanceName)
	assert.Equal(t, 8, s140s2.EpisodeCount)
	assert.Equal(t, 3, s140s2.EpisodeFileCount)
	assert.Equal(t, 10, s140s2.TotalEpisodeCount)
	assert.Equal(t, 8, s140s2.AiredEpisodeCount)
	assert.Equal(t, int64(500_000), s140s2.SizeOnDiskBytes)
	assert.True(t, s140s2.Monitored)
}

// TestScanUseCase_SeasonStats_AiredFallback — defensive: if a future
// Sonarr response omits Aired at the season level too, the writer
// falls back to EpisodeCount (same belt-and-braces fix as the series-
// level path).
func TestScanUseCase_SeasonStats_AiredFallback(t *testing.T) {
	t.Parallel()
	s := series.Series{
		ID: 372, Title: "Star City", Type: series.SeriesTypeStandard, Monitored: true,
		QualityProfile: 14,
		Seasons: []series.Season{{
			Number: 1, Monitored: true,
			Statistics: series.Statistics{
				EpisodeCount: 5, EpisodeFileCount: 4,
				// Aired intentionally zero.
			},
		}},
	}
	sonarrFake := &fakeSonarr{name: "main", series: []series.Series{s}}
	stats := &fakeSeasonStats{}
	uc := newScanUseCaseForTest(t, sonarrFake).WithSeasonStats(stats)

	_, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)

	stats.mu.Lock()
	defer stats.mu.Unlock()
	require.Len(t, stats.upserted, 1)
	assert.Equal(t, 5, stats.upserted[0].AiredEpisodeCount,
		"AiredEpisodeCount should fall back to EpisodeCount when Aired is zero")
}

// TestScanUseCase_SeasonStats_UpsertErrorDoesNotFailScan — best-effort
// sidecar contract: a season_stats writer failure must not abort the
// scan (same D-2.5 pattern as series_cache).
func TestScanUseCase_SeasonStats_UpsertErrorDoesNotFailScan(t *testing.T) {
	t.Parallel()
	sonarrFake := &fakeSonarr{name: "main", series: []series.Series{multiSeasonSeries(140, "Rick")}}
	stats := &fakeSeasonStats{upsertErr: errors.New("disk full")}
	uc := newScanUseCaseForTest(t, sonarrFake).WithSeasonStats(stats)

	res, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, "completed", res.Status)
}

// TestScanUseCase_SeasonStats_NilRepo_NoCall — without the dep wired
// the use case must still complete cleanly (legacy callers).
func TestScanUseCase_SeasonStats_NilRepo_NoCall(t *testing.T) {
	t.Parallel()
	sonarrFake := &fakeSonarr{name: "main", series: []series.Series{multiSeasonSeries(140, "Rick")}}
	uc := newScanUseCaseForTest(t, sonarrFake) // no WithSeasonStats
	res, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, "completed", res.Status)
}

// TestScanUseCase_SeasonStats_RegressionStory380Wiring is the
// named-for-what-it-protects regression test for Story 380. The pre-fix
// scan loop silently skipped season_stats writes — webhook E-1 path
// wrote them, but the 6h scan tick did not, leaving
// SeriesSeasonsAccordion rendering 0/N for every season on instances
// whose webhook never fired.
//
// Distinct from TestScanUseCase_SeasonStats_UpsertsEverySeason (which
// uses 2x2 matrix to test fanout): this test uses the simplest possible
// 1x1 fixture so a regression ("the call disappeared from
// fillSeriesCache") fails with the cleanest possible message.
func TestScanUseCase_SeasonStats_RegressionStory380Wiring(t *testing.T) {
	t.Parallel()

	single := series.Series{
		ID: 140, Title: "Rick and Morty",
		Type:           series.SeriesTypeStandard,
		Monitored:      true,
		QualityProfile: 14,
		Seasons: []series.Season{{
			Number: 2, Monitored: true,
			Statistics: series.Statistics{
				EpisodeCount: 5, EpisodeFileCount: 5,
				Total: 5, Aired: 5, SizeOnDisk: 2_000_000,
			},
		}},
	}
	sonarrFake := &fakeSonarr{name: "main", series: []series.Series{single}}
	stats := &fakeSeasonStats{}
	uc := newScanUseCaseForTest(t, sonarrFake).WithSeasonStats(stats)

	_, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)

	stats.mu.Lock()
	defer stats.mu.Unlock()
	require.Len(t, stats.upserted, 1,
		"REGRESSION Story 380: fillSeriesCache must call SeasonStats.Upsert exactly once per Sonarr season")

	got := stats.upserted[0]
	assert.Equal(t, domain.InstanceName("main"), got.InstanceName)
	assert.Equal(t, domain.SonarrSeriesID(140), got.SonarrSeriesID,
		"REGRESSION: SonarrSeriesID must be projected from payload")
	assert.Equal(t, 2, got.SeasonNumber,
		"REGRESSION: SeasonNumber must be projected from payload")
	assert.Equal(t, 5, got.EpisodeCount)
	assert.Equal(t, 5, got.EpisodeFileCount)
	assert.Equal(t, 5, got.AiredEpisodeCount)
	assert.Equal(t, int64(2_000_000), got.SizeOnDiskBytes)
	assert.True(t, got.Monitored)
}

// fakeEpisodeStatesRefresher captures RefreshEpisodeStates calls for the
// F-975 scan-piggyback wiring tests.
type fakeEpisodeStatesRefresher struct {
	mu    sync.Mutex
	calls []domain.SonarrSeriesID
	err   error
}

func (f *fakeEpisodeStatesRefresher) RefreshEpisodeStates(_ context.Context, _ domain.InstanceName, id domain.SonarrSeriesID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, id)
	return f.err
}

var _ EpisodeStatesRefresher = (*fakeEpisodeStatesRefresher)(nil)

// fakeEpisodeStatesCoverage is the #1031 heal-guard probe stub. count is the
// coverage returned for every series; err (when set) simulates a probe failure.
type fakeEpisodeStatesCoverage struct {
	mu    sync.Mutex
	count int
	err   error
	calls []domain.SonarrSeriesID
}

func (f *fakeEpisodeStatesCoverage) CountBySeries(_ context.Context, _ domain.InstanceName, id domain.SonarrSeriesID) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, id)
	return f.count, f.err
}

var _ EpisodeStatesCoverage = (*fakeEpisodeStatesCoverage)(nil)

// fakeCanonEpisodeCounter is the W18-3 (#1044) heal-reloop guard stub. count is
// the canon-episode count returned for every series; err (when set) simulates a
// count failure. calls records each probed series id.
type fakeCanonEpisodeCounter struct {
	mu    sync.Mutex
	count int
	err   error
	calls []domain.SonarrSeriesID
}

func (f *fakeCanonEpisodeCounter) CanonEpisodeCount(_ context.Context, _ domain.InstanceName, id domain.SonarrSeriesID) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, id)
	return f.count, f.err
}

var _ CanonEpisodeCounter = (*fakeCanonEpisodeCounter)(nil)

// completeSeries builds a series whose single monitored season is fully
// on-disk (Aired == EpisodeFileCount) so seriesAllSeasonsComplete short-
// circuits it before any Sonarr per-series work — the F-975 piggyback must
// NOT fire for it.
func completeSeries(id domain.SonarrSeriesID, title string) series.Series {
	return series.Series{ID: id, Title: title, Type: series.SeriesTypeStandard, Monitored: true,
		QualityProfile: 14,
		Seasons: []series.Season{{
			Number: 1, Monitored: true,
			Statistics: series.Statistics{Total: 10, Aired: 10, EpisodeFileCount: 10},
		}},
	}
}

func TestProcessScan_PiggybacksEpisodeStates_ForWalkedSeries(t *testing.T) {
	t.Parallel()
	sonarrFake := &fakeSonarr{name: "main", series: []series.Series{monSeries(140, "Rick and Morty")}}
	ref := &fakeEpisodeStatesRefresher{}
	uc := newScanUseCaseForTest(t, sonarrFake).WithEpisodeStatesRefresher(ref)
	_, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)
	ref.mu.Lock()
	defer ref.mu.Unlock()
	require.Contains(t, ref.calls, domain.SonarrSeriesID(140))
}

func TestProcessScan_PiggybackSkipped_ForAllCompleteSeries(t *testing.T) {
	t.Parallel()
	sonarrFake := &fakeSonarr{name: "main", series: []series.Series{completeSeries(140, "Rick and Morty")}}
	ref := &fakeEpisodeStatesRefresher{}
	uc := newScanUseCaseForTest(t, sonarrFake).WithEpisodeStatesRefresher(ref)
	_, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)
	ref.mu.Lock()
	defer ref.mu.Unlock()
	assert.Empty(t, ref.calls) // fast-path skipped -> no refresh
}

func TestProcessScan_PiggybackError_DoesNotFailScan(t *testing.T) {
	t.Parallel()
	sonarrFake := &fakeSonarr{name: "main", series: []series.Series{monSeries(140, "Rick and Morty")}}
	ref := &fakeEpisodeStatesRefresher{err: errors.New("sonarr down")}
	uc := newScanUseCaseForTest(t, sonarrFake).WithEpisodeStatesRefresher(ref)
	res, err := uc.RunInstance(context.Background(), "main", TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, "completed", res.Status)
}

// newCompleteSeriesScanUC builds a scan UseCase over a single all-complete
// series, wiring a decRepo the caller can inspect for the ReasonAllComplete
// skip audit trail. Sonarr per-series calls (GetQualityProfile /
// ListEpisodeFiles) must NOT fire on the fast-path — the fakeSonarr would
// error/panic if they did.
func newCompleteSeriesScanUC(t *testing.T, dec *fakeDecRepo) (*UseCase, *fakeSonarr) {
	t.Helper()
	sonarrFake := &fakeSonarr{name: "homelab", series: []series.Series{completeSeries(140, "Star City")}}
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	evalUC := evaluate.NewUseCase(sonarrFake, dec, lg)
	uc := NewUseCase([]Instance{{
		Config: config.SonarrInstance{
			Name:   "homelab",
			Limits: config.LimitsConfig{ScanMaxSeries: 100, MaxGrabsPerScan: 10},
		},
		Client: sonarrFake,
	}}, evalUC, &fakeScanRepo{}, lg, true)
	return uc, sonarrFake
}

// TestProcessScan_HealsEpisodeStates_ForCompleteSeriesMissingCoverage is the
// #1031 fix: an all-complete series with ZERO episode_states rows must get a
// one-time heal refresh, while the ReasonAllComplete skip audit trail stays
// intact.
func TestProcessScan_HealsEpisodeStates_ForCompleteSeriesMissingCoverage(t *testing.T) {
	t.Parallel()
	dec := &fakeDecRepo{}
	uc, _ := newCompleteSeriesScanUC(t, dec)
	ref := &fakeEpisodeStatesRefresher{}
	cov := &fakeEpisodeStatesCoverage{count: 0} // no episode_states yet
	uc.WithEpisodeStatesRefresher(ref).WithEpisodeStatesCoverage(cov)

	res, err := uc.RunInstance(context.Background(), "homelab", TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, "completed", res.Status)

	ref.mu.Lock()
	assert.Contains(t, ref.calls, domain.SonarrSeriesID(140), "missing coverage must trigger a heal refresh")
	ref.mu.Unlock()

	require.Len(t, dec.d, 1, "all-complete skip decision must still be recorded")
	assert.Equal(t, decision.ReasonAllComplete, dec.d[0].Reason)
	assert.Equal(t, decision.OutcomeSkip, dec.d[0].Outcome)
}

// TestProcessScan_SkipsHeal_ForCompleteSeriesWithCoverage asserts the guard:
// when episode_states coverage already exists the fast-path issues NO Sonarr
// episode fetch (steady-state scans stay call-free), yet still records the
// skip decision.
func TestProcessScan_SkipsHeal_ForCompleteSeriesWithCoverage(t *testing.T) {
	t.Parallel()
	dec := &fakeDecRepo{}
	uc, _ := newCompleteSeriesScanUC(t, dec)
	ref := &fakeEpisodeStatesRefresher{}
	cov := &fakeEpisodeStatesCoverage{count: 10} // full coverage present
	uc.WithEpisodeStatesRefresher(ref).WithEpisodeStatesCoverage(cov)

	res, err := uc.RunInstance(context.Background(), "homelab", TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, "completed", res.Status)

	ref.mu.Lock()
	assert.Empty(t, ref.calls, "present coverage must NOT trigger a heal refresh")
	ref.mu.Unlock()

	cov.mu.Lock()
	assert.Contains(t, cov.calls, domain.SonarrSeriesID(140), "guard must probe coverage")
	cov.mu.Unlock()

	require.Len(t, dec.d, 1, "all-complete skip decision must still be recorded")
	assert.Equal(t, decision.ReasonAllComplete, dec.d[0].Reason)
}

// TestProcessScan_HealCoverageProbeError_DoesNotFailScan asserts a coverage
// probe failure is swallowed (best-effort): no heal, scan still completes.
func TestProcessScan_HealCoverageProbeError_DoesNotFailScan(t *testing.T) {
	t.Parallel()
	dec := &fakeDecRepo{}
	uc, _ := newCompleteSeriesScanUC(t, dec)
	ref := &fakeEpisodeStatesRefresher{}
	cov := &fakeEpisodeStatesCoverage{err: errors.New("db down")}
	uc.WithEpisodeStatesRefresher(ref).WithEpisodeStatesCoverage(cov)

	res, err := uc.RunInstance(context.Background(), "homelab", TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, "completed", res.Status)

	ref.mu.Lock()
	assert.Empty(t, ref.calls, "probe error must NOT trigger a heal refresh")
	ref.mu.Unlock()
}

// newHealTestUseCase builds a bare scan UseCase suitable for calling the
// unexported heal helper directly — the heal path touches only the refresher,
// coverage, canon-counter and logger, never the evaluator or instances.
func newHealTestUseCase(t *testing.T) *UseCase {
	t.Helper()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return NewUseCase(nil, nil, &fakeScanRepo{}, lg, true)
}

// TestHealEpisodeStates_SkipsNoCanonEpisodes_NoReloop is the W18-3 (#1044)
// proof: a tmdb-less / canon-unresolved all-complete series (coverage stays 0
// because there are no canon episodes to key episode_states against) must NEVER
// call Sonarr — not on the first scan and not on any subsequent scan. Before
// the fix the heal re-fired forever.
func TestHealEpisodeStates_SkipsNoCanonEpisodes_NoReloop(t *testing.T) {
	t.Parallel()
	uc := newHealTestUseCase(t)
	ref := &fakeEpisodeStatesRefresher{}
	cov := &fakeEpisodeStatesCoverage{count: 0} // coverage never climbs (symptom)
	cnt := &fakeCanonEpisodeCounter{count: 0}   // zero canon episodes
	uc.WithEpisodeStatesRefresher(ref).
		WithEpisodeStatesCoverage(cov).
		WithCanonEpisodeCounter(cnt)

	uc.healEpisodeStatesForCompleteSeries(context.Background(), "main", 140)
	uc.healEpisodeStatesForCompleteSeries(context.Background(), "main", 140)

	ref.mu.Lock()
	assert.Empty(t, ref.calls, "no canon episodes -> heal must never call Sonarr, across scans")
	ref.mu.Unlock()

	cnt.mu.Lock()
	assert.Len(t, cnt.calls, 2, "canon-episode count must be consulted on each zero-coverage scan")
	cnt.mu.Unlock()
}

// TestHealEpisodeStates_HealsOnceThenCovered proves the preserved #1031
// behavior for a series that DOES have canon episodes: heal fires exactly once
// while coverage is absent, and once the write lands (coverage > 0) subsequent
// scans issue no further Sonarr fetch.
func TestHealEpisodeStates_HealsOnceThenCovered(t *testing.T) {
	t.Parallel()
	uc := newHealTestUseCase(t)
	ref := &fakeEpisodeStatesRefresher{}
	cov := &fakeEpisodeStatesCoverage{count: 0} // coverage absent
	cnt := &fakeCanonEpisodeCounter{count: 12}  // canon episodes exist
	uc.WithEpisodeStatesRefresher(ref).
		WithEpisodeStatesCoverage(cov).
		WithCanonEpisodeCounter(cnt)

	uc.healEpisodeStatesForCompleteSeries(context.Background(), "main", 140)
	ref.mu.Lock()
	assert.Len(t, ref.calls, 1, "canon episodes + absent coverage must heal exactly once")
	ref.mu.Unlock()

	// simulate the heal write landing: coverage now present.
	cov.mu.Lock()
	cov.count = 12
	cov.mu.Unlock()

	uc.healEpisodeStatesForCompleteSeries(context.Background(), "main", 140)
	ref.mu.Lock()
	assert.Len(t, ref.calls, 1, "once covered, subsequent scans must not re-heal")
	ref.mu.Unlock()
}

// TestHealEpisodeStates_NilCanonCounter_PreservesPriorBehavior asserts the
// nil-OK fallback: with no canon counter wired, the pre-W18-3 #1031 behavior
// stands (heal fires whenever coverage is absent) and nothing panics.
func TestHealEpisodeStates_NilCanonCounter_PreservesPriorBehavior(t *testing.T) {
	t.Parallel()
	uc := newHealTestUseCase(t)
	ref := &fakeEpisodeStatesRefresher{}
	cov := &fakeEpisodeStatesCoverage{count: 0}
	uc.WithEpisodeStatesRefresher(ref).WithEpisodeStatesCoverage(cov)
	// canonEpisodeCounter deliberately unset (nil).

	require.NotPanics(t, func() {
		uc.healEpisodeStatesForCompleteSeries(context.Background(), "main", 140)
	})
	ref.mu.Lock()
	assert.Len(t, ref.calls, 1, "nil counter -> prior #1031 behavior: heal fires on absent coverage")
	ref.mu.Unlock()
}
