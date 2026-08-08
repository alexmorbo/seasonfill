package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	grabdomain "github.com/alexmorbo/seasonfill/internal/grab/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	appta "github.com/alexmorbo/seasonfill/internal/torrentaction/app"
)

const testHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// --- fakes ---

type fakeGrabs struct {
	rec grabdomain.Record
	err error
}

func (f fakeGrabs) FindLatestSuccessByHash(_ context.Context, _ string) (grabdomain.Record, error) {
	return f.rec, f.err
}

type fakeSeriesMap struct {
	ref appta.SeriesMapRef
	err error
}

func (f fakeSeriesMap) FindByHash(_ context.Context, _ string) (appta.SeriesMapRef, error) {
	return f.ref, f.err
}

// seriesMapMiss is the "hash absent from the bridge" fake — the same 404
// shape the real repo returns.
func seriesMapMiss() fakeSeriesMap {
	return fakeSeriesMap{err: errors.Join(
		&sharedErrors.GrabNotFoundError{ID: "hash:" + testHash}, sharedports.ErrNotFound)}
}

func seriesMapRefFor(inst shareddomain.InstanceName) fakeSeriesMap {
	return fakeSeriesMap{ref: appta.SeriesMapRef{InstanceName: inst, SeriesID: 1}}
}

type fakeController struct {
	loginErr   error
	pauseErr   error
	resumeErr  error
	recheckErr error
	paused     int
	resumed    int
	rechecked  int
	closed     bool
}

func (c *fakeController) Login(context.Context) error          { return c.loginErr }
func (c *fakeController) Pause(context.Context, string) error  { c.paused++; return c.pauseErr }
func (c *fakeController) Resume(context.Context, string) error { c.resumed++; return c.resumeErr }
func (c *fakeController) Recheck(context.Context, string) error {
	c.rechecked++
	return c.recheckErr
}
func (c *fakeController) Close() error { c.closed = true; return nil }

type fakeProvider struct {
	ctrl *fakeController
	err  error
	got  shareddomain.InstanceName
}

func (p *fakeProvider) ClientFor(_ context.Context, inst shareddomain.InstanceName) (appta.TorrentController, error) {
	p.got = inst
	if p.err != nil {
		return nil, p.err
	}
	return p.ctrl, nil
}

type fakeAudit struct {
	rows []appta.AuditRecord
	err  error
}

func (a *fakeAudit) Write(_ context.Context, rec appta.AuditRecord) error {
	a.rows = append(a.rows, rec)
	return a.err
}

func recordFor(inst shareddomain.InstanceName) grabdomain.Record {
	return grabdomain.Record{ID: uuid.New(), InstanceName: inst}
}

// --- tests ---

// Guard: a hash outside our grabs bubbles ports.ErrNotFound and never
// dials qBit or writes audit.
func TestDo_ForeignHash_404_NoDialNoAudit(t *testing.T) {
	grabs := fakeGrabs{err: errors.Join(
		&sharedErrors.GrabNotFoundError{ID: "hash:" + testHash}, sharedports.ErrNotFound)}
	prov := &fakeProvider{ctrl: &fakeController{}}
	audit := &fakeAudit{}
	uc := appta.New(grabs, seriesMapMiss(), prov, audit, nil)

	err := uc.Do(context.Background(), appta.Input{
		Instance: "main", Hash: testHash, Action: appta.ActionPause, Actor: "u",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, sharedports.ErrNotFound))
	assert.Empty(t, prov.got, "must not dial any instance")
	assert.Empty(t, audit.rows, "must not audit a guarded-out hash")
}

// Actual-instance resolution: path :name != grab instance -> 404, no dial.
func TestDo_InstanceMismatch_404(t *testing.T) {
	grabs := fakeGrabs{rec: recordFor("actual")}
	prov := &fakeProvider{ctrl: &fakeController{}}
	audit := &fakeAudit{}
	uc := appta.New(grabs, seriesMapMiss(), prov, audit, nil)

	err := uc.Do(context.Background(), appta.Input{
		Instance: "wrong", Hash: testHash, Action: appta.ActionPause, Actor: "u",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, sharedports.ErrNotFound))
	assert.Empty(t, prov.got, "must not dial when path != actual")
	assert.Empty(t, audit.rows)
}

// Happy path dials the ACTUAL instance from the grab record.
func TestDo_DialsActualInstance(t *testing.T) {
	grabs := fakeGrabs{rec: recordFor("actual")}
	ctrl := &fakeController{}
	prov := &fakeProvider{ctrl: ctrl}
	audit := &fakeAudit{}
	uc := appta.New(grabs, seriesMapMiss(), prov, audit, nil)

	err := uc.Do(context.Background(), appta.Input{
		Instance: "actual", Hash: testHash, Action: appta.ActionResume, Actor: "op",
	})
	require.NoError(t, err)
	assert.Equal(t, shareddomain.InstanceName("actual"), prov.got)
	assert.Equal(t, 1, ctrl.resumed)
	assert.True(t, ctrl.closed, "client must be closed")
}

