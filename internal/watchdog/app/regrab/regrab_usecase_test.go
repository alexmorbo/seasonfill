package regrab_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/release"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	appgrab "github.com/alexmorbo/seasonfill/internal/grab/app"
	"github.com/alexmorbo/seasonfill/internal/grab/app/evaluate"
	domaingrab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	"github.com/alexmorbo/seasonfill/internal/grab/domain/decision"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/qbit"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/app/regrab"
	"github.com/alexmorbo/seasonfill/internal/watchdog/app/regrab/mocks"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
	domainregrab "github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

const (
	testInstance domain.InstanceName = "alpha"
)

const (
	testHash   = "abcdef0123456789abcdef0123456789abcdef01"
	testSeries = domain.SonarrSeriesID(122)
	testSeason = 2
)

func enabledSettings() regrab.Settings {
	return regrab.Settings{
		InstanceID:             7,
		InstanceName:           testInstance,
		Enabled:                true,
		URL:                    "http://qbit.local:8080",
		Username:               "admin",
		PasswordPlaintext:      "hunter2",
		Category:               "sonarr",
		PollInterval:           30 * time.Minute,
		RegrabCooldown:         120 * time.Hour,
		MaxConsecutiveNoBetter: 3,
		CustomUnregisteredMsgs: []string{"deleted"},
	}
}

func successGrab() domaingrab.Record {
	h := domain.QbitHash(testHash)
	return domaingrab.Record{
		ID:           uuid.New(),
		InstanceName: testInstance,
		SeriesID:     testSeries,
		SeriesTitle:  "Test Series",
		SeasonNumber: testSeason,
		ReleaseGUID:  "guid-original",
		ReleaseTitle: "Original Release",
		IndexerID:    1,
		IndexerName:  "indexer-x",
		Status:       domaingrab.StatusGrabbed,
		TorrentHash:  &h,
	}
}

func unregisteredVerdict() qbit.DetectionResult {
	return qbit.DetectionResult{
		Hash:         testHash,
		Unregistered: true,
		TrackerMsg:   "Torrent not registered with this tracker",
		TrackerURL:   "http://tracker.example.com/announce",
	}
}

func aliveVerdict() qbit.DetectionResult {
	return qbit.DetectionResult{Hash: testHash}
}

func successDecision() decision.Decision {
	rel := release.Release{
		GUID:              "guid-replacement",
		Title:             "Replacement Release",
		IndexerID:         1,
		IndexerName:       "indexer-x",
		CustomFormatScore: 200,
		QualityName:       "WEB-DL 1080p",
	}
	scored := release.Scored{Release: rel}
	return decision.Decision{
		ID:           uuid.New(),
		Outcome:      decision.OutcomeGrab,
		SeriesID:     testSeries,
		SeasonNumber: testSeason,
		Selected:     &scored,
	}
}

func nothingBetterDecision() decision.Decision {
	return decision.Decision{
		ID:           uuid.New(),
		Outcome:      decision.OutcomeSkip,
		Reason:       decision.ReasonSkipNoMissing,
		SeriesID:     testSeries,
		SeasonNumber: testSeason,
	}
}

// fakeSonarr implements ports.SonarrClient with just-enough surface for
// the evaluator's reads. Used inside scan.Instance so the regrab use
// case can call inst.Client.GetSeries etc.
type fakeSonarr struct{}

func (fakeSonarr) SystemStatus(ctx context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{}, nil
}
func (fakeSonarr) ListSeries(ctx context.Context) ([]series.Series, error) {
	return nil, nil
}
func (fakeSonarr) ListSeriesCache(ctx context.Context, instanceName domain.InstanceName) ([]series.CacheEntry, error) {
	return nil, nil
}
func (fakeSonarr) GetSeries(ctx context.Context, id domain.SonarrSeriesID) (series.Series, error) {
	return series.Series{ID: id, Title: "Test Series", QualityProfile: 1}, nil
}
func (fakeSonarr) Name() string {
	return "fake"
}
func (fakeSonarr) ListEpisodes(ctx context.Context, id domain.SonarrSeriesID, season int) ([]series.Episode, error) {
	return []series.Episode{{Number: 1, AirDateUTC: time.Now().Add(-time.Hour)}}, nil
}
func (fakeSonarr) ListEpisodesBySeries(_ context.Context, _ domain.SonarrSeriesID) ([]series.Episode, error) {
	return nil, nil
}
func (fakeSonarr) ListEpisodeFiles(ctx context.Context, id domain.SonarrSeriesID) (map[int]int, error) {
	return map[int]int{}, nil
}
func (fakeSonarr) ListEpisodeFilesBySeason(ctx context.Context, id domain.SonarrSeriesID, season int) ([]ports.EpisodeFileDetail, error) {
	return nil, nil
}
func (fakeSonarr) GetQualityProfile(ctx context.Context, id int) (ports.QualityProfile, error) {
	return ports.QualityProfile{ID: id}, nil
}
func (fakeSonarr) SearchReleases(ctx context.Context, id domain.SonarrSeriesID, s int) ([]release.Release, error) {
	return nil, nil
}
func (fakeSonarr) ForceGrab(ctx context.Context, guid string, indexerID int) (string, error) {
	return "", nil
}
func (fakeSonarr) ListIndexers(ctx context.Context) ([]ports.Indexer, error) {
	return nil, nil
}
func (fakeSonarr) ListTags(ctx context.Context) ([]ports.Tag, error) {
	return nil, nil
}
func (fakeSonarr) GrabHistory(ctx context.Context, id domain.SonarrSeriesID) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (fakeSonarr) ParseRelease(ctx context.Context, title string) (ports.ParseResult, error) {
	return ports.ParseResult{}, nil
}

