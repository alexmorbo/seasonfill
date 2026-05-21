package webhook

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEventType_Constants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, EventType("grabbed"), EventTypeGrabbed)
	assert.Equal(t, EventType("imported"), EventTypeImported)
	assert.Equal(t, EventType("import_failed"), EventTypeImportFailed)
	assert.Equal(t, EventType("unsupported"), EventTypeUnsupported)
}

func TestEventType_IsConsumed(t *testing.T) {
	t.Parallel()
	cases := map[EventType]bool{
		EventTypeGrabbed:      true,
		EventTypeImported:     true,
		EventTypeImportFailed: true,
		EventTypeUnsupported:  false,
		EventType("future"):   false,
	}
	for et, want := range cases {
		et, want := et, want
		t.Run(string(et), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, want, et.IsConsumed())
		})
	}
}

func TestEventType_IsTerminal(t *testing.T) {
	t.Parallel()
	cases := map[EventType]bool{
		EventTypeGrabbed:      false,
		EventTypeImported:     true,
		EventTypeImportFailed: true,
		EventTypeUnsupported:  false,
	}
	for et, want := range cases {
		et, want := et, want
		t.Run(string(et), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, want, et.IsTerminal())
		})
	}
}

func TestEvent_ZeroValueIsUnsupported(t *testing.T) {
	t.Parallel()
	var e Event
	assert.Equal(t, EventType(""), e.Type)
	assert.False(t, e.Type.IsConsumed())
	assert.False(t, e.Type.IsTerminal())
}
