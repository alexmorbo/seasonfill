package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

func TestWatchdogBlacklistRepository_CountByInstance(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogBlacklistRepository(db)
			ctx := context.Background()
			now := time.Now().UTC()
			rows := []regrab.BlacklistEntry{
				{InstanceID: 1, SeriesID: 10, SeasonNumber: 1, Reason: regrab.ReasonConsecutiveNoBetter, Consecutive: 3, CreatedAt: now},
				{InstanceID: 1, SeriesID: 11, SeasonNumber: 2, Reason: regrab.ReasonConsecutiveNoBetter, Consecutive: 3, CreatedAt: now},
				{InstanceID: 2, SeriesID: 99, SeasonNumber: 1, Reason: regrab.ReasonConsecutiveNoBetter, Consecutive: 3, CreatedAt: now},
			}
			for _, e := range rows {
				if err := repo.Upsert(ctx, e); err != nil {
					t.Fatalf("upsert: %v", err)
				}
			}
			for _, c := range []struct {
				id   uint
				want int
			}{{1, 2}, {2, 1}, {99, 0}} {
				got, err := repo.CountByInstance(ctx, c.id)
				if err != nil {
					t.Fatalf("count id=%d: %v", c.id, err)
				}
				if got != c.want {
					t.Errorf("id=%d: got %d want %d", c.id, got, c.want)
				}
			}
		})
	}
}