func makeUC(t *testing.T, ctrl *gomock.Controller) (
	*regrab.UseCase,
	*mocks.MockSettingsLookup,
	*mocks.MockInstanceRegistry,
	*mocks.MockQbitClientFactory,
	*mocks.MockDetectorFactory,
	*mocks.MockDetector,
	*mocks.MockClient,
	*mocks.MockGrabRepository,
	*mocks.MockCooldownRepository,
	*mocks.MockWatchdogBlacklistRepository,
	*mocks.MockNoBetterCounterRepository,
	*mocks.MockEvaluateExecutor,
	*mocks.MockGrabExecutor,
	*mocks.MockMetrics,
) {
	t.Helper()
	settings := mocks.NewMockSettingsLookup(ctrl)
	instances := mocks.NewMockInstanceRegistry(ctrl)
	qbitFac := mocks.NewMockQbitClientFactory(ctrl)
	detFac := mocks.NewMockDetectorFactory(ctrl)
	det := mocks.NewMockDetector(ctrl)
	qclient := mocks.NewMockClient(ctrl)
	grabs := mocks.NewMockGrabRepository(ctrl)
	cooldowns := mocks.NewMockCooldownRepository(ctrl)
	bl := mocks.NewMockWatchdogBlacklistRepository(ctrl)
	cnt := mocks.NewMockNoBetterCounterRepository(ctrl)
	ev := mocks.NewMockEvaluateExecutor(ctrl)
	gx := mocks.NewMockGrabExecutor(ctrl)
	mt := mocks.NewMockMetrics(ctrl)

	uc := regrab.NewUseCase(settings, instances, qbitFac, detFac,
		grabs, cooldowns, bl, cnt, ev, gx, nil).WithMetrics(mt)

	// Wire any common stubs.
	return uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, bl, cnt, ev, gx, mt
}

func TestRunInstance_DisabledSettings_SkipsEarly(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, _, _, _, _, _, _, _, _, _, _, _, _ := makeUC(t, ctrl)
	s := enabledSettings()
	s.Enabled = false
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, testInstance, res.InstanceName)
	assert.Zero(t, res.TorrentsSeen)
}

func TestRunInstance_SettingsNotFound_SkipsEarly(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, _, _, _, _, _, _, _, _, _, _, _, _ := makeUC(t, ctrl)
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(regrab.Settings{}, ports.ErrNotFound)

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, testInstance, res.InstanceName)
}

func TestRunInstance_QbitClientError_UpdatesStreakAndReturns(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, _, qbitFac, _, _, _, _, _, _, _, _, _, mt := makeUC(t, ctrl)
	s := enabledSettings()
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	bootErr := errors.New("dial qbit: connection refused")
	qbitFac.EXPECT().NewClient(s).Return(nil, bootErr)
	mt.EXPECT().IncPollResult(testInstance, "qbit_error")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.ErrorIs(t, res.QbitError, bootErr)
}

func TestRunInstance_TorrentHashNotInGrabs_Skips(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, _, qclient, grabs, _, _, _, _, _, mt := makeUC(t, ctrl)
	s := enabledSettings()
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(mocks.NewMockDetector(ctrl))
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: fakeSonarr{}}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(domaingrab.Record{}, ports.ErrNotFound)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 1, res.TorrentsSeen)
	assert.Zero(t, res.UnregisteredCount)
}

func TestRunInstance_DetectorSaysAlive_Skips(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, _, _, _, _, _, mt := makeUC(t, ctrl)
	s := enabledSettings()
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: fakeSonarr{}}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(successGrab(), nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(aliveVerdict(), nil)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 1, res.TorrentsSeen)
	assert.Zero(t, res.UnregisteredCount)
}

func TestRunInstance_CooldownActive_Skips(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, _, _, _, _, mt := makeUC(t, ctrl)
	fixedNow := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	uc.WithClock(func() time.Time { return fixedNow })
	s := enabledSettings()
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: fakeSonarr{}}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(successGrab(), nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cdKey := cooldown.SeriesKey(testInstance, testSeries, testSeason)
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, cdKey).Return(
		cooldown.Cooldown{Scope: cooldown.ScopeRegrabRetry, Key: cdKey,
			ExpiresAt: fixedNow.Add(time.Hour)}, true, nil)
	mt.EXPECT().IncRegrabResult(testInstance, "skip_cooldown")
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 1, res.UnregisteredCount)
	assert.Equal(t, 1, res.SkippedCooldown)
}

func TestRunInstance_Blacklisted_Skips(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, bl, _, _, _, mt := makeUC(t, ctrl)
	s := enabledSettings()
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: fakeSonarr{}}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(successGrab(), nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(
		domainregrab.BlacklistEntry{InstanceID: s.InstanceID}, nil)
	mt.EXPECT().IncRegrabResult(testInstance, "skip_blacklist")
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 1, res.SkippedBlacklist)
}

