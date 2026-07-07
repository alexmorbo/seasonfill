package seriesdetail

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
)

// W18-16 SWR. A warm row (full hydration + fresh skeleton_synced_at) must NOT
// block: the skeleton freshen is dispatched ModeAsync so the response serves the
// current canon immediately. A cold row (never skeleton-synced) blocks ModeSync
// for first paint.
func TestSkeletonComposer_SWR_ColdBlocks_StaleServesAsync(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	t.Run("warm + fresh skeleton clock → ModeAsync (serve stale immediately)", func(t *testing.T) {
		t.Parallel()
		canon := skBaseCanon()
		fresh := now.Add(-1 * time.Hour) // continuing, well within the 2d TTL
		canon.SkeletonSyncedAt = &fresh
		deps, sf, _ := skBaseDeps(canon)
		deps.Now = func() time.Time { return now }

		sc := NewSkeletonComposer(deps)
		_, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
		require.NoError(t, err)

		require.Equal(t, ModeAsync, modeForSection(sf, freshener.SectionSkeleton),
			"warm row must NOT block the response on the skeleton refresh")
	})

	t.Run("cold (nil skeleton clock) → ModeSync (block first paint)", func(t *testing.T) {
		t.Parallel()
		canon := skBaseCanon() // SkeletonSyncedAt nil ⇒ cold
		deps, sf, _ := skBaseDeps(canon)
		deps.Now = func() time.Time { return now }

		sc := NewSkeletonComposer(deps)
		_, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
		require.NoError(t, err)

		require.Equal(t, ModeSync, modeForSection(sf, freshener.SectionSkeleton),
			"cold row must block for the first paint")
	})

	t.Run("stub hydration → ModeSync even if skeleton clock set", func(t *testing.T) {
		t.Parallel()
		canon := skBaseCanon()
		canon.Hydration = series.HydrationStub
		fresh := now.Add(-1 * time.Hour)
		canon.SkeletonSyncedAt = &fresh
		deps, sf, _ := skBaseDeps(canon)
		deps.Now = func() time.Time { return now }

		sc := NewSkeletonComposer(deps)
		_, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
		require.NoError(t, err)

		require.Equal(t, ModeSync, modeForSection(sf, freshener.SectionSkeleton))
	})
}

// modeForSection scans the spyFreshener's recorded calls for the one whose scope
// contains the given section and returns its mode. The skeleton scope is
// dispatched as its OWN call ([]{SectionSkeleton}); Overview/Cast/Media are a
// separate ModeAsync call.
func modeForSection(sf *spyFreshener, section freshener.Section) EnsureFreshMode {
	for _, c := range sf.calls {
		if len(c.sections) == 1 && c.sections[0] == section {
			return c.mode
		}
	}
	return EnsureFreshMode(-1)
}
