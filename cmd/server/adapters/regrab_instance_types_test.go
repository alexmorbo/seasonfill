package adapters

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

func TestRegrabInstanceTypes_UnionsBothHolders(t *testing.T) {
	t.Parallel()
	sonarr := NewInstanceMapHolder(map[string]scan.Instance{
		"homelab": {Config: runtime.InstanceSnapshot{Name: "homelab", Type: "sonarr"}},
	})
	radarr := NewRadarrInstanceMapHolder(map[string]scan.RadarrInstance{
		"radarr": {Config: runtime.InstanceSnapshot{Name: "radarr", Type: "radarr"}},
	})

	got := NewRegrabInstanceTypes(sonarr, radarr).InstanceTypes()

	assert.Equal(t, map[string]string{"homelab": "sonarr", "radarr": "radarr"}, got)
}

// An empty arr_instance.type (legacy rows) resolves to the type the holder
// itself implies — same rule as internal/runtime/snapshot.go:341-342 and
// scan.IsRadarr.
func TestRegrabInstanceTypes_EmptyTypeFallsBackToHolderKind(t *testing.T) {
	t.Parallel()
	sonarr := NewInstanceMapHolder(map[string]scan.Instance{
		"legacy": {Config: runtime.InstanceSnapshot{Name: "legacy"}},
	})
	radarr := NewRadarrInstanceMapHolder(map[string]scan.RadarrInstance{
		"legacy_movies": {Config: runtime.InstanceSnapshot{Name: "legacy_movies"}},
	})

	got := NewRegrabInstanceTypes(sonarr, radarr).InstanceTypes()

	assert.Equal(t, "sonarr", got["legacy"])
	assert.Equal(t, "radarr", got["legacy_movies"])
}

// A nil holder contributes nothing and must not panic — the loop then reads
// the missing names as "unknown" and fails open.
func TestRegrabInstanceTypes_NilHoldersAreSafe(t *testing.T) {
	t.Parallel()
	assert.Empty(t, NewRegrabInstanceTypes(nil, nil).InstanceTypes())
	assert.Empty(t, RegrabInstanceTypes{}.InstanceTypes())

	radarr := NewRadarrInstanceMapHolder(map[string]scan.RadarrInstance{
		"radarr": {Config: runtime.InstanceSnapshot{Name: "radarr", Type: "radarr"}},
	})
	got := NewRegrabInstanceTypes(nil, radarr).InstanceTypes()
	assert.Equal(t, map[string]string{"radarr": "radarr"}, got)
}

// The adapter must be reload-aware: a Replace on the holder is visible on
// the next InstanceTypes call, because that is the whole reason the loop
// re-reads it on every swap.
func TestRegrabInstanceTypes_ReflectsHolderReplace(t *testing.T) {
	t.Parallel()
	radarr := NewRadarrInstanceMapHolder(nil)
	src := NewRegrabInstanceTypes(nil, radarr)
	assert.Empty(t, src.InstanceTypes())

	radarr.Replace(map[string]scan.RadarrInstance{
		"radarr": {Config: runtime.InstanceSnapshot{Name: "radarr", Type: "radarr"}},
	})
	assert.Equal(t, map[string]string{"radarr": "radarr"}, src.InstanceTypes())
}