func TestRunInstance_EvaluateGrab_HappyPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, bl, cnt, ev, gx, mt := makeUC(t, ctrl)
	fixedNow := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	uc.WithClock(func() time.Time { return fixedNow })
	s := enabledSettings()
	orig := successGrab()
	orig.ReleaseGUID = "" // skip 114 replay path — exercise evaluator branch
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{
		Client: fakeSonarr{},
	}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)

	// Evaluator returns OutcomeGrab.
	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(successDecision(), nil)
	// Grab executor returns success with a fresh record id.
	newID := uuid.New()
	gx.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(appgrab.Output{
		Record: domaingrab.Record{ID: newID, InstanceName: testInstance,
			SeriesID: testSeries, SeasonNumber: testSeason},
		Attempts: 1,
	})
	// Audit stamp.
	grabs.EXPECT().SetReplayOfID(gomock.Any(), newID, orig.ID).Return(nil)
	// Counter reset on success.
	cnt.EXPECT().Reset(gomock.Any(), s.InstanceID, testSeries, testSeason, gomock.Any()).Return(nil)
	mt.EXPECT().IncRegrabResult(testInstance, "grabbed")
	// Cooldown activate.
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 1, res.RegrabbedCount)
	assert.Equal(t, 1, res.UnregisteredCount)
}

func TestRunInstance_NothingBetter_IncrementsCounterAndMaybeBlacklists(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, bl, cnt, ev, _, mt := makeUC(t, ctrl)
	s := enabledSettings()
	orig := successGrab()
	orig.ReleaseGUID = "" // skip 114 replay path — exercise evaluator branch
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: fakeSonarr{}}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)
	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(nothingBetterDecision(), nil)
	// Counter increment crosses the threshold → blacklist + reset.
	threshold := s.MaxConsecutiveNoBetter
	cnt.EXPECT().Increment(gomock.Any(), s.InstanceID, testSeries, testSeason, gomock.Any()).Return(
		domainregrab.NoBetterCounter{
			InstanceID:   s.InstanceID,
			SeriesID:     testSeries,
			SeasonNumber: testSeason,
			Consecutive:  threshold,
		}, nil)
	bl.EXPECT().Upsert(gomock.Any(), gomock.Any()).Return(nil)
	cnt.EXPECT().Reset(gomock.Any(), s.InstanceID, testSeries, testSeason, gomock.Any()).Return(nil)
	mt.EXPECT().IncRegrabResult(testInstance, "nothing_better")
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 1, res.NothingBetterCount)
	require.Len(t, res.BlacklistedThisCycle, 1)
	assert.Equal(t, testSeries, res.BlacklistedThisCycle[0].SeriesID)
}

func TestRunInstance_DualTorrentSameTriple_EvaluatesOnce(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, bl, cnt, ev, _, mt := makeUC(t, ctrl)
	s := enabledSettings()
	orig := successGrab()
	orig.ReleaseGUID = "" // skip 114 replay path — exercise evaluator branch
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	// Two torrents, different hashes, both map to the same grab.
	hashA := testHash
	hashB := "fedcba9876543210fedcba9876543210fedcba98"
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: hashA, Category: "sonarr"},
		{Hash: hashB, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: fakeSonarr{}}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), hashA).Return(orig, nil)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), hashB).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), hashA).Return(unregisteredVerdict(), nil)
	det.EXPECT().Detect(gomock.Any(), hashB).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, gomock.Any()).Times(2)
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)
	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(nothingBetterDecision(), nil).Times(1)
	cnt.EXPECT().Increment(gomock.Any(), s.InstanceID, testSeries, testSeason, gomock.Any()).Return(
		domainregrab.NoBetterCounter{Consecutive: 1}, nil).Times(1)
	mt.EXPECT().IncRegrabResult(testInstance, "nothing_better").Times(1)
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 2, res.UnregisteredCount, "both torrents counted as unregistered")
	assert.Equal(t, 1, res.NothingBetterCount, "but evaluator was invoked only once")
}

func TestRunInstance_EvaluateError_CountsErrorActivatesCooldown(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, bl, _, ev, _, mt := makeUC(t, ctrl)
	s := enabledSettings()
	orig := successGrab()
	orig.ReleaseGUID = "" // skip 114 replay path — exercise evaluator branch
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: fakeSonarr{}}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)
	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(decision.Decision{}, errors.New("sonarr 503"))
	mt.EXPECT().IncRegrabResult(testInstance, "error")
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 1, res.ErrorCount)
}

func TestRunInstance_InstanceNotInRegistry_ReturnsError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, _, qclient, _, _, _, _, _, _, _ := makeUC(t, ctrl)
	s := enabledSettings()
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(mocks.NewMockDetector(ctrl))
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{}, false)

	_, err := uc.RunInstance(context.Background(), testInstance)
	require.Error(t, err)
	assert.ErrorIs(t, err, regrab.ErrUnknownInstance)
}

