package release

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The ranking function compares two Scored values across six keys, in priority order:
//  1. CustomFormatScore (higher wins)
//  2. Coverage          (higher wins)
//  3. IsOriginRelease   (true wins)
//  4. IndexerPriority   (lower wins; 1 is best in Sonarr)
//  5. Seeders           (higher wins)
//  6. SizeBytes         (higher wins — fallback only)
//
// Each subtest isolates exactly one key by holding the others equal.
func TestScored_Less_TieBreakers(t *testing.T) {
	t.Parallel()

	base := func() Scored {
		return Scored{
			Release: Release{
				CustomFormatScore: 100,
				IndexerPriority:   25,
				Seeders:           50,
				SizeBytes:         1_000_000_000,
			},
			Coverage:        5,
			IsOriginRelease: false,
		}
	}

	t.Run("higher CustomFormatScore wins", func(t *testing.T) {
		t.Parallel()
		a := base()
		a.Release.CustomFormatScore = 500
		b := base()
		b.Release.CustomFormatScore = 100
		assert.True(t, a.Less(b))
		assert.False(t, b.Less(a))
	})

	t.Run("higher Coverage wins when CFS equal", func(t *testing.T) {
		t.Parallel()
		a := base()
		a.Coverage = 10
		b := base()
		b.Coverage = 3
		assert.True(t, a.Less(b))
		assert.False(t, b.Less(a))
	})

	t.Run("origin release wins when CFS and Coverage equal", func(t *testing.T) {
		t.Parallel()
		a := base()
		a.IsOriginRelease = true
		b := base()
		b.IsOriginRelease = false
		assert.True(t, a.Less(b))
		assert.False(t, b.Less(a))
	})

	t.Run("lower IndexerPriority wins when above equal", func(t *testing.T) {
		t.Parallel()
		a := base()
		a.Release.IndexerPriority = 1
		b := base()
		b.Release.IndexerPriority = 50
		assert.True(t, a.Less(b))
		assert.False(t, b.Less(a))
	})

	t.Run("higher Seeders wins when above equal", func(t *testing.T) {
		t.Parallel()
		a := base()
		a.Release.Seeders = 1000
		b := base()
		b.Release.Seeders = 5
		assert.True(t, a.Less(b))
		assert.False(t, b.Less(a))
	})

	t.Run("higher SizeBytes wins as final fallback", func(t *testing.T) {
		t.Parallel()
		a := base()
		a.Release.SizeBytes = 5_000_000_000
		b := base()
		b.Release.SizeBytes = 1_000_000_000
		assert.True(t, a.Less(b))
		assert.False(t, b.Less(a))
	})

	t.Run("identical releases — Less is false in both directions", func(t *testing.T) {
		t.Parallel()
		a := base()
		b := base()
		assert.False(t, a.Less(b))
		assert.False(t, b.Less(a))
	})
}

func TestScored_Less_FullPriorityChain(t *testing.T) {
	t.Parallel()

	// Higher-priority key beats lower-priority key. Even when Seeders are vastly
	// inferior, a winning CustomFormatScore takes the candidate.
	better := Scored{
		Release: Release{
			CustomFormatScore: 500,
			IndexerPriority:   99,
			Seeders:           1,
			SizeBytes:         100,
		},
		Coverage:        1,
		IsOriginRelease: false,
	}
	worse := Scored{
		Release: Release{
			CustomFormatScore: 100,
			IndexerPriority:   1,
			Seeders:           99999,
			SizeBytes:         100_000_000_000,
		},
		Coverage:        10,
		IsOriginRelease: true,
	}

	assert.True(t, better.Less(worse), "CFS=500 must dominate even when every other key favors the alternative")
}
