package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

func TestWatchdogBlacklistRepository_ListByInstanceWithLimit_Paginates(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogBlacklistRepository(db)
			ctx := context.Background()
			seedBlacklistInstance(t, ctx, repo, "homelab")
			base := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)

			for i := range 5 {
				if err := repo.Upsert(ctx, regrab.BlacklistEntry{
					InstanceName: "homelab", SeriesID: domain.SonarrSeriesID(100 + i), SeasonNumber: 1,
					Reason: regrab.ReasonConsecutiveNoBetter, Consecutive: 3,
					CreatedAt: base.Add(time.Duration(i) * time.Hour),
				}); err != nil {
					t.Fatalf("upsert %d: %v", i, err)
				}
			}

			// Page 1: limit=2 → newest two (i=4, i=3).
			p1, err := repo.ListByInstanceWithLimit(ctx, "homelab", 2, time.Time{}, 0, 0)
			if err != nil {
				t.Fatalf("page 1: %v", err)
			}
			if len(p1) != 2 {
				t.Fatalf("page 1 len: %d", len(p1))
			}
			if p1[0].SeriesID != 104 || p1[1].SeriesID != 103 {
				t.Errorf("page 1 order: %+v", p1)
			}

			// Page 2: continue after p1[1].
			last := p1[1]
			p2, err := repo.ListByInstanceWithLimit(ctx, "homelab", 2, last.CreatedAt, last.SeriesID, last.SeasonNumber)
			if err != nil {
				t.Fatalf("page 2: %v", err)
			}
			if len(p2) != 2 {
				t.Fatalf("page 2 len: %d", len(p2))
			}
			if p2[0].SeriesID != 102 || p2[1].SeriesID != 101 {
				t.Errorf("page 2 order: %+v", p2)
			}

			// Page 3: continue after p2[1]; only 1 row remains.
			last = p2[1]
			p3, err := repo.ListByInstanceWithLimit(ctx, "homelab", 2, last.CreatedAt, last.SeriesID, last.SeasonNumber)
			if err != nil {
				t.Fatalf("page 3: %v", err)
			}
			if len(p3) != 1 || p3[0].SeriesID != 100 {
				t.Errorf("page 3: %+v", p3)
			}
		})
	}
}