// TestRunInstance_DebugInstrumentation_EmitsKeysAtCheckpoints replays the
// happy-path scenario (one unregistered torrent → grab outcome) under a
// debug-level slog.JSONHandler and asserts every one of the 7 checkpoint
// keys appears in the captured output. This guards against silent drift
// of the debug instrumentation as the pipeline evolves.
func TestRunInstance_DebugInstrumentation_EmitsKeysAtCheckpoints(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	buf := &bytes.Buffer{}
	debugLogger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	settings := mocks.NewMockSettingsLookup(ctrl)
	instances := mocks.NewMockInstanceRegistry(ctrl)
	qbitFac := mocks.NewMockQbitClientFactory(ctrl)
	detFac := mocks.NewMockDetectorFactory(ctrl)
	det := mocks.NewMockDetector(ctrl)
	qclient := mocks.NewMockClient(ctrl)
	grabs := mocks.NewMockGrabRepository(ctrl)
	cooldowns := mocks.NewMockCooldownRepository(ctrl)
	bl := mocks.NewMockWatchdogBlacklistRepository(ctrl)
	cnt := mocks.NewMockNoBetterCounterRepository(ctrl)
	ev := mocks.NewMockEvaluateExecutor(ctrl)
	gx := mocks.NewMockGrabExecutor(ctrl)
	mt := mocks.NewMockMetrics(ctrl)

	uc := regrab.NewUseCase(settings, instances, qbitFac, detFac,
		grabs, cooldowns, bl, cnt, ev, gx, debugLogger).WithMetrics(mt)

	s := enabledSettings()
	orig := successGrab()
	orig.ReleaseGUID = "" // skip 114 replay path — exercise evaluator branch

	// Two torrents: one we own + one orphan. The orphan covers the
	// grab_lookup_miss path; the owned one covers the rest. Both also
	// land in the bounded sample_hashes slice.
	orphanHash := "1111111111111111111111111111111111111111"

	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr", State: "stalledDL", Name: "Owned.Torrent"},
		{Hash: orphanHash, Category: "sonarr", State: "downloading", Name: "Orphan.Torrent"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: fakeSonarr{}}, true)

	// Owned torrent → HIT, unregistered, passes both gates, evaluator grabs.
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)
	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(successDecision(), nil)
	newID := uuid.New()
	gx.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(appgrab.Output{
		Record: domaingrab.Record{ID: newID, InstanceName: testInstance,
			SeriesID: testSeries, SeasonNumber: testSeason},
		Attempts: 1,
	})
	grabs.EXPECT().SetReplayOfID(gomock.Any(), newID, orig.ID).Return(nil)
	cnt.EXPECT().Reset(gomock.Any(), s.InstanceID, testSeries, testSeason, gomock.Any()).Return(nil)
	mt.EXPECT().IncRegrabResult(testInstance, "grabbed")
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)

	// Orphan torrent → MISS, loop continues.
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), orphanHash).Return(domaingrab.Record{}, ports.ErrNotFound)

	mt.EXPECT().IncPollResult(testInstance, "ok")

	_, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)

	// Decode the JSON log stream and collect every "msg" key emitted.
	msgs := collectLogMsgs(t, buf.Bytes())

	wantKeys := []string{
		"regrab_torrents_listed",
		"regrab_iter_torrent",
		"regrab_grab_lookup_hit",
		"regrab_grab_lookup_miss",
		"regrab_verdict",
		"regrab_cooldown_passed",
		"regrab_blacklist_passed",
		"regrab_evaluated",
	}
	for _, key := range wantKeys {
		assert.Contains(t, msgs, key, "expected debug key %q in log stream", key)
	}
}

// collectLogMsgs decodes a buffer of newline-delimited JSON log records
// and returns the set of msg values seen. Used by the debug-checkpoint
// test above.
func collectLogMsgs(t *testing.T, buf []byte) map[string]struct{} {
	t.Helper()
	msgs := map[string]struct{}{}
	dec := json.NewDecoder(bytes.NewReader(buf))
	for dec.More() {
		var entry map[string]any
		if err := dec.Decode(&entry); err != nil {
			t.Fatalf("decode log entry: %v", err)
		}
		if m, ok := entry["msg"].(string); ok {
			msgs[m] = struct{}{}
		}
	}
	return msgs
}

// Reuse evaluate.Input type in test imports — keeps the import alive
// in case mockgen output drops the indirect dep.
var _ = evaluate.Input{}

// TestRunInstance_ReplayByGUID_Success — unregistered verdict with a
// valid GUID + IndexerID. ForceGrab succeeds. The use case must skip
// the evaluator entirely, persist a synthetic Decision row, and run
// the existing grab branch (which produces a new grab_records row +
// SetReplayOfID).
func TestRunInstance_ReplayByGUID_Success(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, bl, cnt, ev, gx, mt := makeUC(t, ctrl)
	decisionsRepo := mocks.NewMockDecisionRepository(ctrl)
	uc.WithDecisions(decisionsRepo)

	s := enabledSettings()
	orig := successGrab()
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)

	// Wire a Sonarr stub whose ForceGrab succeeds and records the call.
	var gotGUID string
	var gotIndexerID int
	forceGrabCalls := 0
	sonarrStub := replayingSonarr{
		forceGrab: func(_ context.Context, guid string, indexerID int) (string, error) {
			forceGrabCalls++
			gotGUID = guid
			gotIndexerID = indexerID
			return "", nil
		},
	}
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: sonarrStub}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)

	// Replay path persists a synthetic decision row — capture it.
	var saved decision.Decision
	decisionsRepo.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, d decision.Decision) error {
		saved = d
		return nil
	})

	// Evaluator must NOT be called on the replay-success path.
	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Times(0)

	// Grab executor runs as if a fresh candidate was picked.
	newID := uuid.New()
	gx.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(appgrab.Output{
		Record: domaingrab.Record{ID: newID, InstanceName: testInstance,
			SeriesID: testSeries, SeasonNumber: testSeason},
		Attempts: 1,
	})
	grabs.EXPECT().SetReplayOfID(gomock.Any(), newID, orig.ID).Return(nil)
	cnt.EXPECT().Reset(gomock.Any(), s.InstanceID, testSeries, testSeason, gomock.Any()).Return(nil)
	mt.EXPECT().IncRegrabResult(testInstance, "grabbed")
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 1, res.RegrabbedCount, "replay success counts as a regular regrab")
	assert.Equal(t, 1, forceGrabCalls, "ForceGrab MUST be called exactly once")
	assert.Equal(t, orig.ReleaseGUID, gotGUID)
	assert.Equal(t, orig.IndexerID, gotIndexerID)

	require.NotNil(t, saved.Intent, "synthetic decision row MUST carry an Intent")
	assert.Equal(t,
		decision.ChosenBecauseWatchdogReplayUnregistered,
		saved.Intent.ChosenBecause,
		"intent.chosen_because must mark the replay path")
	assert.Contains(t, saved.Intent.ChosenReasonDetail, orig.ID.String(),
		"intent detail must mention the parent grab id for audit")
	require.NotNil(t, saved.Selected, "synthetic decision must carry a Selected so runGrab works")
	assert.Equal(t, orig.ReleaseGUID, saved.Selected.Release.GUID,
		"synthetic Selected must reuse the original GUID")
	assert.Equal(t, decision.OutcomeGrab, saved.Outcome)
	assert.Equal(t, uuid.Nil, saved.ScanRunID,
		"121b §B: replay path persists NULL scan_run_id")
}

