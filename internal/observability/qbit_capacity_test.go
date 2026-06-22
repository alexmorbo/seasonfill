package observability

import (
	"strings"
	"testing"
)

func TestSetQbitTorrentsRows(t *testing.T) {
	SetQbitTorrentsRows("alpha", 4242)
	body := dumpRegistry()
	const want = `seasonfill_qbit_torrents_rows{instance="alpha"}`
	if !strings.Contains(body, want) {
		t.Fatalf("missing gauge line %q in dump:\n%s", want, body)
	}
}

func TestQbitCapacityAdapter_Dispatches(t *testing.T) {
	a := QbitCapacityMetricsAdapter{}
	a.SetQbitTorrentsRows("delta", 1)
	body := dumpRegistry()
	const want = `seasonfill_qbit_torrents_rows{instance="delta"}`
	if !strings.Contains(body, want) {
		t.Fatalf("adapter missed dispatch:\n%s", body)
	}
}
