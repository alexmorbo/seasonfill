package scan

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// boolPtr is a tiny helper to take the address of a literal bool value
// when populating *bool config fields in tests.
func boolPtr(b bool) *bool { return &b }

type fakeSonarr struct {
	name     string
	series   []series.Series
	episodes func(seriesID, season int) []series.Episode
	releases []release.Release
	tags     []ports.Tag
	tagsErr  error
	grabErr  error
	grabCnt  int
	grabMu   sync.Mutex
}

func (f *fakeSonarr) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{Version: "test"}, nil
}
func (f *fakeSonarr) ListSeries(_ context.Context) ([]series.Series, error) { return f.series, nil }
func (f *fakeSonarr) GetSeries(_ context.Context, _ int) (series.Series, error) {
	return series.Series{}, nil
}
func (f *fakeSonarr) ListEpisodes(_ context.Context, sID, sn int) ([]series.Episode, error) {
	if f.episodes != nil {
		return f.episodes(sID, sn), nil
	}
	return []series.Episode{
		{Number: 1, SeasonNumber: sn, Monitored: true, HasFile: true, QualityID: 19, QualityName: "WEBDL-2160p"},
		{Number: 2, SeasonNumber: sn, Monitored: true, HasFile: true, QualityID: 19, QualityName: "WEBDL-2160p"},
		{Number: 3, SeasonNumber: sn, Monitored: true, HasFile: false},
	}, nil
}
func (f *fakeSonarr) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return map[int]int{}, nil
}
func (f *fakeSonarr) SearchReleases(_ context.Context, _, _ int) ([]release.Release, error) {
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
func (f *fakeSonarr) GrabHistory(_ context.Context, _ int) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (f *fakeSonarr) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	f.grabMu.Lock()
	defer f.grabMu.Unlock()
	f.grabCnt++
	return "", f.grabErr
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

func (r *fakeDecRepo) List(_ context.Context, _ ports.DecisionFilter, _ ports.Pagination) ([]decision.Decision, *ports.Cursor, error) {
	panic("fake List unexpectedly called - this stub is not configured for List queries")
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

func monSeries(id int, title string) series.Series {
	return series.Series{ID: id, Title: title, Type: series.SeriesTypeStandard, Monitored: true,
		QualityProfile: 14, Seasons: []series.Season{{Number: 1, Monitored: true}}}
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
	assert.Equal(t, "auto1", results[0].InstanceName)
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
		tt := tt
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
		tt := tt
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

func (b *barrier) Reached(_ string) {
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
				Seasons: []series.Season{{Number: 2, Monitored: true}}},
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
	inst := Instance{Config: config.SonarrInstance{Name: "x", DryRun: boolPtr(false)}}
	assert.False(t, uc.instanceDryRun(inst))

	// No override: inherits global dry-run=true.
	inst2 := Instance{Config: config.SonarrInstance{Name: "x"}}
	assert.True(t, uc.instanceDryRun(inst2))

	// Explicit override = true is also honored (matches global, still bound).
	inst3 := Instance{Config: config.SonarrInstance{Name: "x", DryRun: boolPtr(true)}}
	assert.True(t, uc.instanceDryRun(inst3))
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
			{ID: 200, Title: "Show", Type: series.SeriesTypeStandard, Monitored: true, QualityProfile: 14,
				Seasons: []series.Season{
					{Number: 1, Monitored: true},
					{Number: 2, Monitored: true},
					{Number: 3, Monitored: true},
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
