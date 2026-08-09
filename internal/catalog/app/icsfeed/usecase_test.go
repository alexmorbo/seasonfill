package icsfeed

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/calendar"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

type fakeCal struct {
	rep       calendar.Report
	err       error
	lastQuery calendar.Query
	calls     int
}

func (f *fakeCal) Build(_ context.Context, q calendar.Query) (calendar.Report, error) {
	f.calls++
	f.lastQuery = q
	return f.rep, f.err
}

type fakeEpoch struct {
	epoch  int64
	bumped int
	getErr error
}

func (f *fakeEpoch) GetICSEpoch(_ context.Context) (int64, error) {
	if f.getErr != nil {
		return 0, f.getErr
	}
	return f.epoch, nil
}

func (f *fakeEpoch) BumpICSEpoch(_ context.Context) (int64, error) {
	f.bumped++
	f.epoch++
	return f.epoch, nil
}

func newUC(t *testing.T, cal CalendarBuilder, ep EpochRepository) (*UseCase, []byte) {
	t.Helper()
	key, err := crypto.DeriveICSTokenKey("master-key")
	require.NoError(t, err)
	return NewUseCase(cal, ep, key), key
}

func TestMint_SignsAtCurrentEpoch(t *testing.T) {
	t.Parallel()
	ep := &fakeEpoch{epoch: 5}
	uc, key := newUC(t, &fakeCal{}, ep)

	m, err := uc.Mint(context.Background(), "library")
	require.NoError(t, err)
	assert.Equal(t, "library", m.Scope)

	p, err := VerifyToken(key, m.Token)
	require.NoError(t, err)
	assert.Equal(t, int64(5), p.Epoch)
	assert.Equal(t, "library", p.Scope)
}

func TestMint_NormalizesScope(t *testing.T) {
	t.Parallel()
	ep := &fakeEpoch{epoch: 0}
	uc, _ := newUC(t, &fakeCal{}, ep)
	m, err := uc.Mint(context.Background(), "garbage")
	require.NoError(t, err)
	assert.Equal(t, "all", m.Scope)
}

func TestRender_WindowAndScopeFromToken(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	cal := &fakeCal{rep: calendar.Report{GeneratedAt: now}}
	ep := &fakeEpoch{epoch: 3}
	uc, key := newUC(t, cal, ep)
	uc.WithClock(func() time.Time { return now })

	tok, err := SignToken(key, "followed", 3)
	require.NoError(t, err)

	body, err := uc.Render(context.Background(), tok)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(body, "BEGIN:VCALENDAR"))
	assert.Equal(t, "followed", cal.lastQuery.Scope, "scope comes from the token")
	assert.Equal(t, now.AddDate(0, -1, 0), cal.lastQuery.From)
	assert.Equal(t, now.AddDate(0, 3, 0), cal.lastQuery.To)
}

func TestRender_LibraryScopeFromToken(t *testing.T) {
	t.Parallel()
	cal := &fakeCal{}
	ep := &fakeEpoch{epoch: 0}
	uc, key := newUC(t, cal, ep)
	tok, _ := SignToken(key, "library", 0)
	_, err := uc.Render(context.Background(), tok)
	require.NoError(t, err)
	assert.Equal(t, "library", cal.lastQuery.Scope)
}

func TestRender_RevokedEpoch_Rejected(t *testing.T) {
	t.Parallel()
	cal := &fakeCal{}
	ep := &fakeEpoch{epoch: 2}
	uc, key := newUC(t, cal, ep)
	tok, _ := SignToken(key, "all", 2)
	// simulate a revoke — live epoch advances past the token's epoch
	ep.epoch = 3
	_, err := uc.Render(context.Background(), tok)
	assert.ErrorIs(t, err, ErrRevoked)
	assert.Equal(t, 0, cal.calls, "no calendar query on a revoked token")
}

func TestRender_TamperedToken_Rejected(t *testing.T) {
	t.Parallel()
	uc, _ := newUC(t, &fakeCal{}, &fakeEpoch{epoch: 0})
	_, err := uc.Render(context.Background(), "not.a.valid.token")
	assert.ErrorIs(t, err, ErrRevoked)
}

func TestRender_BuildError_WrappedNotRevoked(t *testing.T) {
	t.Parallel()
	cal := &fakeCal{err: errors.New("db down")}
	ep := &fakeEpoch{epoch: 0}
	uc, key := newUC(t, cal, ep)
	tok, _ := SignToken(key, "all", 0)
	_, err := uc.Render(context.Background(), tok)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRevoked)
	assert.Contains(t, err.Error(), "db down")
}

func TestRevoke_BumpsEpoch(t *testing.T) {
	t.Parallel()
	ep := &fakeEpoch{epoch: 4}
	uc, _ := newUC(t, &fakeCal{}, ep)
	got, err := uc.Revoke(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(5), got)
	assert.Equal(t, 1, ep.bumped)
}
