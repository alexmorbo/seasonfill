package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestGrabRepository_CountReplays(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewGrabRepository(db)
			ctx := context.Background()
			now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)

			parent := grab.Record{
				ID:           uuid.New(),
				InstanceName: "alpha",
				SeriesID:     1,
				SeasonNumber: 1,
				ReleaseGUID:  "g1",
				ReleaseTitle: "t1",
				Status:       grab.StatusImported,
				ScanRunID:    uuid.New(),
				CreatedAt:    now.Add(-30 * 24 * time.Hour),
				UpdatedAt:    now.Add(-30 * 24 * time.Hour),
			}
			if err := repo.Create(ctx, parent); err != nil {
				t.Fatalf("create parent: %v", err)
			}

			cases := []struct {
				name     string
				instance domain.InstanceName
				hoursAgo int
			}{
				{"r_1h_alpha", "alpha", 1},
				{"r_18h_alpha", "alpha", 18},
				{"r_3d_alpha", "alpha", 72},
				{"r_10d_alpha", "alpha", 240},
				{"r_2h_beta", "beta", 2},
			}
			for _, c := range cases {
				rec := grab.Record{
					ID:           uuid.New(),
					InstanceName: c.instance,
					SeriesID:     2,
					SeasonNumber: 1,
					ReleaseGUID:  "g_" + c.name,
					ReleaseTitle: c.name,
					Status:       grab.StatusImported,
					ScanRunID:    uuid.New(),
					CreatedAt:    now.Add(-time.Duration(c.hoursAgo) * time.Hour),
					UpdatedAt:    now.Add(-time.Duration(c.hoursAgo) * time.Hour),
				}
				if err := repo.CreateReplay(ctx, rec, parent.ID); err != nil {
					t.Fatalf("create %s: %v", c.name, err)
				}
			}

			checks := []struct {
				label    string
				fn       func() (int, error)
				expected int
			}{
				{"24h alpha", func() (int, error) { return repo.CountReplaysSince(ctx, "alpha", now.Add(-24*time.Hour)) }, 2},
				{"7d alpha", func() (int, error) { return repo.CountReplaysSince(ctx, "alpha", now.Add(-7*24*time.Hour)) }, 3},
				{"all alpha", func() (int, error) { return repo.CountReplaysAll(ctx, "alpha") }, 4},
				{"all beta", func() (int, error) { return repo.CountReplaysAll(ctx, "beta") }, 1},
				{"all ghost", func() (int, error) { return repo.CountReplaysAll(ctx, "ghost") }, 0},
			}
			for _, c := range checks {
				got, err := c.fn()
				if err != nil {
					t.Fatalf("%s: %v", c.label, err)
				}
				if got != c.expected {
					t.Errorf("%s: got %d want %d", c.label, got, c.expected)
				}
			}
		})
	}
}
