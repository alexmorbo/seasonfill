//go:build integration

// S-D — verifies migration 000027 (drop_dead_i18n) drops networks_i18n +
// production_companies_i18n while keeping the canon networks /
// production_companies tables, and that the down-migration restores both
// side-tables EMPTY. Decision O-3.
package integration

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSD_DropDeadI18nMigrationRoundTrip(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			require.NoError(t, m.Up(), "Up() should apply through 000027 on %s", b.name)

			// After 000027: the two dead i18n tables are ABSENT, the canon
			// parents remain PRESENT.
			live := liveTableNames(t, ctx, db, b.name)
			require.NotContains(t, live, "networks_i18n",
				"networks_i18n must be dropped by 000027 on %s", b.name)
			require.NotContains(t, live, "production_companies_i18n",
				"production_companies_i18n must be dropped by 000027 on %s", b.name)
			require.Contains(t, live, "networks",
				"canon networks table must survive 000027 on %s", b.name)
			require.Contains(t, live, "production_companies",
				"canon production_companies table must survive 000027 on %s", b.name)
			require.True(t, slices.Contains(live, "genres_i18n") && slices.Contains(live, "keywords_i18n"),
				"surviving i18n siblings genres_i18n + keywords_i18n must remain on %s", b.name)

			// DOWN one step (000027 -> 000026): both side-tables reappear EMPTY.
			require.NoError(t, m.Steps(-1),
				"Steps(-1) should reverse 000027 on %s", b.name)

			liveAfter := liveTableNames(t, ctx, db, b.name)
			require.Contains(t, liveAfter, "networks_i18n",
				"networks_i18n must be recreated by 000027 down on %s", b.name)
			require.Contains(t, liveAfter, "production_companies_i18n",
				"production_companies_i18n must be recreated by 000027 down on %s", b.name)

			var netCount, pcCount int64
			require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM networks_i18n").Scan(&netCount))
			require.Equal(t, int64(0), netCount, "recreated networks_i18n must be empty on %s", b.name)
			require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM production_companies_i18n").Scan(&pcCount))
			require.Equal(t, int64(0), pcCount, "recreated production_companies_i18n must be empty on %s", b.name)
		})
	}
}