// TestRunInstance_ReplayByGUID_ReleaseGone_FallsThroughToEvaluator —
// ForceGrab returns 404. The use case must call the evaluator as if
// no replay step existed.
func TestRunInstance_ReplayByGUID_ReleaseGone_FallsThroughToEvaluator(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, bl, cnt, ev, _, mt := makeUC(t, ctrl)
	s := enabledSettings()
	orig := successGrab()
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)

	// ForceGrab → 404 release gone.
	forceGrabCalls := 0
	sonarrStub := replayingSonarr{
		forceGrab: func(_ context.Context, _ string, _ int) (string, error) {
			forceGrabCalls++
			return "", &sonarrStatusError404{}
		},
	}
	// Inject a release-gone classifier so the use case classifies our
	// stub error as "release gone" without importing the real sonarr
	// package types into the test.
	uc.WithReleaseGoneClassifier(func(err error) bool {
		var target *sonarrStatusError404
		return errors.As(err, &target)
	})

	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: sonarrStub}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)

	// Evaluator runs (fall-through). Return nothing-better → counter
	// path + cooldown set.
	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(nothingBetterDecision(), nil)
	cnt.EXPECT().Increment(gomock.Any(), s.InstanceID, testSeries, testSeason, gomock.Any()).Return(
		domainregrab.NoBetterCounter{Consecutive: 1}, nil)
	mt.EXPECT().IncRegrabResult(testInstance, "nothing_better")
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 1, forceGrabCalls, "ForceGrab must be tried before fall-through")
	assert.Equal(t, 1, res.NothingBetterCount,
		"after 404 fall-through, evaluator's verdict drives the outcome")
}

// TestRunInstance_ReplayByGUID_TransientError_SurfacesAsError —
// ForceGrab returns 503. The use case must NOT fall through; it
// surfaces OutcomeError and the cooldown fires.
func TestRunInstance_ReplayByGUID_TransientError_SurfacesAsError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, bl, _, ev, _, mt := makeUC(t, ctrl)
	s := enabledSettings()
	orig := successGrab()
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)

	sonarrStub := replayingSonarr{
		forceGrab: func(_ context.Context, _ string, _ int) (string, error) {
			return "", errors.New("sonarr 503 service unavailable")
		},
	}
	// Default classifier returns false for plain errors → surface.
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: sonarrStub}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)

	// Evaluator MUST NOT be called.
	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Times(0)

	mt.EXPECT().IncRegrabResult(testInstance, "error")
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 1, res.ErrorCount, "transient ForceGrab error counts as error, not replay miss")
}

// TestRunInstance_ReplayByGUID_NoGUID_SkipsReplayPath — grab row has
// empty ReleaseGUID. Replay step is skipped; evaluator runs.
func TestRunInstance_ReplayByGUID_NoGUID_SkipsReplayPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, bl, cnt, ev, _, mt := makeUC(t, ctrl)
	s := enabledSettings()
	orig := successGrab()
	orig.ReleaseGUID = "" // <- no GUID, replay path must be skipped

	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)

	forceGrabCalls := 0
	sonarrStub := replayingSonarr{
		forceGrab: func(_ context.Context, _ string, _ int) (string, error) {
			forceGrabCalls++
			return "", nil
		},
	}
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: sonarrStub}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)
	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(nothingBetterDecision(), nil)
	cnt.EXPECT().Increment(gomock.Any(), s.InstanceID, testSeries, testSeason, gomock.Any()).Return(
		domainregrab.NoBetterCounter{Consecutive: 1}, nil)
	mt.EXPECT().IncRegrabResult(testInstance, "nothing_better")
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	_, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 0, forceGrabCalls, "empty GUID must skip the replay path entirely")
}

// TestRunInstance_ReplayByGUID_NoIndexerID_SkipsReplayPath — grab row
// has IndexerID == 0. Replay step is skipped; evaluator runs.
func TestRunInstance_ReplayByGUID_NoIndexerID_SkipsReplayPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, bl, cnt, ev, _, mt := makeUC(t, ctrl)
	s := enabledSettings()
	orig := successGrab()
	orig.IndexerID = 0 // <- no IndexerID, replay path must be skipped

	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)

	forceGrabCalls := 0
	sonarrStub := replayingSonarr{
		forceGrab: func(_ context.Context, _ string, _ int) (string, error) {
			forceGrabCalls++
			return "", nil
		},
	}
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: sonarrStub}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)
	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(nothingBetterDecision(), nil)
	cnt.EXPECT().Increment(gomock.Any(), s.InstanceID, testSeries, testSeason, gomock.Any()).Return(
		domainregrab.NoBetterCounter{Consecutive: 1}, nil)
	mt.EXPECT().IncRegrabResult(testInstance, "nothing_better")
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	_, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 0, forceGrabCalls, "missing IndexerID must skip the replay path entirely")
}

