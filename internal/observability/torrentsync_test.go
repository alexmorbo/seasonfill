package observability

import (
	"bytes"
	"strings"
	"testing"

	"github.com/VictoriaMetrics/metrics"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/qbit"
)

func dumpRegistry() string {
	buf := &bytes.Buffer{}
	metrics.WritePrometheus(buf, true)
	return buf.String()
}

func TestObserveTorrentsyncRefreshDuration_OK(t *testing.T) {
	ObserveTorrentsyncRefreshDuration("alpha", TorrentsyncOutcomeOK, 0.42)
	body := dumpRegistry()
	const want = `seasonfill_torrentsync_refresh_duration_seconds_count{instance="alpha",outcome="ok"}`
	if !strings.Contains(body, want) {
		t.Fatalf("missing histogram line %q in dump:\n%s", want, body)
	}
}

func TestObserveTorrentsyncRefreshDuration_Error(t *testing.T) {
	ObserveTorrentsyncRefreshDuration("alpha", TorrentsyncOutcomeError, 30)
	body := dumpRegistry()
	const want = `seasonfill_torrentsync_refresh_duration_seconds_count{instance="alpha",outcome="error"}`
	if !strings.Contains(body, want) {
		t.Fatalf("missing histogram line %q in dump:\n%s", want, body)
	}
}

func TestSetTorrentsyncTorrentsByState_EveryGroup(t *testing.T) {
	groups := []qbit.StateGroup{
		qbit.StateGroupDownloading, qbit.StateGroupSeeding, qbit.StateGroupStalled,
		qbit.StateGroupQueued, qbit.StateGroupPaused, qbit.StateGroupChecking,
		qbit.StateGroupError, qbit.StateGroupUnknown,
	}
	for i, g := range groups {
		SetTorrentsyncTorrentsByState("alpha", g, i+1)
	}
	body := dumpRegistry()
	for _, g := range groups {
		want := `seasonfill_torrentsync_torrents_total{instance="alpha",state="` + string(g) + `"}`
		if !strings.Contains(body, want) {
			t.Errorf("missing gauge line %q in dump:\n%s", want, body)
		}
	}
}

func TestAddTorrentsyncDelta_AllOps(t *testing.T) {
	AddTorrentsyncDelta("beta", TorrentsyncDeltaOpInsert, 5)
	AddTorrentsyncDelta("beta", TorrentsyncDeltaOpUpdate, 12)
	AddTorrentsyncDelta("beta", TorrentsyncDeltaOpDelete, 1)
	body := dumpRegistry()
	for _, op := range []string{"insert", "update", "delete"} {
		want := `seasonfill_torrentsync_delta_total{instance="beta",op="` + op + `"}`
		if !strings.Contains(body, want) {
			t.Errorf("missing counter line %q in dump:\n%s", want, body)
		}
	}
}

func TestAddTorrentsyncDelta_ZeroNIsNoOp(t *testing.T) {
	// Use a unique instance label so prior tests' counters do not
	// pollute the line-value extraction below. WritePrometheus emits
	// runtime metrics that drift between calls, so a literal before/after
	// dump comparison flakes — assert directly on the named counter line.
	AddTorrentsyncDelta("noop_inst", TorrentsyncDeltaOpInsert, 4)
	const line = `seasonfill_torrentsync_delta_total{instance="noop_inst",op="insert"} 4`
	if !strings.Contains(dumpRegistry(), line) {
		t.Fatalf("baseline counter value missing: want %q", line)
	}
	AddTorrentsyncDelta("noop_inst", TorrentsyncDeltaOpInsert, 0)
	AddTorrentsyncDelta("noop_inst", TorrentsyncDeltaOpInsert, -3)
	if !strings.Contains(dumpRegistry(), line) {
		t.Fatalf("zero/negative n must be a no-op; counter advanced past 4")
	}
}

func TestSetTorrentsyncLastRefreshAt(t *testing.T) {
	SetTorrentsyncLastRefreshAt("alpha", 1700000000)
	body := dumpRegistry()
	const want = `seasonfill_torrentsync_last_refresh_at_seconds{instance="alpha"}`
	if !strings.Contains(body, want) {
		t.Fatalf("missing gauge line %q in dump:\n%s", want, body)
	}
}

func TestAddTorrentsyncUnmappedDetected_Increments(t *testing.T) {
	AddTorrentsyncUnmappedDetected("alpha", 4)
	body := dumpRegistry()
	const want = `seasonfill_torrentsync_unmapped_total{instance="alpha"}`
	if !strings.Contains(body, want) {
		t.Fatalf("missing counter line %q in dump:\n%s", want, body)
	}
}

func TestTorrentsyncMetricsAdapter_DispatchesEveryMethod(t *testing.T) {
	a := TorrentsyncMetricsAdapter{}
	a.SetTorrentsyncUnmapped("gamma", 7)
	a.ObserveRefreshDuration("gamma", TorrentsyncOutcomeOK, 0.1)
	a.SetTorrentsByState("gamma", qbit.StateGroupSeeding, 9)
	a.AddDelta("gamma", TorrentsyncDeltaOpInsert, 2)
	a.SetLastRefreshAt("gamma", 1700000001)
	a.AddUnmappedDetected("gamma", 3)
	body := dumpRegistry()
	wants := []string{
		`seasonfill_torrentsync_unmapped{instance="gamma"}`,
		`seasonfill_torrentsync_refresh_duration_seconds_count{instance="gamma",outcome="ok"}`,
		`seasonfill_torrentsync_torrents_total{instance="gamma",state="seeding"}`,
		`seasonfill_torrentsync_delta_total{instance="gamma",op="insert"}`,
		`seasonfill_torrentsync_last_refresh_at_seconds{instance="gamma"}`,
		`seasonfill_torrentsync_unmapped_total{instance="gamma"}`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("adapter missed dispatch for %q:\n%s", w, body)
		}
	}
}
