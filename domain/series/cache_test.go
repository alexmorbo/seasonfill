package series

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCacheEntry_IsActive_NilDeletedAt(t *testing.T) {
	t.Parallel()
	e := CacheEntry{InstanceName: "alpha", SonarrSeriesID: 1}
	assert.True(t, e.IsActive())
}

func TestCacheEntry_IsActive_DeletedAtSet(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	e := CacheEntry{InstanceName: "alpha", SonarrSeriesID: 1, DeletedAt: &now}
	assert.False(t, e.IsActive())
}
