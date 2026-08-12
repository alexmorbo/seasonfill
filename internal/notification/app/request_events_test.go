package app

import (
	"strings"
	"testing"
)

// TestRequestEventTypes_EnumSync guards the Ф8-U-2 enum-sync invariant: the two
// request event types MUST be in KnownEventTypes (else subscription 400) and
// Render MUST return a non-default (non-raw) title for each.
func TestRequestEventTypes_EnumSync(t *testing.T) {
	t.Parallel()
	for _, et := range []string{"request.approved", "request.denied"} {
		if _, ok := KnownEventTypes[et]; !ok {
			t.Fatalf("KnownEventTypes missing %q — subscription would 400", et)
		}
		msg := Render(et, []byte(`{"request_id":7,"media_type":"tv"}`))
		if strings.Contains(msg.Title, et) {
			t.Fatalf("Render(%q) returned default (raw event_type) title %q — missing render case", et, msg.Title)
		}
		if msg.Title == "" || msg.Body == "" {
			t.Fatalf("Render(%q) returned empty title/body", et)
		}
	}
}

// TestRequestEventTypes_InDefaultSet asserts both types are also in the default
// subscription set (parity with the FE DEFAULT_EVENT_TYPES).
func TestRequestEventTypes_InDefaultSet(t *testing.T) {
	t.Parallel()
	want := map[string]bool{"request.approved": false, "request.denied": false}
	for _, et := range DefaultEventTypes {
		if _, ok := want[et]; ok {
			want[et] = true
		}
	}
	for et, found := range want {
		if !found {
			t.Fatalf("DefaultEventTypes missing %q", et)
		}
	}
}
