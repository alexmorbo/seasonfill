package rest

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/config"
)

func TestInstanceRegistry_NilLoad_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	var r InstanceRegistry
	assert.Empty(t, r.snapshot())
}

func TestInstanceRegistry_Load_PicksUpMutations(t *testing.T) {
	t.Parallel()
	state := map[string]scan.Instance{
		"alpha": {Config: config.SonarrInstance{Name: "alpha", URL: "http://a"}},
	}
	r := InstanceRegistry{Load: func() map[string]scan.Instance { return state }}

	first := r.snapshot()
	assert.Contains(t, first, "alpha")

	// Simulate a reload-bus snapshot replacement.
	state = map[string]scan.Instance{
		"beta": {Config: config.SonarrInstance{Name: "beta", URL: "http://b"}},
	}
	second := r.snapshot()
	assert.NotContains(t, second, "alpha", "handlers must see the new snapshot")
	assert.Contains(t, second, "beta")
}
