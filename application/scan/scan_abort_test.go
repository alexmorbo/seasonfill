package scan

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/grab"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	"github.com/alexmorbo/seasonfill/domain/decision"
	domaingrab "github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/domain/instance"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// abortFakeSonarr returns enough monitored series + monitored seasons to
// exceed the 3-consecutive-grab_failed threshold, and always errors on
// ForceGrab so every grab attempt fails. Each Sonarr method increments
// a public atomic counter so tests can assert "nothing was called".
type abortFakeSonarr struct {
	name              string
	systemStatusCalls int64
	listSeriesCalls   int64
	getSeriesCalls    int64
	listEpisodesCalls int64
	listFilesCalls    int64
	searchCalls       int64
	qualityProfCalls  int64
	listIndexersCalls int64
	listTagsCalls     int64
	grabHistoryCalls  int64
	forceGrabCalls    int64
}

// totalCalls returns the sum of every Sonarr-method invocation counter.
// Used by gated-scan tests to assert no method was invoked.
func (f *abortFakeSonarr) totalCalls() int64 {
	return atomic.LoadInt64(&f.systemStatusCalls) +
		atomic.LoadInt64(&f.listSeriesCalls) +
		atomic.LoadInt64(&f.getSeriesCalls) +
		atomic.LoadInt64(&f.listEpisodesCalls) +
		atomic.LoadInt64(&f.listFilesCalls) +
		atomic.LoadInt64(&f.searchCalls) +
		atomic.LoadInt64(&f.qualityProfCalls) +
		atomic.LoadInt64(&f.listIndexersCalls) +
		atomic.LoadInt64(&f.listTagsCalls) +
		atomic.LoadInt64(&f.grabHistoryCalls) +
		atomic.LoadInt64(&f.forceGrabCalls)
}

func (f *abortFakeSonarr) Name() string { return f.name }

func (f *abortFakeSonarr) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	atomic.AddInt64(&f.systemStatusCalls, 1)
	return ports.SystemStatus{Version: "test"}, nil
}

func (f *abortFakeSonarr) ListSeries(_ context.Context) ([]series.Series, error) {
	atomic.AddInt64(&f.listSeriesCalls, 1)
	out := make([]series.Series, 0, 5)
	for i := 1; i <= 5; i++ {
		out = append(out, series.Series{
			ID:             i,
			Title:          "S",
			Monitored:      true,
			QualityProfile: 14,
			Seasons:        []series.Season{{Number: 1, Monitored: true}},
		})
	}
	return out, nil
}

func (f *abortFakeSonarr) GetSeries(_ context.Context, _ int) (series.Series, error) {
	atomic.AddInt64(&f.getSeriesCalls, 1)
	return series.Series{}, nil
}

func (f *abortFakeSonarr) ListEpisodes(_ context.Context, _, sn int) ([]series.Episode, error) {
	atomic.AddInt64(&f.listEpisodesCalls, 1)
	return []series.Episode{
		{ID: 1, Number: 1, SeasonNumber: sn, Title: "e1", Monitored: true, HasFile: true, QualityID: 5, QualityName: "WEB-1080p",
			AirDateUTC: time.Now().UTC().Add(-14 * 24 * time.Hour)},
		{ID: 2, Number: 2, SeasonNumber: sn, Title: "e2", Monitored: true, HasFile: false,
			AirDateUTC: time.Now().UTC().Add(-7 * 24 * time.Hour)},
		{ID: 3, Number: 3, SeasonNumber: sn, Title: "e3", Monitored: true, HasFile: false,
			AirDateUTC: time.Now().UTC().Add(-1 * 24 * time.Hour)},
	}, nil
}

func (f *abortFakeSonarr) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	atomic.AddInt64(&f.listFilesCalls, 1)
	return map[int]int{}, nil
}

func (f *abortFakeSonarr) SearchReleases(_ context.Context, sID, sn int) ([]release.Release, error) {
	atomic.AddInt64(&f.searchCalls, 1)
	return []release.Release{{
		GUID:                 "g",
		Title:                "T",
		IndexerID:            1,
		IndexerName:          "RT",
		Protocol:             "torrent",
		QualityID:            5,
		QualityName:          "WEB-1080p",
		CustomFormatScore:    1000,
		Seeders:              50,
		MappedEpisodeNumbers: []int{2, 3},
		MappedSeasonNumber:   sn,
		IsFullSeason:         false,
	}}, nil
}

