package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func mkDownloadLink(hash string, instance domain.InstanceName, source grab.LinkSource) grab.DownloadLink {
	now := time.Now().UTC().Truncate(time.Second)
	seriesID := int64(123)
	return grab.DownloadLink{
		QbitHash:           domain.QbitHash(hash),
		InstanceName:       instance,
		InstanceType:       "sonarr",
		ExternalSeriesID:   &seriesID,
		ExternalEpisodeIDs: "[1,2,3]",
		DiscoveredAt:       now,
		Source:             source,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func TestDownloadLink_InsertOnly_Inserts(t *testing.T) {
	t.Parallel()
	for _, backend := range grabBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedSonarrInstance(t, db, "main")
			repo := NewDownloadLinkRepository(db)
			link := mkDownloadLink("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "main", grab.LinkSourceWebhook)
			require.NoError(t, repo.InsertOnly(context.Background(), link))

			var m database.DownloadLinkModel
			require.NoError(t, db.First(&m, "qbit_hash = ?", string(link.QbitHash)).Error)
			assert.Equal(t, "sonarr", m.InstanceType)
			assert.Equal(t, "webhook", m.Source)
			require.NotNil(t, m.ExternalSeriesID)
			assert.Equal(t, int64(123), *m.ExternalSeriesID)
		})
	}
}

func TestDownloadLink_InsertOnly_ConflictReturnsNil(t *testing.T) {
	t.Parallel()
	for _, backend := range grabBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedSonarrInstance(t, db, "main")
			repo := NewDownloadLinkRepository(db)
			hash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			ctx := context.Background()
			require.NoError(t, repo.InsertOnly(ctx, mkDownloadLink(hash, "main", grab.LinkSourceWebhook)))

			// Second insert (same hash) — different source/episode payload.
			second := mkDownloadLink(hash, "main", grab.LinkSourceArrPoll)
			second.ExternalEpisodeIDs = "[4,5]"
			require.NoError(t, repo.InsertOnly(ctx, second))

			// Row count unchanged.
			var count int64
			require.NoError(t, db.Model(&database.DownloadLinkModel{}).
				Where("qbit_hash = ?", hash).Count(&count).Error)
			assert.Equal(t, int64(1), count)

			// Original payload preserved (DoNothing → second write is no-op).
			var m database.DownloadLinkModel
			require.NoError(t, db.First(&m, "qbit_hash = ?", hash).Error)
			assert.Equal(t, "webhook", m.Source, "ON CONFLICT DO NOTHING preserves first row")
			require.NotNil(t, m.ExternalEpisodeIDs)
			assert.Equal(t, "[1,2,3]", *m.ExternalEpisodeIDs)
		})
	}
}

func TestDownloadLink_FindByHash_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range grabBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewDownloadLinkRepository(db)
			_, err := repo.FindByHash(context.Background(), domain.QbitHash("0000000000000000000000000000000000000000"))
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestDownloadLink_FindByHash_RoundTrip(t *testing.T) {
	t.Parallel()
	for _, backend := range grabBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedSonarrInstance(t, db, "main")
			repo := NewDownloadLinkRepository(db)
			hash := "cccccccccccccccccccccccccccccccccccccccc"
			require.NoError(t, repo.InsertOnly(context.Background(),
				mkDownloadLink(hash, "main", grab.LinkSourceWebhook)))

			got, err := repo.FindByHash(context.Background(), domain.QbitHash(hash))
			require.NoError(t, err)
			assert.Equal(t, domain.QbitHash(hash), got.QbitHash)
			assert.Equal(t, domain.InstanceName("main"), got.InstanceName)
			assert.Equal(t, grab.LinkSourceWebhook, got.Source)
			assert.Equal(t, "[1,2,3]", got.ExternalEpisodeIDs)
		})
	}
}

