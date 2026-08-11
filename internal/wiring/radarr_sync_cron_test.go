package wiring

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/scheduler"
)

func testLog() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// TestRegisterRadarrSync_RegistersJob proves Ф6-R-6b Gap 1: the radarr-sync
// usecase is registered on the boot scheduler under the "radarr-sync" name.
// Registration success + a duplicate-registration error on the same name prove
// the job now exists on the schedule (R-4b left it DORMANT — never registered).
func TestRegisterRadarrSync_RegistersJob(t *testing.T) {
	t.Parallel()
	sched := scheduler.New("0 */6 * * *", 0, testLog())
	syncUC := scan.NewRadarrSyncUseCase(nil, scan.RadarrSyncDeps{})
	bundle := &RadarrSyncBundle{SyncUC: syncUC}

	require.NoError(t, registerRadarrSync(sched, bundle, "0 */6 * * *", testLog()))

	// The registry is build-once: a second Register under the same name errors.
	// That error is only possible if the first registration landed.
	err := sched.Register("radarr-sync", "0 */6 * * *", func(context.Context) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate registration")
}

// TestRegisterRadarrSync_JobInvokesRunAll proves the registered closure actually
// calls RadarrSyncUseCase.RunAll (which iterates instances) — i.e. the schedule
// drives a real sync, not a dead stub. Uses a "@every 200ms" spec and waits for
// the instance's ListMovies to be hit.
func TestRegisterRadarrSync_JobInvokesRunAll(t *testing.T) {
	t.Parallel()
	var listCalls atomic.Int32
	client := &ports.RadarrClientMock{
		ListMoviesFunc: func(context.Context) ([]ports.RadarrMovie, error) {
			listCalls.Add(1)
			return nil, nil
		},
		NameFunc: func() string { return "movies" },
	}
	syncUC := scan.NewRadarrSyncUseCase(
		[]scan.RadarrInstance{{
			Config: runtime.InstanceSnapshot{
				Name: "movies", Type: scan.InstanceTypeRadarr,
				URL: "http://radarr:7878", APIKey: "k", Timeout: 5 * time.Second,
			},
			Client: client,
		}},
		scan.RadarrSyncDeps{Logger: testLog()},
	)
	bundle := &RadarrSyncBundle{SyncUC: syncUC}

	sched := scheduler.New("@every 100ms", 0, testLog())
	require.NoError(t, registerRadarrSync(sched, bundle, "@every 100ms", testLog()))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	require.NoError(t, sched.StartRegistered(ctx))

	require.Eventually(t, func() bool { return listCalls.Load() > 0 }, 3*time.Second, 25*time.Millisecond,
		"radarr-sync cron must invoke RunAll → ListMovies on schedule")
}

// TestRegisterRadarrSync_NilGates proves the nil-gates: no scheduler (cron
// disabled) or no usecase → no-op, no panic, no error.
func TestRegisterRadarrSync_NilGates(t *testing.T) {
	t.Parallel()
	assert.NoError(t, registerRadarrSync(nil, &RadarrSyncBundle{}, "0 */6 * * *", testLog()))
	sched := scheduler.New("0 */6 * * *", 0, testLog())
	assert.NoError(t, registerRadarrSync(sched, nil, "0 */6 * * *", testLog()))
	assert.NoError(t, registerRadarrSync(sched, &RadarrSyncBundle{SyncUC: nil}, "0 */6 * * *", testLog()))
}
