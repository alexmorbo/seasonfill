package seriesdetail

import (
	"testing"

	"github.com/stretchr/testify/assert"

	mediaapp "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	"github.com/alexmorbo/seasonfill/internal/observability"
)

func TestClassifyRecPosterSentinel(t *testing.T) {
	t.Parallel()
	sentinel := mediaapp.SentinelMissingHash

	cases := []struct {
		name        string
		resolved    *string
		rowPresent  bool
		rawNonEmpty bool
		wantReason  string
		wantHit     bool
	}{
		{"nil_resolved_is_not_sentinel", nil, false, false, "", false},
		{"real_hash_is_not_sentinel", new("abc123realhash"), true, true, "", false},
		{"sentinel_no_row", new(sentinel), false, false, observability.RecPosterSentinelNoRow, true},
		{"sentinel_empty_poster_row", new(sentinel), true, false, observability.RecPosterSentinelEmptyPosterRow, true},
		{"sentinel_resolver_miss", new(sentinel), true, true, observability.RecPosterSentinelResolverMiss, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, hit := classifyRecPosterSentinel(tc.resolved, tc.rowPresent, tc.rawNonEmpty)
			assert.Equal(t, tc.wantHit, hit)
			assert.Equal(t, tc.wantReason, reason)
		})
	}
}