// Idempotent 200: an already-paused pause returns nil (client no-ops).
func TestDo_IdempotentSuccess(t *testing.T) {
	grabs := fakeGrabs{rec: recordFor("main")}
	ctrl := &fakeController{} // pauseErr == nil (qBit no-op on already-paused)
	prov := &fakeProvider{ctrl: ctrl}
	audit := &fakeAudit{}
	uc := appta.New(grabs, seriesMapMiss(), prov, audit, nil)

	err := uc.Do(context.Background(), appta.Input{
		Instance: "main", Hash: testHash, Action: appta.ActionPause, Actor: "op",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, ctrl.paused)
}

// Audit written with result=ok on success.
func TestDo_AuditWrittenOnSuccess(t *testing.T) {
	grabs := fakeGrabs{rec: recordFor("main")}
	prov := &fakeProvider{ctrl: &fakeController{}}
	audit := &fakeAudit{}
	uc := appta.New(grabs, seriesMapMiss(), prov, audit, nil)

	err := uc.Do(context.Background(), appta.Input{
		Instance: "main", Hash: testHash, Action: appta.ActionRecheck, Actor: "alice",
	})
	require.NoError(t, err)
	require.Len(t, audit.rows, 1)
	row := audit.rows[0]
	assert.Equal(t, shareddomain.InstanceName("main"), row.InstanceName)
	assert.Equal(t, testHash, row.Hash)
	assert.Equal(t, appta.ActionRecheck, row.Action)
	assert.Equal(t, "alice", row.Actor)
	assert.Equal(t, "ok", row.Result)
	assert.WithinDuration(t, time.Now(), row.CreatedAt, time.Minute)
}

// qBit network error bubbles ErrInstanceNetwork AND writes result=error.
func TestDo_QbitUnreachable_502ErrorAudited(t *testing.T) {
	grabs := fakeGrabs{rec: recordFor("main")}
	ctrl := &fakeController{pauseErr: errors.Join(
		errors.New("dial tcp: timeout"), sharedErrors.ErrInstanceNetwork)}
	prov := &fakeProvider{ctrl: ctrl}
	audit := &fakeAudit{}
	uc := appta.New(grabs, seriesMapMiss(), prov, audit, nil)

	err := uc.Do(context.Background(), appta.Input{
		Instance: "main", Hash: testHash, Action: appta.ActionPause, Actor: "op",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, sharedErrors.ErrInstanceNetwork))
	require.Len(t, audit.rows, 1)
	assert.Equal(t, "error", audit.rows[0].Result)
}

// A failed audit insert never fails a successful action.
func TestDo_AuditWriteFailure_DoesNotFailAction(t *testing.T) {
	grabs := fakeGrabs{rec: recordFor("main")}
	prov := &fakeProvider{ctrl: &fakeController{}}
	audit := &fakeAudit{err: errors.New("db down")}
	uc := appta.New(grabs, seriesMapMiss(), prov, audit, nil)

	err := uc.Do(context.Background(), appta.Input{
		Instance: "main", Hash: testHash, Action: appta.ActionResume, Actor: "op",
	})
	require.NoError(t, err, "audit failure must be swallowed")
}

// Q5 union (a): a hash present ONLY in torrent_series_map (grab miss) now
// proceeds and dials the map's instance — the exact case that 404'd before
// the union guard.
func TestDo_SeriesMapOnly_Proceeds(t *testing.T) {
	grabs := fakeGrabs{err: errors.Join(
		&sharedErrors.GrabNotFoundError{ID: "hash:" + testHash}, sharedports.ErrNotFound)}
	ctrl := &fakeController{}
	prov := &fakeProvider{ctrl: ctrl}
	audit := &fakeAudit{}
	uc := appta.New(grabs, seriesMapRefFor("obs"), prov, audit, nil)

	err := uc.Do(context.Background(), appta.Input{
		Instance: "obs", Hash: testHash, Action: appta.ActionPause, Actor: "op",
	})
	require.NoError(t, err)
	assert.Equal(t, shareddomain.InstanceName("obs"), prov.got, "must dial the map's instance")
	assert.Equal(t, 1, ctrl.paused)
	require.Len(t, audit.rows, 1)
	assert.Equal(t, "ok", audit.rows[0].Result)
}

// Q5 union (d): instance-mismatch resolved via the map path -> 404, no dial.
func TestDo_SeriesMapInstanceMismatch_404(t *testing.T) {
	grabs := fakeGrabs{err: errors.Join(
		&sharedErrors.GrabNotFoundError{ID: "hash:" + testHash}, sharedports.ErrNotFound)}
	prov := &fakeProvider{ctrl: &fakeController{}}
	audit := &fakeAudit{}
	uc := appta.New(grabs, seriesMapRefFor("obs"), prov, audit, nil)

	err := uc.Do(context.Background(), appta.Input{
		Instance: "wrong", Hash: testHash, Action: appta.ActionPause, Actor: "u",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, sharedports.ErrNotFound))
	assert.Empty(t, prov.got, "must not dial when path != map-resolved instance")
	assert.Empty(t, audit.rows)
}
