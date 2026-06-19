package grab

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestStatus_Constants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, Status("grabbed"), StatusGrabbed)
	assert.Equal(t, Status("grab_failed"), StatusGrabFailed)
	assert.Equal(t, Status("imported"), StatusImported)
	assert.Equal(t, Status("import_failed"), StatusImportFailed)
}

func TestRecord_Fields(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	hash := domain.QbitHash("0123456789abcdef0123456789abcdef01234567")
	r := Record{
		ID:          id,
		Status:      StatusGrabbed,
		Attempts:    1,
		DownloadID:  "ABC",
		TorrentHash: &hash,
	}
	assert.Equal(t, id, r.ID)
	assert.Equal(t, StatusGrabbed, r.Status)
	assert.Equal(t, 1, r.Attempts)
	assert.Equal(t, "ABC", r.DownloadID)
	require.NotNil(t, r.TorrentHash)
	assert.Equal(t, hash, *r.TorrentHash)
}

func TestRecord_TorrentHashNilByDefault(t *testing.T) {
	t.Parallel()
	r := Record{ID: uuid.New(), Status: StatusGrabbed}
	assert.Nil(t, r.TorrentHash, "zero-value Record must have nil TorrentHash (NULL semantic preserved)")
}

func TestStatus_IsTerminal(t *testing.T) {
	t.Parallel()
	for in, want := range map[Status]bool{
		StatusGrabbed:      false,
		StatusGrabFailed:   true,
		StatusImported:     true,
		StatusImportFailed: true,
		Status("garbage"):  false,
	} {
		assert.Equal(t, want, in.IsTerminal(), "IsTerminal(%q)", in)
	}
}

func TestStatus_CanTransitionTo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		from, to Status
		ok       bool
	}{
		{StatusGrabbed, StatusGrabbed, true},
		{StatusGrabbed, StatusImported, true},
		{StatusGrabbed, StatusImportFailed, true},
		{StatusGrabbed, StatusGrabFailed, false},
		{StatusGrabbed, Status("garbage"), false},
		{StatusImported, StatusGrabbed, false},
		{StatusImported, StatusImported, false},
		{StatusImported, StatusImportFailed, false},
		{StatusImportFailed, StatusGrabbed, false},
		{StatusImportFailed, StatusImported, false},
		{StatusGrabFailed, StatusGrabbed, false},
		{StatusGrabFailed, StatusImported, false},
	}
	for _, c := range cases {
		assert.Equal(t, c.ok, c.from.CanTransitionTo(c.to),
			"%q.CanTransitionTo(%q)", c.from, c.to)
	}
}

func TestErrInvalidStatusTransition_IsSentinel(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("ctx: %w", ErrInvalidStatusTransition)
	assert.True(t, errors.Is(wrapped, ErrInvalidStatusTransition))
}

func TestParseTorrentHash(t *testing.T) {
	t.Parallel()
	const validLower = "0123456789abcdef0123456789abcdef01234567"
	const validUpper = "0123456789ABCDEF0123456789ABCDEF01234567"
	const validMixed = "0123456789AbCdEf0123456789aBcDeF01234567"
	lower := domain.QbitHash(validLower) // Store as variable to take address

	cases := []struct {
		name string
		in   string
		want *domain.QbitHash
	}{
		{"empty string", "", nil},
		{"only whitespace", "   \t\n", nil},
		{"too short", "abc123", nil},
		{"39 chars", "0123456789abcdef0123456789abcdef0123456", nil},
		{"41 chars", "0123456789abcdef0123456789abcdef012345678", nil},
		{"non-hex chars (g-z)", "0123456789abcdefghij0123456789abcdef0123", nil},
		{"non-hex symbol", "0123456789abcdef0123456789abcdef0123-456", nil},
		{"valid lowercase", validLower, &lower},
		{"valid uppercase normalised", validUpper, &lower},
		{"valid mixed-case normalised", validMixed, &lower},
		{"valid wrapped in whitespace", "  " + validLower + "  ", &lower},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseTorrentHash(tc.in)
			if tc.want == nil {
				assert.Nil(t, got, "expected nil for %q", tc.in)
				return
			}
			require.NotNil(t, got, "expected non-nil for %q", tc.in)
			assert.Equal(t, *tc.want, *got)
			assert.Len(t, string(*got), 40)
		})
	}
}

func TestRecord_SizeBytesNilZeroValue(t *testing.T) {
	r := Record{}
	if r.SizeBytes != nil {
		t.Fatalf("zero-value SizeBytes should be nil, got %v", r.SizeBytes)
	}
}

func TestRecord_SizeBytesPointerRoundTrip(t *testing.T) {
	var b int64 = 13_325_829_734
	r := Record{SizeBytes: &b}
	if r.SizeBytes == nil || *r.SizeBytes != b {
		t.Fatalf("SizeBytes round-trip failed: %v != %d", r.SizeBytes, b)
	}
}
