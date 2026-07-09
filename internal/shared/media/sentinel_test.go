package media

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsSentinel(t *testing.T) {
	t.Parallel()
	assert.False(t, IsSentinel(nil), "nil is not the sentinel")
	other := "deadbeef"
	assert.False(t, IsSentinel(&other))
	s := SentinelMissingHash
	assert.True(t, IsSentinel(&s))
}
