package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRender_KnownEvents(t *testing.T) {
	t.Parallel()
	cases := []struct {
		event   string
		payload string
		want    string // substring expected in body
	}{
		{"grab.failed", `{"series_title":"Foo","season":2,"error":"no release"}`, "Foo"},
		{"import.failed", `{"series_title":"Bar","season":3,"message":"disk full"}`, "disk full"},
		{"grab.ok", `{"series_title":"Baz","season":1,"indexer":"nzb"}`, "nzb"},
		{"watchdog.regrab", `{"series_title":"Qux","season":4}`, "Qux"},
		{"inbox.dead_letter", `{"inbox_id":7,"event_type":"inbox.dead_letter"}`, "7"},
		{"season.premiere", `{"series_title":"Foo","season":2,"air_date":"2026-09-01"}`, "2026-09-01"},
		{"air_date.announced", `{"series_title":"Bar","air_date":"2026-09-15"}`, "2026-09-15"},
		{"digest.weekly", `{"from":"2026-08-09","to":"2026-08-16","premiere_count":2,"finale_count":1}`, "премьер"},
	}
	for _, c := range cases {
		t.Run(c.event, func(t *testing.T) {
			t.Parallel()
			msg := Render(c.event, []byte(c.payload))
			assert.NotEmpty(t, msg.Title)
			assert.NotEmpty(t, msg.Body)
			assert.Contains(t, msg.Body, c.want)
			assert.True(t, strings.HasPrefix(msg.Title, "Seasonfill:"))
		})
	}
}

func TestRender_UnknownEvent_Generic(t *testing.T) {
	t.Parallel()
	raw := `{"x":1}`
	msg := Render("some.future.event", []byte(raw))
	assert.Equal(t, "Seasonfill: some.future.event", msg.Title)
	assert.Equal(t, raw, msg.Body)
}

func TestRender_MalformedPayload_NoPanic(t *testing.T) {
	t.Parallel()
	assert.NotPanics(t, func() {
		msg := Render("grab.failed", []byte("not json"))
		assert.NotEmpty(t, msg.Title)
	})
	assert.NotPanics(t, func() {
		Render("grab.failed", nil)
	})
}
