package regrab_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	appgrab "github.com/alexmorbo/seasonfill/internal/grab/app"
	domaingrab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/qbit"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
	domainregrab "github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

// ADR-0016 N2.4 — watchdog.regrab transactional-outbox emit test. Drives the
// full RunInstance happy path (OutcomeGrabbed) with a Transactor + OutboxEmitter
// wired, asserting the emit runs inside the replay-stamp tx.

type recordingOutbox struct {
	mu   sync.Mutex
	rows []ports.OutboxRow
}

func (o *recordingOutbox) Insert(_ context.Context, row ports.OutboxRow) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rows = append(o.rows, row)
	return nil
}

func (o *recordingOutbox) snapshot() []ports.OutboxRow {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]ports.OutboxRow(nil), o.rows...)
}

type passthroughTx struct{ calls int }

func (t *passthroughTx) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	t.calls++
	return fn(ctx)
}

func TestRunInstance_Regrab_EmitsWatchdogRegrab(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	uc, settings, instances, qbitFac, detFac, det, qclient, grabs, cooldowns, bl, cnt, ev, gx, mt := makeUC(t, ctrl)

	tx := &passthroughTx{}
	outbox := &recordingOutbox{}
	uc.WithTransactor(tx).WithOutbox(outbox)

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
	instances.EXPECT().Get(string(testInstance)).Return(scan.Instance{Client: fakeSonarr{}}, true)
	grabs.EXPECT().FindLatestSuccessByHash(gomock.Any(), testHash).Return(orig, nil)
	det.EXPECT().Detect(gomock.Any(), testHash).Return(unregisteredVerdict(), nil)
	mt.EXPECT().IncUnregistered(testInstance, "tracker.example.com")
	cooldowns.EXPECT().Get(gomock.Any(), cooldown.ScopeRegrabRetry, gomock.Any()).Return(cooldown.Cooldown{}, false, nil)
	bl.EXPECT().Find(gomock.Any(), testInstance, testSeries, testSeason).Return(domainregrab.BlacklistEntry{}, ports.ErrNotFound)

	ev.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(successDecision(), nil)
	newID := uuid.New()
	gx.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(appgrab.Output{
		Record: domaingrab.Record{ID: newID, InstanceName: testInstance,
			SeriesID: testSeries, SeasonNumber: testSeason},
		Attempts: 1,
	})
	grabs.EXPECT().SetReplayOfID(gomock.Any(), newID, orig.ID).Return(nil)
	cnt.EXPECT().Reset(gomock.Any(), testInstance, testSeries, testSeason, gomock.Any()).Return(nil)
	mt.EXPECT().IncRegrabResult(testInstance, "grabbed")
	cooldowns.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)
	mt.EXPECT().IncPollResult(testInstance, "ok")

	res, err := uc.RunInstance(context.Background(), testInstance)
	require.NoError(t, err)
	assert.Equal(t, 1, res.RegrabbedCount)

	require.GreaterOrEqual(t, tx.calls, 1, "replay-stamp + emit run inside a tx")
	rows := outbox.snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, "watchdog.regrab", rows[0].EventType)

	var p map[string]any
	require.NoError(t, json.Unmarshal(rows[0].Payload, &p))
	assert.Equal(t, "Test Series", p["series_title"])
	assert.EqualValues(t, testSeason, p["season"])
}