func (f *abortFakeSonarr) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	atomic.AddInt64(&f.qualityProfCalls, 1)
	return ports.QualityProfile{
		ID: 14, Name: "WEB-1080p",
		Items: []ports.QualityItem{{ID: 5, Name: "WEB-1080p", Order: 1}},
	}, nil
}

func (f *abortFakeSonarr) ListIndexers(_ context.Context) ([]ports.Indexer, error) {
	atomic.AddInt64(&f.listIndexersCalls, 1)
	return nil, nil
}
func (f *abortFakeSonarr) ListTags(_ context.Context) ([]ports.Tag, error) {
	atomic.AddInt64(&f.listTagsCalls, 1)
	return nil, nil
}
func (f *abortFakeSonarr) GrabHistory(_ context.Context, _ int) ([]ports.HistoryEvent, error) {
	atomic.AddInt64(&f.grabHistoryCalls, 1)
	return nil, nil
}

// ForceGrab fails on every call so the scan loop accumulates consecutive
// grab failures and trips the 3-in-a-row threshold.
func (f *abortFakeSonarr) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	atomic.AddInt64(&f.forceGrabCalls, 1)
	return "", errors.New("forced failure")
}

// abortFakeScanRepo records the final status of the scan record.
type abortFakeScanRepo struct {
	mu     sync.Mutex
	create ports.ScanRecord
	update ports.ScanRecord
}

func (r *abortFakeScanRepo) Create(_ context.Context, rec ports.ScanRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.create = rec
	return nil
}

func (r *abortFakeScanRepo) Update(_ context.Context, rec ports.ScanRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.update = rec
	return nil
}

func (r *abortFakeScanRepo) GetByID(_ context.Context, _ uuid.UUID) (ports.ScanRecord, error) {
	return ports.ScanRecord{}, errors.New("not implemented")
}

func (r *abortFakeScanRepo) MarkAborted(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (r *abortFakeScanRepo) List(_ context.Context, _ ports.ScanFilter, _ ports.Pagination) ([]ports.ScanRecord, *ports.Cursor, error) {
	panic("fake List unexpectedly called - this stub is not configured for List queries")
}

func (r *abortFakeScanRepo) FinalStatus() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.update.Status
}

// abortFakeDecisionRepo discards decisions.
type abortFakeDecisionRepo struct{}

func (abortFakeDecisionRepo) Save(_ context.Context, _ decision.Decision) error { return nil }
func (abortFakeDecisionRepo) List(_ context.Context, _ ports.DecisionFilter, _ ports.Pagination) ([]decision.Decision, *ports.Cursor, error) {
	panic("fake List unexpectedly called - this stub is not configured for List queries")
}

// abortFakeGrabRepo discards grab records.
type abortFakeGrabRepo struct{}

func (abortFakeGrabRepo) Create(_ context.Context, _ domaingrab.Record) error { return nil }
func (abortFakeGrabRepo) RecentFailedGUIDs(_ context.Context, _ string, _ int, _ int, _ time.Time) ([]string, error) {
	return nil, nil
}
func (abortFakeGrabRepo) List(_ context.Context, _ ports.GrabFilter, _ ports.Pagination) ([]domaingrab.Record, *ports.Cursor, error) {
	panic("fake List unexpectedly called - this stub is not configured for List queries")
}

func (abortFakeGrabRepo) MatchLatest(_ context.Context, _ ports.MatchKey) (domaingrab.Record, error) {
	panic("fake MatchLatest unexpectedly called - this stub is not configured for MatchLatest queries")
}

func (abortFakeGrabRepo) UpdateStatus(_ context.Context, _ uuid.UUID, _ domaingrab.Status, _ string) error {
	panic("fake UpdateStatus unexpectedly called - this stub is not configured for UpdateStatus calls")
}

// abortFakeCooldownRepo lets every guid/series pass.
type abortFakeCooldownRepo struct{}

func (abortFakeCooldownRepo) Set(_ context.Context, _ cooldown.Cooldown) error { return nil }
func (abortFakeCooldownRepo) Get(_ context.Context, _ cooldown.Scope, _ string) (cooldown.Cooldown, bool, error) {
	return cooldown.Cooldown{}, false, nil
}
func (abortFakeCooldownRepo) FilterActive(_ context.Context, _ cooldown.Scope, _ []string, _ time.Time) ([]cooldown.Cooldown, error) {
	return nil, nil
}
func (abortFakeCooldownRepo) Sweep(_ context.Context, _ time.Time) (int64, error) { return 0, nil }

