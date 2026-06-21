package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
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
			seedBlacklistInstance(t, ctx, repo, "homelab")
			seedBlacklistInstance(t, ctx, repo, "4k")
			now := time.Now().UTC()
			rows := []regrab.BlacklistEntry{
				{InstanceName: "homelab", SeriesID: 10, SeasonNumber: 1, Reason: regrab.ReasonConsecutiveNoBetter, Consecutive: 3, CreatedAt: now},
				{InstanceName: "homelab", SeriesID: 11, SeasonNumber: 2, Reason: regrab.ReasonConsecutiveNoBetter, Consecutive: 3, CreatedAt: now},
				{InstanceName: "4k", SeriesID: 99, SeasonNumber: 1, Reason: regrab.ReasonConsecutiveNoBetter, Consecutive: 3, CreatedAt: now},
			}
			for _, e := range rows {
				if err := repo.Upsert(ctx, e); err != nil {
					t.Fatalf("upsert: %v", err)
				}
			}
			for _, c := range []struct {
				instance domain.InstanceName
				want     int
			}{{"homelab", 2}, {"4k", 1}, {"ghost", 0}} {
				got, err := repo.CountByInstance(ctx, c.instance)
				if err != nil {
					t.Fatalf("count instance=%q: %v", c.instance, err)
				}
				if got != c.want {
					t.Errorf("instance=%q: got %d want %d", c.instance, got, c.want)
				}
			}
		})
	}
}
