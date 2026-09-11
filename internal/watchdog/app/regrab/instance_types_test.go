package regrab

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
)

func TestSupportedInstanceTypes_IsSonarrOnly(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{scan.InstanceTypeSonarr}, SupportedInstanceTypes,
		"ADR-0025 F2: regrab is series-only; movie-regrab is deferred to backlog B-41")
}

// The predicate is PURE and fail-closed; the fail-OPEN policy lives at the
// loop's single call site, not here.
func TestSupportsInstanceType(t *testing.T) {
	t.Parallel()
	assert.True(t, SupportsInstanceType(scan.InstanceTypeSonarr))
	assert.False(t, SupportsInstanceType(scan.InstanceTypeRadarr))
	assert.False(t, SupportsInstanceType(""))
	assert.False(t, SupportsInstanceType("lidarr"))
	assert.False(t, SupportsInstanceType("SONARR"))
}
