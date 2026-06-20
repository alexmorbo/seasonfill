package errtext

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClamp_ShortReturnsUnchanged(t *testing.T) {
	t.Parallel()
	s := "sonarr /api/v3/release returned status=500 body=oops"
	assert.Equal(t, s, Clamp(s))
}

func TestClamp_EmptyReturnsEmpty(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", Clamp(""))
}

func TestClamp_ExactBoundaryUnchanged(t *testing.T) {
	t.Parallel()
	s := strings.Repeat("A", MaxBytes)
	assert.Equal(t, s, Clamp(s))
	assert.Len(t, Clamp(s), MaxBytes)
}

func TestClamp_OneOverBoundaryTruncates(t *testing.T) {
	t.Parallel()
	s := strings.Repeat("A", MaxBytes+1)
	got := Clamp(s)
	assert.NotEqual(t, s, got)
	assert.Contains(t, got, "(truncated 1 bytes)")
	// First MaxBytes bytes preserved verbatim.
	assert.Equal(t, strings.Repeat("A", MaxBytes), got[:MaxBytes])
}

func TestClamp_LargeStackTraceTruncatesWithCount(t *testing.T) {
	t.Parallel()
	// 8 KiB upstream blob — exactly the kind of Sonarr stack trace
	// that motivated this story.
	const total = 8192
	s := strings.Repeat("X", total)
	got := Clamp(s)
	assert.Contains(t, got, "(truncated 4096 bytes)")
	assert.Equal(t, strings.Repeat("X", MaxBytes), got[:MaxBytes])
}

func TestClamp_FullErrorRoundTripPreservesNewlinesUnderCap(t *testing.T) {
	t.Parallel()
	// Multi-line short error — the drawer renders newlines verbatim
	// via whitespace-pre-wrap, so Clamp must not flatten them.
	s := "sonarr /api/v3/release returned status=500 body={\n  \"message\": \"Download client failed\",\n  \"description\": \"qBit refused: dial tcp 10.0.42.7:10095: i/o timeout\"\n}"
	assert.Equal(t, s, Clamp(s))
	assert.Contains(t, Clamp(s), "\n  \"message\"")
}
