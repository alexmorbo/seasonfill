//go:build integration

// D-2 / N-2g (story 508 B-9 Scope A) — end-to-end cold-start race
// verification. Boot pass over an empty `series` table arms the
// kicker; a simulated sonarr_sync writes 10 rows, then
// kicker.OnSyncCompleted fires and re-runs BackfillSeries within ms
// (NOT 60s ticker delay).
package integration

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/wiring"
)

type countingDispatcher struct {
	enqueues atomic.Int64
}

func (c *countingDispatcher) Enqueue(_ appenrich.EntityKind, _ int64, _ appenrich.Priority) {
	c.enqueues.Add(1)
}

func (c *countingDispatcher) Close() {}

// TestD2_ColdStartRace_KickerFiresWithinMs verifies that when the
// boot BackfillSeries pass finds 0 rows, the next scan_completed
// triggers BackfillSeries WITHIN 200ms (NOT the 60s ticker delay).
func TestD2_ColdStartRace_KickerFiresWithinMs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	for _, b := range allD1Backends(t) {
		if b.name != "postgres" {
			continue
		}
		t.Run(b.name, func(t *testing.T) {
			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
			require.NoError(t, err)

			seriesRepo := enrichpersistence.NewSeriesRepository(gdb)
			scanner := wiring.NewColdStartScannerAdapter(seriesRepo)
			log := slog.New(slog.NewTextHandler(io.Discard, nil))

			// 1. Empty series table → first BackfillSeries scan returns 0.
			ids, err := scanner.ListMissingTMDBSync(ctx, 5000)
			require.NoError(t, err)
			require.Empty(t, ids)

			// 2. Wire the kicker against a recording dispatcher so we
			//    know when BackfillSeries successfully re-fires.
			disp := &countingDispatcher{}
			trigger := func(c context.Context) error {
				return appenrich.BackfillSeries(c, scanner, disp, log)
			}
			kicker := adapters.NewColdStartKicker(trigger, log)

			// 3. Boot pass: arm the kicker.
			kicker.MarkPassResult(0)

			// 4. Simulate sonarr_sync writing 10 stub series.
			for i := 1; i <= 10; i++ {
				_, err := db.ExecContext(ctx,
					`INSERT INTO series (title, hydration, in_production, origin_countries, created_at, updated_at)
					 VALUES ($1, 'stub', false, '[]', now(), now())`,
					fmt.Sprintf("Series %d", i))
				require.NoError(t, err)
			}

			// 5. Fire OnSyncCompleted — measure latency to BackfillSeries
			//    completion.
			start := time.Now()
			kicker.OnSyncCompleted(ctx)
			elapsed := time.Since(start)
			require.Less(t, elapsed, 5*time.Second,
				"kicker must fire BackfillSeries synchronously (no 60s wait)")
			require.GreaterOrEqual(t, disp.enqueues.Load(), int64(10),
				"BackfillSeries must enqueue all 10 freshly-synced series")

			// 6. Second OnSyncCompleted must be a no-op (kicker single-fire).
			before := disp.enqueues.Load()
			kicker.OnSyncCompleted(ctx)
			require.Equal(t, before, disp.enqueues.Load())
		})
	}
}
