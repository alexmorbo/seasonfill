package regrab_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/alexmorbo/seasonfill/application/evaluate"
	appgrab "github.com/alexmorbo/seasonfill/application/grab"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/application/regrab/mocks"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	"github.com/alexmorbo/seasonfill/domain/decision"
	domaingrab "github.com/alexmorbo/seasonfill/domain/grab"
	domainregrab "github.com/alexmorbo/seasonfill/domain/regrab"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/qbit"
)

const (
	testInstance = "alpha"
	testHash     = "abcdef0123456789abcdef0123456789abcdef01"
	testSeries   = 122
	testSeason   = 2
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
	h := testHash
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
func (fakeSonarr) GetSeries(ctx context.Context, id int) (series.Series, error) {
	return series.Series{ID: id, Title: "Test Series", QualityProfile: 1}, nil
}
func (fakeSonarr) Name() string {
	return "fake"
}
func (fakeSonarr) ListEpisodes(ctx context.Context, id, season int) ([]series.Episode, error) {
	return []series.Episode{{Number: 1, AirDateUTC: time.Now().Add(-time.Hour)}}, nil
}
func (fakeSonarr) ListEpisodeFiles(ctx context.Context, id int) (map[int]int, error) {
	return map[int]int{}, nil
}
func (fakeSonarr) GetQualityProfile(ctx context.Context, id int) (ports.QualityProfile, error) {
	return ports.QualityProfile{ID: id}, nil
}
func (fakeSonarr) SearchReleases(ctx context.Context, id, s int) ([]release.Release, error) {
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
func (fakeSonarr) GrabHistory(ctx context.Context, id int) ([]ports.HistoryEvent, error) {
	return nil, nil
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
	instances.EXPECT().Get(testInstance).Return(scan.Instance{Client: fakeSonarr{}}, true)
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
	instances.EXPECT().Get(testInstance).Return(scan.Instance{Client: fakeSonarr{}}, true)
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
	instances.EXPECT().Get(testInstance).Return(scan.Instance{Client: fakeSonarr{}}, true)
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
	instances.EXPECT().Get(testInstance).Return(scan.Instance{Client: fakeSonarr{}}, true)
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
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)
	instances.EXPECT().Get(testInstance).Return(scan.Instance{
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
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)
	instances.EXPECT().Get(testInstance).Return(scan.Instance{Client: fakeSonarr{}}, true)
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
	instances.EXPECT().Get(testInstance).Return(scan.Instance{Client: fakeSonarr{}}, true)
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
	settings.EXPECT().Lookup(gomock.Any(), testInstance).Return(s, nil)
	qbitFac.EXPECT().NewClient(s).Return(qclient, nil)
	qclient.EXPECT().Login(gomock.Any()).Return(nil)
	qclient.EXPECT().ListTorrents(gomock.Any()).Return([]qbit.Torrent{
		{Hash: testHash, Category: "sonarr"},
	}, nil)
	qclient.EXPECT().Close().Return(nil)
	detFac.EXPECT().NewDetector(qclient, s.CustomUnregisteredMsgs).Return(det)
	instances.EXPECT().Get(testInstance).Return(scan.Instance{Client: fakeSonarr{}}, true)
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
	instances.EXPECT().Get(testInstance).Return(scan.Instance{}, false)

	_, err := uc.RunInstance(context.Background(), testInstance)
	require.Error(t, err)
	assert.ErrorIs(t, err, regrab.ErrUnknownInstance)
}

// Reuse evaluate.Input type in test imports — keeps the import alive
// in case mockgen output drops the indirect dep.
var _ = evaluate.Input{}
