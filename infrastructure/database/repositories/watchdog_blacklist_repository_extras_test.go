package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

func TestWatchdogBlacklistRepository_DeleteByID_ScopedToInstance(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogBlacklistRepository(db)
			ctx := context.Background()
			now := time.Now().UTC()

			if err := repo.Upsert(ctx, regrab.BlacklistEntry{
				InstanceID: 1, SeriesID: 10, SeasonNumber: 1,
				Reason: regrab.ReasonConsecutiveNoBetter, Consecutive: 3, CreatedAt: now,
			}); err != nil {
				t.Fatalf("upsert: %v", err)
			}
			rows, err := repo.ListByInstance(ctx, 1)
			if err != nil || len(rows) != 1 {
				t.Fatalf("setup: %v len=%d", err, len(rows))
			}
			rowID := rows[0].ID

			// Wrong instance → typed WatchdogBlacklistNotFoundError, row preserved.
			err = repo.DeleteByID(ctx, 999, rowID)
			if err == nil {
				t.Errorf("wrong-instance: expected error, got nil")
			}
			var typed *sharedErrors.WatchdogBlacklistNotFoundError
			if !errors.As(err, &typed) {
				t.Errorf("wrong-instance: expected typed WatchdogBlacklistNotFoundError, got %v", err)
			} else if typed.ID != rowID {
				t.Errorf("wrong-instance: typed.ID = %d, want %d", typed.ID, rowID)
			}
			rows, _ = repo.ListByInstance(ctx, 1)
			if len(rows) != 1 {
				t.Errorf("row was removed by wrong-instance DELETE")
			}

			// Correct instance → row removed.
			if err := repo.DeleteByID(ctx, 1, rowID); err != nil {
				t.Errorf("correct delete: %v", err)
			}
			rows, _ = repo.ListByInstance(ctx, 1)
			if len(rows) != 0 {
				t.Errorf("row not removed: %d", len(rows))
			}

			// Repeat delete → typed WatchdogBlacklistNotFoundError.
			err = repo.DeleteByID(ctx, 1, rowID)
			if err == nil {
				t.Errorf("repeat: expected error, got nil")
			}
			var repeatTyped *sharedErrors.WatchdogBlacklistNotFoundError
			if !errors.As(err, &repeatTyped) {
				t.Errorf("repeat: expected typed WatchdogBlacklistNotFoundError, got %v", err)
			} else if repeatTyped.ID != rowID {
				t.Errorf("repeat: typed.ID = %d, want %d", repeatTyped.ID, rowID)
			}
		})
	}
}

func TestWatchdogBlacklistRepository_ListByInstanceWithLimit_Paginates(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogBlacklistRepository(db)
			ctx := context.Background()
			base := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)

			for i := range 5 {
				if err := repo.Upsert(ctx, regrab.BlacklistEntry{
					InstanceID: 1, SeriesID: domain.SonarrSeriesID(100 + i), SeasonNumber: 1,
					Reason: regrab.ReasonConsecutiveNoBetter, Consecutive: 3,
					CreatedAt: base.Add(time.Duration(i) * time.Hour),
				}); err != nil {
					t.Fatalf("upsert %d: %v", i, err)
				}
			}

			// Page 1: limit=2 → newest two (i=4, i=3).
			p1, err := repo.ListByInstanceWithLimit(ctx, 1, 2, time.Time{}, 0)
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
			p2, err := repo.ListByInstanceWithLimit(ctx, 1, 2, last.CreatedAt, last.ID)
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
			p3, err := repo.ListByInstanceWithLimit(ctx, 1, 2, last.CreatedAt, last.ID)
			if err != nil {
				t.Fatalf("page 3: %v", err)
			}
			if len(p3) != 1 || p3[0].SeriesID != 100 {
				t.Errorf("page 3: %+v", p3)
			}
		})
	}
}