// TestRunInstance_ReplayByGUID_WarmsCacheBeforeForceGrab — happy path.
// SearchReleases MUST run before ForceGrab, with the same (seriesID,
// seasonNumber) as the original grab. ForceGrab succeeds → OutcomeGrabbed.
func TestRunInstance_ReplayByGUID_WarmsCacheBeforeForceGrab(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, bl, cnt, ev, gx, mt := makeUC(t, ctrl)
	decisionsRepo := mocks.NewMockDecisionRepository(ctrl)
	uc.WithDecisions(decisionsRepo)

	s := enabledSettings()
	orig := successGrab()
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)

	// Record the call ORDER — warm MUST happen before ForceGrab.
	var calls []string
	var warmSeries domain.SonarrSeriesID
	var warmSeason int
	sonarrStub := replayingSonarr{
		searchReleases: func(_ context.Context, seriesID domain.SonarrSeriesID, seasonNumber int) ([]release.Release, error) {
			calls = append(calls, "search")
			warmSeries = seriesID
			warmSeason = seasonNumber
			return nil, nil
		},
		forceGrab: func(_ context.Context, _ string, _ int) (string, error) {
			calls = append(calls, "grab")
			return "", nil
		},
	}
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: sonarrStub}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)
	decisionsRepo.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil)
	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Times(0)
	newID := uuid.New()
	gx.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(appgrab.Output{
		Record:   domaingrab.Record{ID: newID, InstanceName: testInstance, SeriesID: testSeries, SeasonNumber: testSeason},
		Attempts: 1,
	})
	grabs.EXPECT().SetReplayOfID(gomock.Any(), newID, orig.ID).Return(nil)
	cnt.EXPECT().Reset(gomock.Any(), s.InstanceID, testSeries, testSeason, gomock.Any()).Return(nil)
	mt.EXPECT().IncRegrabResult(testInstance, "grabbed")
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 1, res.RegrabbedCount)
	assert.Equal(t, []string{"search", "grab"}, calls,
		"SearchReleases MUST be called before ForceGrab to warm Sonarr's release cache")
	assert.Equal(t, orig.SeriesID, warmSeries, "warm call must use the original grab's seriesID")
	assert.Equal(t, orig.SeasonNumber, warmSeason, "warm call must use the original grab's seasonNumber")
}

// TestRunInstance_ReplayByGUID_WarmFails_ContinuesToForceGrab — if the
// warm GET fails (network / 5xx / ctx-cancel), the replay must
// proceed to ForceGrab anyway. Reasoning: another path may have
// already populated the cache; the existing 404 fall-through covers
// the cold case.
func TestRunInstance_ReplayByGUID_WarmFails_ContinuesToForceGrab(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, bl, cnt, ev, gx, mt := makeUC(t, ctrl)
	decisionsRepo := mocks.NewMockDecisionRepository(ctrl)
	uc.WithDecisions(decisionsRepo)

	s := enabledSettings()
	orig := successGrab()
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)

	forceGrabCalls := 0
	sonarrStub := replayingSonarr{
		searchReleases: func(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]release.Release, error) {
			return nil, errors.New("dial sonarr: connection refused")
		},
		forceGrab: func(_ context.Context, _ string, _ int) (string, error) {
			forceGrabCalls++
			return "", nil
		},
	}
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: sonarrStub}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)
	decisionsRepo.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil)
	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Times(0)
	newID := uuid.New()
	gx.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(appgrab.Output{
		Record:   domaingrab.Record{ID: newID, InstanceName: testInstance, SeriesID: testSeries, SeasonNumber: testSeason},
		Attempts: 1,
	})
	grabs.EXPECT().SetReplayOfID(gomock.Any(), newID, orig.ID).Return(nil)
	cnt.EXPECT().Reset(gomock.Any(), s.InstanceID, testSeries, testSeason, gomock.Any()).Return(nil)
	mt.EXPECT().IncRegrabResult(testInstance, "grabbed")
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 1, res.RegrabbedCount, "warm failure must not block the replay")
	assert.Equal(t, 1, forceGrabCalls, "ForceGrab must still run when SearchReleases errors")
}

// TestRunInstance_ReplayByGUID_WarmedButForceGrabReleaseGone — warm
// succeeds but ForceGrab still returns 404. The replay must
// fall through to the evaluator path — warming does not change the
// 404 → release_gone classification.
func TestRunInstance_ReplayByGUID_WarmedButForceGrabReleaseGone(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, bl, cnt, ev, _, mt := makeUC(t, ctrl)
	s := enabledSettings()
	orig := successGrab()
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)

	warmCalls, forceGrabCalls := 0, 0
	sonarrStub := replayingSonarr{
		searchReleases: func(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]release.Release, error) {
			warmCalls++
			return nil, nil
		},
		forceGrab: func(_ context.Context, _ string, _ int) (string, error) {
			forceGrabCalls++
			return "", &sonarrStatusError404{}
		},
	}
	uc.WithReleaseGoneClassifier(func(err error) bool {
		var target *sonarrStatusError404
		return errors.As(err, &target)
	})

	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: sonarrStub}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)

	// Evaluator runs as fall-through and returns nothing-better.
	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(nothingBetterDecision(), nil)
	cnt.EXPECT().Increment(gomock.Any(), s.InstanceID, testSeries, testSeason, gomock.Any()).Return(
		domainregrab.NoBetterCounter{Consecutive: 1}, nil)
	mt.EXPECT().IncRegrabResult(testInstance, "nothing_better")
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 1, warmCalls, "warm call still happens before ForceGrab")
	assert.Equal(t, 1, forceGrabCalls, "ForceGrab runs after warm")
	assert.Equal(t, 1, res.NothingBetterCount,
		"warm-then-404 still falls through; evaluator's verdict drives the outcome")
}

