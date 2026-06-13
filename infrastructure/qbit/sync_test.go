package qbit

import (
	"context"
	encjson "encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSyncQbit emulates the /api/v2/auth/login and
// /api/v2/sync/maindata endpoints autobrr exercises. The test feeds
// fixtures in via queue; the handler walks the slice once per
// request, returning each in sequence.
type fakeSyncQbit struct {
	srv    *httptest.Server
	calls  atomic.Int32
	rids   []int64
	stages [][]byte // pre-marshalled JSON bodies, one per call
}

func newFakeSyncQbit() *fakeSyncQbit {
	f := &fakeSyncQbit{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test"})
		_, _ = w.Write([]byte("Ok."))
	})
	mux.HandleFunc("/api/v2/app/version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("v4.6.0"))
	})
	mux.HandleFunc("/api/v2/sync/maindata", func(w http.ResponseWriter, r *http.Request) {
		i := int(f.calls.Add(1)) - 1
		if i >= len(f.stages) {
			http.Error(w, "no more fixtures", http.StatusInternalServerError)
			return
		}
		var rid int64
		if v := r.URL.Query().Get("rid"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err == nil {
				rid = n
			}
		}
		f.rids = append(f.rids, rid)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(f.stages[i])
	})
	f.srv = httptest.NewServer(mux)
	return f
}

func (f *fakeSyncQbit) close() { f.srv.Close() }

func (f *fakeSyncQbit) queue(payload map[string]any) {
	b, err := encjson.Marshal(payload)
	if err != nil {
		panic(err)
	}
	f.stages = append(f.stages, b)
}

