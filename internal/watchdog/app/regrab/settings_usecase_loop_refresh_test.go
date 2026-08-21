package regrab

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loopRefreshSpy mirrors what production actually does inside the hook:
// it re-reads the settings repo the same way wiring's qbitLoader closure
// does (QbitSettingsRepository.List), so the assertions are on the
// settings the LOOPS would be handed — not merely on "a callback fired".
type loopRefreshSpy struct {
	repo  *fakeSettingsRepo
	calls atomic.Int32
	// seen maps instance name → Enabled, as observed at hook time.
	seen  map[string]bool
	panic bool
}

func newLoopRefreshSpy(repo *fakeSettingsRepo) *loopRefreshSpy {
	return &loopRefreshSpy{repo: repo, seen: map[string]bool{}}
}

func (s *loopRefreshSpy) hook(ctx context.Context) {
	s.calls.Add(1)
	if s.panic {
		panic("loop refresh exploded")
	}
	recs, err := s.repo.List(ctx)
	if err != nil {
		return
	}
	seen := make(map[string]bool, len(recs))
	for _, r := range recs {
		seen[string(r.InstanceName)] = r.Enabled
	}
	s.seen = seen
}

// The headline test: enabling the watchdog must hand the loops a settings
// map that already contains the enabled instance. This is the exact bug —
// pre-F4 nothing fired, so the loops never learned about the new row.
func TestUpsert_EnabledTrue_FiresLoopRefreshWithPersistedSettings(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	spy := newLoopRefreshSpy(repo)
	uc := newUC(t, repo, instances).
		WithWebhookChecker(&stubWebhookChecker{installed: true}).
		WithLoopRefresher(spy.hook)

	in := validInput()
	in.Enabled = true

	view, err := uc.Upsert(context.Background(), "alpha", in)
	require.NoError(t, err)
	require.True(t, view.Enabled)

	require.Equal(t, int32(1), spy.calls.Load(), "hook must fire exactly once")
	enabled, ok := spy.seen["alpha"]
	require.True(t, ok, "hook must observe the freshly-persisted row")
	assert.True(t, enabled, "hook must see enabled=true, i.e. it fired AFTER the commit")
}

// A plain settings edit (interval / url) must also refresh: SwapSettings
// re-tunes a running loop, and the pre-F4 gap swallowed those edits too.
func TestUpsert_DisabledRow_StillFiresLoopRefresh(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	spy := newLoopRefreshSpy(repo)
	uc := newUC(t, repo, instances).WithLoopRefresher(spy.hook)

	_, err := uc.Upsert(context.Background(), "alpha", validInput()) // Enabled=false
	require.NoError(t, err)

	require.Equal(t, int32(1), spy.calls.Load())
	enabled, ok := spy.seen["alpha"]
	require.True(t, ok)
	assert.False(t, enabled)
}

// Delete must refresh too — the map no longer carries the instance, which
// is how SwapSettings learns to cancel its goroutines.
func TestDelete_FiresLoopRefreshAfterRowIsGone(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	spy := newLoopRefreshSpy(repo)
	uc := newUC(t, repo, instances).WithLoopRefresher(spy.hook)

	_, err := uc.Upsert(context.Background(), "alpha", validInput())
	require.NoError(t, err)
	require.Equal(t, int32(1), spy.calls.Load())

	require.NoError(t, uc.Delete(context.Background(), "alpha"))

	require.Equal(t, int32(2), spy.calls.Load())
	_, ok := spy.seen["alpha"]
	assert.False(t, ok, "hook must observe the row already deleted")
}

// A rejected write must NOT touch the loops.
func TestUpsert_ValidationFailure_DoesNotFireLoopRefresh(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	spy := newLoopRefreshSpy(repo)
	uc := newUC(t, repo, instances).WithLoopRefresher(spy.hook)

	in := validInput()
	in.URL = "" // url is required → ValidationError before any repo work

	_, err := uc.Upsert(context.Background(), "alpha", in)
	require.Error(t, err)
	assert.Equal(t, int32(0), spy.calls.Load())
}

func TestUpsert_WebhookGateBlocks_DoesNotFireLoopRefresh(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	spy := newLoopRefreshSpy(repo)
	uc := newUC(t, repo, instances).
		WithWebhookChecker(&stubWebhookChecker{installed: false}).
		WithLoopRefresher(spy.hook)

	in := validInput()
	in.Enabled = true

	_, err := uc.Upsert(context.Background(), "alpha", in)
	require.ErrorIs(t, err, ErrWebhookNotInstalled)
	assert.Equal(t, int32(0), spy.calls.Load())
}

func TestDelete_NotFound_DoesNotFireLoopRefresh(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	spy := newLoopRefreshSpy(repo)
	uc := newUC(t, repo, instances).WithLoopRefresher(spy.hook)

	err := uc.Delete(context.Background(), "alpha") // no row was ever created
	require.Error(t, err)
	assert.Equal(t, int32(0), spy.calls.Load())
}

// The write is already committed when the hook fires, so a panicking hook
// must NOT surface as an error (and must not roll anything back).
func TestUpsert_PanickingLoopRefresh_DoesNotFailTheWrite(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	spy := newLoopRefreshSpy(repo)
	spy.panic = true
	uc := newUC(t, repo, instances).WithLoopRefresher(spy.hook)

	view, err := uc.Upsert(context.Background(), "alpha", validInput())
	require.NoError(t, err, "a broken loop refresh must not fail the settings write")
	assert.Equal(t, "alpha", string(view.InstanceName))
	assert.Equal(t, int32(1), spy.calls.Load())
	_, stored := repo.rows["alpha"]
	assert.True(t, stored, "the row must still be persisted")
}

// The default (no WithLoopRefresher) must behave exactly as pre-F4.
func TestUpsert_NilLoopRefresher_IsNoOp(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	uc := newUC(t, repo, instances) // no WithLoopRefresher

	_, err := uc.Upsert(context.Background(), "alpha", validInput())
	require.NoError(t, err)
	require.NoError(t, uc.Delete(context.Background(), "alpha"))
}