// abortFakeOriginRepo is empty.
type abortFakeOriginRepo struct{}

func (abortFakeOriginRepo) Upsert(_ context.Context, _ ports.OriginRelease) error { return nil }
func (abortFakeOriginRepo) Get(_ context.Context, _ string, _, _ int) (ports.OriginRelease, bool, error) {
	return ports.OriginRelease{}, false, nil
}

// fakeHealth tracks MarkUnavailable calls.
type fakeHealth struct {
	mu          sync.Mutex
	state       instance.Health
	unavailMsg  string
	transitions int
}

func newFakeHealth(initial instance.Health) *fakeHealth {
	return &fakeHealth{state: initial}
}

func (h *fakeHealth) Get(_ string) (instance.Snapshot, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return instance.Snapshot{Health: h.state}, true
}

func (h *fakeHealth) MarkUnavailable(_ string, state instance.Health, lastErr string, _ time.Time) (instance.Health, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	from := h.state
	h.state = state
	h.unavailMsg = lastErr
	h.transitions++
	return from, from != state
}

func (h *fakeHealth) State() instance.Health {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state
}

func (h *fakeHealth) Message() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.unavailMsg
}

// classifier — Grab path needs a classifier; use a permissive one that
// classifies the test error as "transient" so the retry loop runs through
// its budget on every grab.
type permClassifier struct{}

func (permClassifier) IsTransient(_ error) bool { return true }
func (permClassifier) Is4xx(_ error) bool       { return false }

// TestScan_AbortsAfterThreeConsecutiveGrabFails — H-4. Confirms the existing
// 3-consecutive-grab_failed abort path now also transitions instance health
// to HealthUnavailableUnknown (D-2.4).
func TestScan_AbortsAfterThreeConsecutiveGrabFails(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	sonarr := &abortFakeSonarr{name: "main"}
	scanRepo := &abortFakeScanRepo{}
	decRepo := abortFakeDecisionRepo{}
	cdRepo := abortFakeCooldownRepo{}
	origRepo := abortFakeOriginRepo{}
	grabRepo := abortFakeGrabRepo{}

	evaluator := evaluate.NewUseCase(sonarr, decRepo, logger)
	grabUC := grab.NewUseCase(grabRepo, cdRepo, origRepo, permClassifier{}, logger).
		WithSleeper(func(_ context.Context, _ time.Duration) error { return nil })

	health := newFakeHealth(instance.HealthAvailable)

	uc := NewUseCase(
		[]Instance{{
			Config: config.SonarrInstance{
				Name: "main",
				Tags: config.TagsConfig{},
				Search: config.SearchConfig{
					SkipSpecials: true,
					SkipAnime:    true,
				},
				Ranking: config.RankingConfig{OriginBonus: 1.0},
				Retry: config.RetryConfig{
					MaxAttempts: 1, // each grab attempt fails outright, no retry
				},
				Limits: config.LimitsConfig{MaxGrabsPerScan: 10, ScanMaxSeries: 10},
				Cooldown: config.CooldownConfig{
					SeriesAfterGrab:     24 * time.Hour,
					GUIDAfterFailedGrab: 72 * time.Hour,
				},
			},
			Client: sonarr,
		}},
		evaluator,
		scanRepo,
		logger,
		false, // dryRun = false so real-grab path is exercised
	).
		WithGrabUseCase(grabUC).
		WithCooldowns(cdRepo).
		WithOrigins(origRepo).
		WithHealthRegistry(health)

	results, _ := uc.Run(context.Background(), TriggerManual)
	require.Len(t, results, 1)
	// The scan should complete even if it ends with 3 consecutive failures
	// because the completion is due to the abort, not a context error
	assert.Equal(t, "failed", results[0].Status, "scan must finalize as failed after 3 consecutive grab_failed")
	assert.GreaterOrEqual(t, results[0].GrabsFailed, 3, "at least 3 grab attempts must have failed")
	assert.Equal(t, "failed", scanRepo.FinalStatus(), "scan record persisted with status=failed")
	assert.Equal(t, instance.HealthUnavailableUnknown, health.State(), "D-2.4: instance must transition to UnavailableUnknown")
	assert.Contains(t, health.Message(), "3 consecutive grab_failed")
	assert.Equal(t, 1, health.transitions, "D-2.4: MarkUnavailable must fire exactly once per scan")
}

