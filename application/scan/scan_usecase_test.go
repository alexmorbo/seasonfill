package scan

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/config"
)

type fakeSonarr struct {
	name   string
	series []series.Series
}

func (f *fakeSonarr) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{Version: "test"}, nil
}
func (f *fakeSonarr) ListSeries(_ context.Context) ([]series.Series, error) { return f.series, nil }
func (f *fakeSonarr) GetSeries(_ context.Context, _ int) (series.Series, error) {
	return series.Series{}, nil
}
func (f *fakeSonarr) ListEpisodes(_ context.Context, _, sn int) ([]series.Episode, error) {
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
	return []release.Release{
		{
			GUID:                 "g1",
			Title:                "S02 pack",
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
func (f *fakeSonarr) ListTags(_ context.Context) ([]ports.Tag, error)         { return nil, nil }
func (f *fakeSonarr) GrabHistory(_ context.Context, _ int) ([]ports.HistoryEvent, error) {
	return nil, nil
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
			Limits:  config.LimitsConfig{ScanMaxSeries: 100},
			Ranking: config.RankingConfig{OriginBonus: 1.0},
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

func TestScan_ConcurrentSameInstanceReturnsConflict(t *testing.T) {
	uc, _, _ := makeUseCase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var (
		err1, err2 error
		wg         sync.WaitGroup
	)
	wg.Add(2)
	go func() { defer wg.Done(); _, err1 = uc.RunInstance(ctx, "main", TriggerCron) }()
	go func() { defer wg.Done(); _, err2 = uc.RunInstance(ctx, "main", TriggerManual) }()
	wg.Wait()

	gotConflict := errors.Is(err1, ErrScanAlreadyRunning) || errors.Is(err2, ErrScanAlreadyRunning)
	gotSuccess := err1 == nil || err2 == nil
	assert.True(t, gotConflict, "expected one call to fail with ErrScanAlreadyRunning")
	assert.True(t, gotSuccess, "expected the other call to succeed")
}