// TestSyncSession_FullThenDelta exercises the rid-based delta flow:
//  1. First call: rid=0, full snapshot of 3 torrents.
//  2. Second call: rid=42, partial — one torrent's progress
//     advances, one torrent is removed (TorrentsRemoved).
//
// Asserts: Snapshot reflects the merge, Rid advances, Removed
// reports the deleted hash.
func TestSyncSession_FullThenDelta(t *testing.T) {
	t.Parallel()
	f := newFakeSyncQbit()
	defer f.close()

	// Stage 1 — full snapshot.
	f.queue(map[string]any{
		"rid":         int64(42),
		"full_update": true,
		"torrents": map[string]any{
			"aaaa11111111111111111111111111111111aaaa": map[string]any{
				"name":        "Show.S01.1080p",
				"hash":        "aaaa11111111111111111111111111111111aaaa",
				"infohash_v1": "aaaa11111111111111111111111111111111aaaa",
				"state":       "downloading",
				"progress":    0.10,
				"size":        int64(1 << 30),
				"added_on":    int64(1700000000),
				"popularity":  1.5,
			},
			"bbbb22222222222222222222222222222222bbbb": map[string]any{
				"name":        "Show.S01E02.1080p",
				"hash":        "bbbb22222222222222222222222222222222bbbb",
				"infohash_v1": "bbbb22222222222222222222222222222222bbbb",
				"state":       "stoppedUP", // qBit 5.x spelling
			},
			"cccc33333333333333333333333333333333cccc": map[string]any{
				"name":        "Show.S02.PACK",
				"hash":        "cccc33333333333333333333333333333333cccc",
				"infohash_v1": "cccc33333333333333333333333333333333cccc",
				"state":       "uploading",
			},
		},
	})

	// Stage 2 — partial: aaaa progress advances, cccc removed.
	f.queue(map[string]any{
		"rid":         int64(43),
		"full_update": false,
		"torrents": map[string]any{
			"aaaa11111111111111111111111111111111aaaa": map[string]any{
				"progress": 0.55,
				"state":    "downloading",
			},
		},
		"torrents_removed": []string{"cccc33333333333333333333333333333333cccc"},
	})

	c, err := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = c.Close() }()

	sess, err := c.NewSyncSession(context.Background())
	if err != nil {
		t.Fatalf("NewSyncSession: %v", err)
	}

	snap1, err := sess.Refresh(context.Background())
	if err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	if got, want := len(snap1.Torrents), 3; got != want {
		t.Fatalf("snapshot 1 torrent count: got %d want %d", got, want)
	}
	if snap1.Rid != 42 {
		t.Fatalf("snap1.Rid = %d, want 42", snap1.Rid)
	}
	if len(snap1.Removed) != 0 {
		t.Fatalf("snap1.Removed should be empty, got %v", snap1.Removed)
	}
	bbbb := snap1.Torrents["bbbb22222222222222222222222222222222bbbb"]
	if bbbb.StateGroup != StateGroupPaused {
		t.Fatalf("stoppedUP must map to paused, got %q", bbbb.StateGroup)
	}
	aaaa := snap1.Torrents["aaaa11111111111111111111111111111111aaaa"]
	if aaaa.Popularity != 1.5 {
		t.Fatalf("popularity decode: got %v want 1.5", aaaa.Popularity)
	}
	if aaaa.AddedOn.IsZero() {
		t.Fatal("AddedOn should be non-zero after decoding")
	}

	snap2, err := sess.Refresh(context.Background())
	if err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	if snap2.Rid != 43 {
		t.Fatalf("snap2.Rid = %d, want 43", snap2.Rid)
	}
	if len(snap2.Torrents) != 2 {
		t.Fatalf("snap2 torrent count = %d, want 2 (cccc removed)", len(snap2.Torrents))
	}
	if _, gone := snap2.Torrents["cccc33333333333333333333333333333333cccc"]; gone {
		t.Fatal("cccc should be gone from snapshot 2")
	}
	if len(snap2.Removed) != 1 || snap2.Removed[0] != "cccc33333333333333333333333333333333cccc" {
		t.Fatalf("snap2.Removed = %v, want [cccc…]", snap2.Removed)
	}
	merged := snap2.Torrents["aaaa11111111111111111111111111111111aaaa"]
	if merged.Progress != 0.55 {
		t.Fatalf("partial-merge: progress = %v, want 0.55", merged.Progress)
	}
	// Fields not in the partial must persist from full snapshot.
	if merged.Name != "Show.S01.1080p" {
		t.Fatalf("partial-merge: name should persist, got %q", merged.Name)
	}

	// Second request must have sent rid=42 (the cursor from snap1).
	if len(f.rids) < 2 || f.rids[1] != 42 {
		t.Fatalf("expected second request rid=42, got %v", f.rids)
	}
	if sess.Rid() != 43 {
		t.Fatalf("sess.Rid() = %d, want 43", sess.Rid())
	}
}

// TestNormaliseHash_V1Precedence: when both v1 and fallback are set,
// v1 wins. Cross-seed v2-only fixture: v1 empty, fallback supplied —
// fallback wins. Uppercase is lowercased.
func TestNormaliseHash_V1Precedence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		v1, fallback, want string
	}{
		{"AAAA", "bbbb", "aaaa"}, // v1 wins, lowercased
		{"", "BBBB", "bbbb"},     // v1 empty → fallback
		{"  ", "ccCC", "cccc"},   // whitespace-only v1 → fallback
		{"AaA1", "AaA2", "aaa1"}, // v1 wins even when same length
		{"", "", ""},             // both empty → empty
	}
	for _, tc := range cases {
		if got := NormaliseHash(tc.v1, tc.fallback); got != tc.want {
			t.Fatalf("NormaliseHash(%q,%q) = %q, want %q", tc.v1, tc.fallback, got, tc.want)
		}
	}
}

// TestExtractTrackerHost: full announce URL → host; malformed
// fallback path; empty → empty.
func TestExtractTrackerHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw, want string
	}{
		{"http://tracker.example.com:6969/announce", "tracker.example.com"},
		{"udp://Tracker.Example.com:80/announce", "tracker.example.com"},
		{"https://t.example.com/announce?key=abc", "t.example.com"},
		{"", ""},
		{"garbage-no-scheme", "garbage-no-scheme"},
	}
	for _, tc := range cases {
		if got := ExtractTrackerHost(tc.raw); got != tc.want {
			t.Fatalf("ExtractTrackerHost(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