// TestRunInstance_ReplayByGUID_AlreadyAdded_TreatedAsGrabbed — Sonarr
// returns 500 wrapping qBit 409 ("hash already in qBit"). Story 117:
// the replay's intent (have the file in qBit) is realised, so the use
// case must persist a decision row with ChosenBecauseWatchdogReplayAlreadyAdded
// and treat the outcome as OutcomeGrabbed.
func TestRunInstance_ReplayByGUID_AlreadyAdded_TreatedAsGrabbed(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, bl, cnt, ev, gx, mt := makeUC(t, ctrl)
	decisionsRepo := mocks.NewMockDecisionRepository(ctrl)
	uc.WithDecisions(decisionsRepo)

	s := enabledSettings()
	orig := successGrab()
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)

	sonarrStub := replayingSonarr{
		forceGrab: func(_ context.Context, _ string, _ int) (string, error) {
			return "", &sonarrStatusError409{}
		},
	}
	// Inject the already-added classifier so the use case fingerprints
	// our stub error without needing the real Sonarr StatusError shape.
	uc.WithReleaseAlreadyAddedClassifier(func(err error) bool {
		var target *sonarrStatusError409
		return errors.As(err, &target)
	})

	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: sonarrStub}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)

	var saved decision.Decision
	decisionsRepo.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, d decision.Decision) error {
		saved = d
		return nil
	})

	// Evaluator MUST NOT be called.
	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Times(0)

	newID := uuid.New()
	gx.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(appgrab.Output{
		Record: domaingrab.Record{ID: newID, InstanceName: testInstance,
			SeriesID: testSeries, SeasonNumber: testSeason},
		Attempts: 1,
	})
	grabs.EXPECT().SetReplayOfID(gomock.Any(), newID, orig.ID).Return(nil)
	cnt.EXPECT().Reset(gomock.Any(), s.InstanceID, testSeries, testSeason, gomock.Any()).Return(nil)
	mt.EXPECT().IncRegrabResult(testInstance, "grabbed")
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 1, res.RegrabbedCount, "already-added counts as a grab")
	require.NotNil(t, saved.Intent)
	assert.Equal(t,
		decision.ChosenBecauseWatchdogReplayAlreadyAdded,
		saved.Intent.ChosenBecause,
		"intent must mark the already-added path")
	assert.Equal(t, decision.OutcomeGrab, saved.Outcome,
		"already-added persists as OutcomeGrab so runGrab fires")
	require.NotNil(t, saved.Selected, "Selected must be populated for runGrab")
	assert.Equal(t, uuid.Nil, saved.ScanRunID,
		"121b §B: replay path persists NULL scan_run_id")
}

// TestRunInstance_ReplayByGUID_OtherError_WritesErrorDecision — Sonarr
// returns a generic 5xx (not release-gone, not already-added). Story 117:
// use case must persist an audit-trail decision row with Outcome=Skip
// + Intent=ReplayError + Reason=ReplayError, surface OutcomeError so
// the caller fires the cooldown, and NOT emit the legacy
// regrab_evaluate_failed WARN (the decision row IS the audit trail).
func TestRunInstance_ReplayByGUID_OtherError_WritesErrorDecision(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	buf := &bytes.Buffer{}
	debugLogger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	settings := mocks.NewMockSettingsLookup(ctrl)
	instances := mocks.NewMockInstanceRegistry(ctrl)
	qbitFac := mocks.NewMockQbitClientFactory(ctrl)
	detFac := mocks.NewMockDetectorFactory(ctrl)
	det := mocks.NewMockDetector(ctrl)
	qclient := mocks.NewMockClient(ctrl)
	grabs := mocks.NewMockGrabRepository(ctrl)
	cooldowns := mocks.NewMockCooldownRepository(ctrl)
	bl := mocks.NewMockWatchdogBlacklistRepository(ctrl)
	cnt := mocks.NewMockNoBetterCounterRepository(ctrl)
	ev := mocks.NewMockEvaluateExecutor(ctrl)
	gx := mocks.NewMockGrabExecutor(ctrl)
	mt := mocks.NewMockMetrics(ctrl)
	decisionsRepo := mocks.NewMockDecisionRepository(ctrl)

	uc := regrab.NewUseCase(settings, instances, qbitFac, detFac,
		grabs, cooldowns, bl, cnt, ev, gx, debugLogger).
		WithMetrics(mt).
		WithDecisions(decisionsRepo)

	s := enabledSettings()
	orig := successGrab()
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)

	bootErr := errors.New("sonarr 503 service unavailable")
	sonarrStub := replayingSonarr{
		forceGrab: func(_ context.Context, _ string, _ int) (string, error) {
			return "", bootErr
		},
	}
	// Both classifiers default to false → falls into the "other error"
	// branch which persists a SKIP audit decision.
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: sonarrStub}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)

	var saved decision.Decision
	decisionsRepo.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, d decision.Decision) error {
		saved = d
		return nil
	})

	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Times(0)
	mt.EXPECT().IncRegrabResult(testInstance, "error")
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 1, res.ErrorCount, "other-error path still increments ErrorCount")
	require.NotEqual(t, uuid.Nil, saved.ID, "audit decision must be persisted")
	require.NotNil(t, saved.Intent)
	assert.Equal(t,
		decision.ChosenBecauseWatchdogReplayError,
		saved.Intent.ChosenBecause,
		"intent must mark the error path")
	assert.Equal(t, decision.OutcomeSkip, saved.Outcome,
		"error decision persists as OutcomeSkip + ReasonReplayError")
	assert.Equal(t, decision.ReasonReplayError, saved.Reason)
	assert.Equal(t, bootErr.Error(), saved.ErrorDetail,
		"original error string must land in ErrorDetail")
	assert.Equal(t, uuid.Nil, saved.ScanRunID,
		"121b §B: replay path persists NULL scan_run_id")

	msgs := collectLogMsgs(t, buf.Bytes())
	_, hasLegacy := msgs["regrab_evaluate_failed"]
	assert.False(t, hasLegacy,
		"legacy regrab_evaluate_failed WARN must NOT fire when a decision row exists")
	_, hasNew := msgs["regrab_replay_error_persisted"]
	assert.True(t, hasNew,
		"new regrab_replay_error_persisted INFO must fire instead")
}

