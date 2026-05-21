package grab

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
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
	r := Record{ID: id, Status: StatusGrabbed, Attempts: 1, DownloadID: "ABC"}
	assert.Equal(t, id, r.ID)
	assert.Equal(t, StatusGrabbed, r.Status)
	assert.Equal(t, 1, r.Attempts)
	assert.Equal(t, "ABC", r.DownloadID)
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
