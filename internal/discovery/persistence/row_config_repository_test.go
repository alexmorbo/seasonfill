package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// seedDiscoveryRow inserts one discovery_rows row via the GORM model so
// the params jsonb/text transcode goes through the same path the repo
// reads. discovery_rows has no FK, so no parent rows are needed.
func seedDiscoveryRow(t *testing.T, db *gorm.DB, position int, rowType, source, title, params string) {
	t.Helper()
	m := discoveryRowModel{
		RowType:   rowType,
		Source:    source,
		MediaType: "tv",
		Params:    datatypes.JSON(params),
		Position:  position,
		Enabled:   true,
		Title:     title,
	}
	require.NoError(t, db.Create(&m).Error)
	require.NotZero(t, m.ID)
}

func TestRowConfigRepository_List_Empty(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewRowConfigRepository(db)

			rows, err := repo.List(context.Background())
			require.NoError(t, err)
			assert.NotNil(t, rows)
			assert.Len(t, rows, 0)
		})
	}
}

func TestRowConfigRepository_List_OrderedByPosition(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewRowConfigRepository(db)

			// Insert out-of-order: positions 2, 0, 1.
			seedDiscoveryRow(t, db, 2, string(disco.RowTypeNetwork), string(disco.SourceTMDBDiscover), "Netflix", `{"with_networks":"213"}`)
			seedDiscoveryRow(t, db, 0, string(disco.RowTypeTrending), string(disco.SourceTMDBDiscover), "Тренды", `{}`)
			seedDiscoveryRow(t, db, 1, string(disco.RowTypeGenre), string(disco.SourceTMDBDiscover), "Драмы", `{"with_genres":"18"}`)

			rows, err := repo.List(context.Background())
			require.NoError(t, err)
			require.Len(t, rows, 3)

			assert.Equal(t, 0, rows[0].Position)
			assert.Equal(t, 1, rows[1].Position)
			assert.Equal(t, 2, rows[2].Position)

			assert.Equal(t, disco.RowTypeTrending, rows[0].RowType)
			assert.Equal(t, disco.RowTypeGenre, rows[1].RowType)
			assert.Equal(t, disco.RowTypeNetwork, rows[2].RowType)

			// params round-trip on the genre row.
			assert.Equal(t, map[string]string{"with_genres": "18"}, rows[1].Params)
			// enum + scalar fields carried through.
			assert.Equal(t, disco.SourceTMDBDiscover, rows[0].Source)
			assert.Equal(t, disco.MediaTypeTV, rows[0].MediaType)
			assert.True(t, rows[0].Enabled)
			assert.NotZero(t, rows[0].ID)
		})
	}
}

func TestRowConfigRepository_List_EmptyParamsObject(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewRowConfigRepository(db)

			seedDiscoveryRow(t, db, 0, string(disco.RowTypeTrending), string(disco.SourceTMDBDiscover), "Тренды", `{}`)

			rows, err := repo.List(context.Background())
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.NotNil(t, rows[0].Params)
			assert.Len(t, rows[0].Params, 0)
		})
	}
}

func TestRowConfigRepository_Replace_RoundTrip(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewRowConfigRepository(db)

			// Seed 1 pre-existing row to prove Replace clears it.
			seedDiscoveryRow(t, db, 0, string(disco.RowTypeTrending), string(disco.SourceTMDBDiscover), "Старое", `{}`)

			in := []disco.Row{
				{RowType: disco.RowTypePopular, Source: disco.SourceTMDBDiscover, MediaType: disco.MediaTypeTV, Enabled: true, Title: "Популярное", Params: map[string]string{}},
				{RowType: disco.RowTypeGenre, Source: disco.SourceTMDBDiscover, MediaType: disco.MediaTypeTV, Enabled: false, Title: "Драмы", Params: map[string]string{"with_genres": "18", "sort_by": "popularity.desc"}},
				{RowType: disco.RowTypeRecentlyAdded, Source: disco.SourceLibrary, MediaType: disco.MediaTypeTV, Enabled: true, Title: "Недавнее", Params: map[string]string{}},
			}
			require.NoError(t, repo.Replace(context.Background(), in))

			got, err := repo.List(context.Background())
			require.NoError(t, err)
			require.Len(t, got, 3)

			// Dense positions 0..2 regardless of incoming Position (all 0 above).
			for i := range got {
				assert.Equal(t, i, got[i].Position)
			}
			// Order preserved by slice order.
			assert.Equal(t, disco.RowTypePopular, got[0].RowType)
			assert.Equal(t, disco.RowTypeGenre, got[1].RowType)
			assert.Equal(t, disco.RowTypeRecentlyAdded, got[2].RowType)
			// Params JSON round-trip.
			assert.Equal(t, map[string]string{"with_genres": "18", "sort_by": "popularity.desc"}, got[1].Params)
			// Empty params → non-nil empty map.
			assert.NotNil(t, got[0].Params)
			assert.Len(t, got[0].Params, 0)
			// Scalars carried.
			assert.False(t, got[1].Enabled)
			assert.Equal(t, disco.SourceLibrary, got[2].Source)
			// Old seeded row is gone (replaced, not appended).
			for _, r := range got {
				assert.NotEqual(t, "Старое", r.Title)
			}
		})
	}
}

func TestRowConfigRepository_Replace_Empty_Clears(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewRowConfigRepository(db)

			seedDiscoveryRow(t, db, 0, string(disco.RowTypeTrending), string(disco.SourceTMDBDiscover), "Тренды", `{}`)
			seedDiscoveryRow(t, db, 1, string(disco.RowTypePopular), string(disco.SourceTMDBDiscover), "Популярное", `{}`)

			require.NoError(t, repo.Replace(context.Background(), nil))

			got, err := repo.List(context.Background())
			require.NoError(t, err)
			assert.NotNil(t, got)
			assert.Len(t, got, 0)
		})
	}
}

func TestRowConfigRepository_DeleteAll(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewRowConfigRepository(db)

			seedDiscoveryRow(t, db, 0, string(disco.RowTypeGenre), string(disco.SourceTMDBDiscover), "Драмы", `{"with_genres":"18"}`)

			require.NoError(t, repo.DeleteAll(context.Background()))
			got, err := repo.List(context.Background())
			require.NoError(t, err)
			assert.Len(t, got, 0)

			// Idempotent — second delete on empty table is a no-op success.
			require.NoError(t, repo.DeleteAll(context.Background()))
		})
	}
}
