package dataports

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursor_Roundtrip(t *testing.T) {
	t.Parallel()
	orig := &Cursor{
		Timestamp: time.Date(2026, 5, 21, 14, 30, 45, 123456789, time.UTC),
		ID:        "8f3a3b6e-1234-4abc-9def-000000000001",
	}
	decoded, err := ParseCursor(orig.String())
	require.NoError(t, err)
	require.NotNil(t, decoded)
	assert.True(t, decoded.Timestamp.Equal(orig.Timestamp))
	assert.Equal(t, orig.ID, decoded.ID)
}

func TestCursor_String_NilReceiver(t *testing.T) {
	t.Parallel()
	var c *Cursor
	assert.Empty(t, c.String())
}

func TestParseCursor_EmptyOrWhitespace(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "   ", "\t"} {
		got, err := ParseCursor(in)
		require.NoError(t, err, in)
		assert.Nil(t, got, in)
	}
}

func TestParseCursor_MalformedBase64(t *testing.T) {
	t.Parallel()
	_, err := ParseCursor("!!!not-base64!!!")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidCursor))
}

func TestParseCursor_MalformedJSON(t *testing.T) {
	t.Parallel()
	// base64url of "not json"
	_, err := ParseCursor("bm90IGpzb24")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidCursor))
}

func TestParseCursor_MissingFields(t *testing.T) {
	t.Parallel()
	// Zero-value Cursor encodes to a payload with empty ID — must reject.
	c := &Cursor{}
	_, err := ParseCursor(c.String())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidCursor))
}

func TestParseCursor_BadTimestamp(t *testing.T) {
	t.Parallel()
	// base64url of `{"ts":"not-a-time","id":"x"}`
	_, err := ParseCursor("eyJ0cyI6Im5vdC1hLXRpbWUiLCJpZCI6IngifQ")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidCursor))
}

func TestCursor_String_URLSafeNoPadding(t *testing.T) {
	t.Parallel()
	enc := (&Cursor{
		Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		ID:        "abc",
	}).String()
	assert.False(t, strings.ContainsAny(enc, "=+/"))
}
