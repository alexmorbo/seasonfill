package enrichment

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNextAttemptAt_Matrix(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	hr := time.Hour
	cases := []struct {
		name     string
		attempts int
		want     time.Duration
	}{
		{"negative clamps to 0", -1, hr},
		{"attempt 0 -> 1h", 0, hr},
		{"attempt 1 -> 2h", 1, 2 * hr},
		{"attempt 2 -> 4h", 2, 4 * hr},
		{"attempt 3 -> 8h", 3, 8 * hr},
		{"attempt 4 -> 16h", 4, 16 * hr},
		{"attempt 5 -> 24h clamp", 5, 24 * hr},
		{"attempt 6 -> 24h clamp", 6, 24 * hr},
		{"attempt 10 -> 24h clamp", 10, 24 * hr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := NextAttemptAt(tc.attempts, base)
			assert.Equal(t, base.Add(tc.want), got)
		})
	}
}

func TestNextAttemptAt_Monotonic(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	prev := base
	for i := range 10 {
		got := NextAttemptAt(i, base)
		assert.True(t, !got.Before(prev),
			"attempts=%d: NextAttemptAt=%s should be >= prev=%s",
			i, got, prev)
		prev = got
	}
}

func TestNextAttemptAt_Clamp24h(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	// attempts >= 5 must saturate at exactly 24h.
	for _, n := range []int{5, 6, 7, 10, 100} {
		got := NextAttemptAt(n, base)
		assert.Equal(t, base.Add(24*time.Hour), got,
			"attempts=%d should clamp", n)
	}
}