func TestDownloadLink_SetGlobalSeriesID_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range grabBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedSonarrInstance(t, db, "main")
			// global_series_id FK→series(id); seed the parent rows the
			// repo will reference.
			seedSeries(t, db, 900, "Series 900")
			seedSeries(t, db, 999, "Series 999")
			repo := NewDownloadLinkRepository(db)
			hash := "dddddddddddddddddddddddddddddddddddddddd"
			require.NoError(t, repo.InsertOnly(context.Background(),
				mkDownloadLink(hash, "main", grab.LinkSourceWebhook)))

			// First stamp succeeds.
			require.NoError(t, repo.SetGlobalSeriesID(context.Background(),
				domain.QbitHash(hash), domain.SeriesID(900)))

			var m database.DownloadLinkModel
			require.NoError(t, db.First(&m, "qbit_hash = ?", hash).Error)
			require.NotNil(t, m.GlobalSeriesID)
			assert.Equal(t, int64(900), *m.GlobalSeriesID)

			// Second stamp with a different id MUST NOT overwrite — the
			// WHERE global_series_id IS NULL guard makes it a silent no-op.
			require.NoError(t, repo.SetGlobalSeriesID(context.Background(),
				domain.QbitHash(hash), domain.SeriesID(999)))

			require.NoError(t, db.First(&m, "qbit_hash = ?", hash).Error)
			require.NotNil(t, m.GlobalSeriesID)
			assert.Equal(t, int64(900), *m.GlobalSeriesID, "first-write-wins on global_series_id")
		})
	}
}

func TestDownloadLink_SetGlobalSeriesID_MissingRowIsNoOp(t *testing.T) {
	t.Parallel()
	for _, backend := range grabBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewDownloadLinkRepository(db)
			require.NoError(t, repo.SetGlobalSeriesID(context.Background(),
				domain.QbitHash("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
				domain.SeriesID(123)))
		})
	}
}

func TestDownloadLink_ListByInstance_Filters(t *testing.T) {
	t.Parallel()
	for _, backend := range grabBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedSonarrInstance(t, db, "main")
			seedSonarrInstance(t, db, "4k")
			repo := NewDownloadLinkRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.InsertOnly(ctx, mkDownloadLink(
				"1111111111111111111111111111111111111111", "main", grab.LinkSourceWebhook)))
			require.NoError(t, repo.InsertOnly(ctx, mkDownloadLink(
				"2222222222222222222222222222222222222222", "main", grab.LinkSourceArrPoll)))
			require.NoError(t, repo.InsertOnly(ctx, mkDownloadLink(
				"3333333333333333333333333333333333333333", "4k", grab.LinkSourceWebhook)))

			// instance=main, no source filter → 2 rows
			got, err := repo.ListByInstance(ctx, domain.InstanceName("main"), nil, 100)
			require.NoError(t, err)
			assert.Len(t, got, 2)
			for _, l := range got {
				assert.Equal(t, domain.InstanceName("main"), l.InstanceName)
			}

			// instance=main, source=webhook → 1 row
			webhook := grab.LinkSourceWebhook
			got, err = repo.ListByInstance(ctx, domain.InstanceName("main"), &webhook, 100)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, grab.LinkSourceWebhook, got[0].Source)

			// instance=4k → 1 row
			got, err = repo.ListByInstance(ctx, domain.InstanceName("4k"), nil, 100)
			require.NoError(t, err)
			require.Len(t, got, 1)
		})
	}
}

func TestDownloadLink_ListByInstance_ClampsLimit(t *testing.T) {
	t.Parallel()
	for _, backend := range grabBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewDownloadLinkRepository(db)

			// limit <= 0 clamps to MaxListLimit — empty DB still returns empty.
			got, err := repo.ListByInstance(context.Background(), "main", nil, 0)
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}

func TestDownloadLink_InsertOnly_ClosedDB_ReturnsError(t *testing.T) {
	t.Parallel()
	for _, backend := range grabBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewDownloadLinkRepository(db)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			require.NoError(t, sqlDB.Close())

			err = repo.InsertOnly(context.Background(), mkDownloadLink(
				"ffffffffffffffffffffffffffffffffffffffff", "main", grab.LinkSourceWebhook))
			require.Error(t, err)
		})
	}
}