// TestScan_PreflightGate_SkipsUnavailable — pre-scan gate test. When the
// registry already reports HealthUnavailableAuth, runOne returns "skipped"
// without touching the Sonarr client. Deferred-item #4: explicitly assert
// total Sonarr-method invocations == 0 instead of relying on the stateless
// fake's implicit silence.
func TestScan_PreflightGate_SkipsUnavailable(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sonarr := &abortFakeSonarr{name: "main"}
	scanRepo := &abortFakeScanRepo{}
	decRepo := abortFakeDecisionRepo{}
	evaluator := evaluate.NewPerInstanceUseCase(decRepo, logger)

	health := newFakeHealth(instance.HealthUnavailableAuth)

	uc := NewUseCase(
		[]Instance{{
			Config: config.SonarrInstance{Name: "main"},
			Client: sonarr,
		}},
		evaluator,
		scanRepo,
		logger,
		false,
	).WithHealthRegistry(health)

	results, err := uc.Run(context.Background(), TriggerManual)
	require.Error(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "skipped", results[0].Status)
	assert.True(t, errors.Is(err, domain.ErrInstanceUnavailable))
	// The scan was skipped before scanRepo.Create was called.
	assert.Empty(t, scanRepo.FinalStatus())
	// Deferred-item #4: assert NO Sonarr method was invoked during the gated scan.
	assert.EqualValues(t, 0, sonarr.totalCalls(), "preflight gate must not call any Sonarr method")
}

// TestScan_MidScanAuthAbort — when ListSeries returns ErrInstanceUnauthorized,
// the scan finalizes as aborted and the instance transitions to UnavailableAuth.
// Wrap the error properly using errors.Join so errors.Is(err, sentinel) matches.
type authFailFakeSonarrWrapped struct{ name string }

func (f *authFailFakeSonarrWrapped) Name() string { return f.name }
func (f *authFailFakeSonarrWrapped) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{Version: "x"}, nil
}
func (f *authFailFakeSonarrWrapped) ListSeries(_ context.Context) ([]series.Series, error) {
	return nil, errors.Join(errors.New("401 from sonarr"), domain.ErrInstanceUnauthorized)
}
func (f *authFailFakeSonarrWrapped) GetSeries(_ context.Context, _ int) (series.Series, error) {
	return series.Series{}, nil
}
func (f *authFailFakeSonarrWrapped) ListEpisodes(_ context.Context, _, _ int) ([]series.Episode, error) {
	return nil, nil
}
func (f *authFailFakeSonarrWrapped) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return nil, nil
}
func (f *authFailFakeSonarrWrapped) SearchReleases(_ context.Context, _, _ int) ([]release.Release, error) {
	return nil, nil
}
func (f *authFailFakeSonarrWrapped) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{}, nil
}
func (f *authFailFakeSonarrWrapped) ListIndexers(_ context.Context) ([]ports.Indexer, error) {
	return nil, nil
}
func (f *authFailFakeSonarrWrapped) ListTags(_ context.Context) ([]ports.Tag, error) { return nil, nil }
func (f *authFailFakeSonarrWrapped) GrabHistory(_ context.Context, _ int) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (f *authFailFakeSonarrWrapped) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	return "", nil
}

func TestScan_MidScanAuthAbort(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	scanRepo := &abortFakeScanRepo{}
	decRepo := abortFakeDecisionRepo{}
	evaluator := evaluate.NewPerInstanceUseCase(decRepo, logger)
	health := newFakeHealth(instance.HealthAvailable)

	uc := NewUseCase(
		[]Instance{{
			Config: config.SonarrInstance{Name: "main"},
			Client: &authFailFakeSonarrWrapped{name: "main"},
		}},
		evaluator,
		scanRepo,
		logger,
		false,
	).WithHealthRegistry(health)

	results, err := uc.Run(context.Background(), TriggerManual)
	require.Error(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "aborted", results[0].Status)
	assert.Equal(t, "aborted", scanRepo.FinalStatus())
	assert.Equal(t, instance.HealthUnavailableAuth, health.State())
	assert.Equal(t, 1, health.transitions, "MarkUnavailable must fire exactly once on auth abort")
}