// TestRunInstance_ReplayByGUID_OtherError_DecisionRepoNil_LegacyLogPath —
// when DecisionRepository is unwired (nil), the replay error path
// falls back to the legacy regrab_evaluate_failed WARN (no decision
// row, no INFO). Back-compat guard.
func TestRunInstance_ReplayByGUID_OtherError_DecisionRepoNil_LegacyLogPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	buf := &bytes.Buffer{}
	debugLogger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	settings := mocks.NewMockSettingsLookup(ctrl)
	instances := mocks.NewMockInstanceRegistry(ctrl)
	qbitFac := mocks.NewMockQbitClientFactory(ctrl)
	detFac := mocks.NewMockDetectorFactory(ctrl)
	det := mocks.NewMockDetector(ctrl)
	qclient := mocks.NewMockClient(ctrl)
	grabs := mocks.NewMockGrabRepository(ctrl)
	cooldowns := mocks.NewMockCooldownRepository(ctrl)
	bl := mocks.NewMockWatchdogBlacklistRepository(ctrl)
	cnt := mocks.NewMockNoBetterCounterRepository(ctrl)
	ev := mocks.NewMockEvaluateExecutor(ctrl)
	gx := mocks.NewMockGrabExecutor(ctrl)
	mt := mocks.NewMockMetrics(ctrl)

	// NOTE: WithDecisions NOT called → u.decisions == nil.
	uc := regrab.NewUseCase(settings, instances, qbitFac, detFac,
		grabs, cooldowns, bl, cnt, ev, gx, debugLogger).WithMetrics(mt)

	s := enabledSettings()
	orig := successGrab()
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)

	sonarrStub := replayingSonarr{
		forceGrab: func(_ context.Context, _ string, _ int) (string, error) {
			return "", errors.New("sonarr 503 service unavailable")
		},
	}
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: sonarrStub}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), s.InstanceID, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)
	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Times(0)
	mt.EXPECT().IncRegrabResult(testInstance, "error")
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	_, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)

	msgs := collectLogMsgs(t, buf.Bytes())
	_, hasLegacy := msgs["regrab_evaluate_failed"]
	assert.True(t, hasLegacy,
		"legacy WARN MUST still fire when decisions repo is unwired (back-compat)")
}

// sonarrStatusError409 is a stand-in for the real Sonarr StatusError
// carrying a 500-wrapping-qBit-409 body. Mirrors sonarrStatusError404 —
// kept tiny so the test stays decoupled from the real Sonarr types.
type sonarrStatusError409 struct{}

func (sonarrStatusError409) Error() string { return "stub: 500 [409:Conflict] qBittorrent" }

// replayingSonarr wraps fakeSonarr with hooks on SearchReleases and
// ForceGrab so the replay tests can both inspect call args and inject
// errors. The default (nil hook) returns the fakeSonarr zero-value
// answer — empty slice / nil error / nil grab id.
type replayingSonarr struct {
	fakeSonarr
	searchReleases func(ctx context.Context, seriesID domain.SonarrSeriesID, seasonNumber int) ([]release.Release, error)
	forceGrab      func(ctx context.Context, guid string, indexerID int) (string, error)
}

func (r replayingSonarr) SearchReleases(ctx context.Context, seriesID domain.SonarrSeriesID, seasonNumber int) ([]release.Release, error) {
	if r.searchReleases == nil {
		return r.fakeSonarr.SearchReleases(ctx, seriesID, seasonNumber)
	}
	return r.searchReleases(ctx, seriesID, seasonNumber)
}

func (r replayingSonarr) ForceGrab(ctx context.Context, guid string, indexerID int) (string, error) {
	if r.forceGrab == nil {
		return "", nil
	}
	return r.forceGrab(ctx, guid, indexerID)
}

// sonarrStatusError404 is a tiny stand-in for the real Sonarr
// StatusError{Status:404}. We can't construct the real type from a
// _test.go file without an import cycle, so the test overrides the
// release-gone classifier with one that matches THIS type. The use
// case behaviour under test (fall-through vs surface) is what
// matters; the classifier itself is exercised by the unit test on
// IsReleaseGone in infrastructure/sonarr.
type sonarrStatusError404 struct{}

func (sonarrStatusError404) Error() string { return "stub: 404 release gone" }
