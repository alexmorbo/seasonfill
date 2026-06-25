package seriesdetail_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
)

// Story 541 — CastFullPageDefaultLimit is exported so the rest handler
// reads the same number. 200 is a load-bearing budget (≤60KB payload).
func TestCastFullPageDefaultLimit_Value(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 200, seriesdetail.CastFullPageDefaultLimit,
		"Story 541 promises 200; updating this value REQUIRES updating the docstring + LOAD budget reasoning")
}
