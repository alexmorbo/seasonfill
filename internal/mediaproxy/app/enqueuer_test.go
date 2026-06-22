package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestEnqueuer_Dedup(t *testing.T) {
	t.Parallel()
	eq := NewEnqueuer(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	defer eq.Close()
	url := "https://image.tmdb.org/t/p/w342/abc.jpg"
	eq.Enqueue(context.Background(), []EnqueueRequest{
		{UpstreamURL: url, Kind: "poster_w342", Extension: "jpg"},
		{UpstreamURL: url, Kind: "poster_w342", Extension: "jpg"},
	})
	got := drain(eq.Channel())
	if len(got) != 1 {
		t.Fatalf("dedup: want 1 job got %d", len(got))
	}
	if got[0].Hash != HashFromURL(url) {
		t.Fatalf("hash mismatch")
	}
}

func TestEnqueuer_QueueFull(t *testing.T) {
	t.Parallel()
	eq := NewEnqueuer(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	defer eq.Close()
	// Stuff > channelCap unique URLs without a consumer running.
	for i := range channelCap + 50 {
		eq.Enqueue(context.Background(), []EnqueueRequest{
			{UpstreamURL: "https://image.tmdb.org/t/p/w342/img" + strconv.Itoa(i) + ".jpg", Kind: "poster_w342", Extension: "jpg"},
		})
	}
	if len(eq.Channel()) != channelCap {
		t.Fatalf("channel: want %d got %d", channelCap, len(eq.Channel()))
	}
}

// B-28 — warnRate emits the first drop immediately so the operator
// sees overflow surface within seconds of cold-start.
func TestEnqueuer_QueueFull_FirstWarnImmediate(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	eq := newEnqueuerForTest(logger, 30*time.Second, clock.Now)
	defer eq.Close()
	// Stuff exactly channelCap+1 unique URLs without a consumer —
	// the +1 hits the default-arm and triggers exactly ONE WARN.
	for i := range channelCap + 1 {
		eq.Enqueue(context.Background(), []EnqueueRequest{
			{UpstreamURL: "https://image.tmdb.org/t/p/w342/img" + strconv.Itoa(i) + ".jpg", Kind: "poster_w342", Extension: "jpg"},
		})
	}
	lines := countWarnLines(buf.String())
	if lines != 1 {
		t.Fatalf("leading-edge: want exactly 1 WARN, got %d (buf=%s)", lines, buf.String())
	}
	// dropped_in_window must be 1 на первой emit — никаких
	// suppressed entries ещё нет.
	if !strings.Contains(buf.String(), `"dropped_in_window":1`) {
		t.Fatalf("first WARN must have dropped_in_window=1, got: %s", buf.String())
	}
}

// B-28 — 47 drops внутри одного 30s окна emit exactly 1 WARN
// (the leading edge) and accumulate a count.
func TestEnqueuer_QueueFull_WindowSuppression(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	eq := newEnqueuerForTest(logger, 30*time.Second, clock.Now)
	defer eq.Close()
	// First fill exhausts the channel.
	for i := range channelCap {
		eq.Enqueue(context.Background(), []EnqueueRequest{
			{UpstreamURL: "https://image.tmdb.org/t/p/w342/img" + strconv.Itoa(i) + ".jpg", Kind: "poster_w342", Extension: "jpg"},
		})
	}
	// Next 47 drops — same wall clock, all inside the window.
	for i := range 47 {
		eq.Enqueue(context.Background(), []EnqueueRequest{
			{UpstreamURL: "https://image.tmdb.org/t/p/w342/extra" + strconv.Itoa(i) + ".jpg", Kind: "still_w300", Extension: "jpg"},
		})
	}
	lines := countWarnLines(buf.String())
	if lines != 1 {
		t.Fatalf("window-suppression: want exactly 1 WARN, got %d", lines)
	}
	// The single emitted WARN is the LEADING edge — dropped_in_window=1
	// (it reflects the trigger drop alone; the 46 subsequent
	// suppressed drops are pending для следующего window).
	if !strings.Contains(buf.String(), `"dropped_in_window":1`) {
		t.Fatalf("leading-edge WARN must have dropped_in_window=1, got: %s", buf.String())
	}
}

// B-28 — после window expiry the next drop emits an aggregate WARN
// with the suppressed count from the previous window.
func TestEnqueuer_QueueFull_WindowExpiry_EmitsAggregate(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	eq := newEnqueuerForTest(logger, 30*time.Second, clock.Now)
	defer eq.Close()
	// Fill channel.
	for i := range channelCap {
		eq.Enqueue(context.Background(), []EnqueueRequest{
			{UpstreamURL: "https://image.tmdb.org/t/p/w342/img" + strconv.Itoa(i) + ".jpg", Kind: "poster_w342", Extension: "jpg"},
		})
	}
	// First overflow drop — leading-edge WARN (count=1).
	eq.Enqueue(context.Background(), []EnqueueRequest{
		{UpstreamURL: "https://image.tmdb.org/t/p/w342/lead.jpg", Kind: "poster_w342", Extension: "jpg"},
	})
	// 9 drops inside window — suppressed (sample captured from first).
	for i := range 9 {
		eq.Enqueue(context.Background(), []EnqueueRequest{
			{UpstreamURL: "https://image.tmdb.org/t/p/w342/inwin" + strconv.Itoa(i) + ".jpg", Kind: "still_w300", Extension: "jpg"},
		})
	}
	// Advance past window — next drop must emit aggregate (9
	// suppressed + 1 trigger = 10 dropped_in_window).
	clock.now = clock.now.Add(31 * time.Second)
	eq.Enqueue(context.Background(), []EnqueueRequest{
		{UpstreamURL: "https://image.tmdb.org/t/p/w342/trigger.jpg", Kind: "backdrop_w1280", Extension: "jpg"},
	})
	lines := countWarnLines(buf.String())
	if lines != 2 {
		t.Fatalf("expiry-aggregate: want exactly 2 WARN (leading + aggregate), got %d (buf=%s)", lines, buf.String())
	}
	// Second WARN must report dropped_in_window=10 (9 suppressed +
	// trigger) and the kind_sample must be the FIRST suppressed
	// drop's kind ("still_w300"), not the trigger's "backdrop_w1280".
	if !strings.Contains(buf.String(), `"dropped_in_window":10`) {
		t.Fatalf("aggregate must report dropped_in_window=10, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"kind_sample":"still_w300"`) {
		t.Fatalf("aggregate must use first suppressed sample kind, got: %s", buf.String())
	}
}

// fakeClock is a deterministic time source for warnRate tests.
// Tests mutate .now directly — single goroutine, no lock needed.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

// countWarnLines counts WARN-level JSON log lines в buf. JSON
// handler emits one line per log call; level token is the literal
// "level":"WARN".
func countWarnLines(s string) int {
	return strings.Count(s, `"level":"WARN"`)
}

func TestBuildTMDBImageURL(t *testing.T) {
	t.Parallel()
	got := BuildTMDBImageURL("w342", "/abc.jpg")
	want := "https://image.tmdb.org/t/p/w342/abc.jpg"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if BuildTMDBImageURL("w342", "") != "" {
		t.Fatal("empty path must yield empty url")
	}
	// Path without leading slash gets one inserted.
	got2 := BuildTMDBImageURL("w780", "noslash.jpg")
	want2 := "https://image.tmdb.org/t/p/w780/noslash.jpg"
	if got2 != want2 {
		t.Fatalf("got %q want %q", got2, want2)
	}
}

func TestExtractExt(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"/abc.jpg":  "jpg",
		"/abc.png":  "png",
		"/abc.JPEG": "jpeg",
		"/abc":      "",
		"abc.":      "",
	} {
		if got := ExtractExt(in); got != want {
			t.Fatalf("ExtractExt(%q): got %q want %q", in, got, want)
		}
	}
}

// Story 347 — sentinel-missing hash invariants.

func TestSentinelMissingHash_Deterministic(t *testing.T) {
	t.Parallel()
	sum := sha256.Sum256([]byte("seasonfill:media:sentinel:missing:v1"))
	want := hex.EncodeToString(sum[:])
	if SentinelMissingHash != want {
		t.Fatalf("SentinelMissingHash: got %q want %q", SentinelMissingHash, want)
	}
	// 64 lowercase hex chars — must satisfy isValidHashHex on the
	// handler side without special casing.
	if len(SentinelMissingHash) != 64 {
		t.Fatalf("SentinelMissingHash len: got %d want 64", len(SentinelMissingHash))
	}
}

func TestSentinelMissingHash_NoCollisionWithRealURL(t *testing.T) {
	t.Parallel()
	urls := []string{
		"https://image.tmdb.org/t/p/w342/abc.jpg",
		"https://image.tmdb.org/t/p/w1280/abc.jpg",
		"https://image.tmdb.org/t/p/w185/x.png",
		"https://image.tmdb.org/t/p/w154/y.jpg",
		"https://image.tmdb.org/t/p/original/z.jpg",
		BuildTMDBImageURL("w342", "/seasonfill:media:sentinel:missing:v1"),
	}
	for _, u := range urls {
		if HashFromURL(u) == SentinelMissingHash {
			t.Fatalf("collision: HashFromURL(%q) == SentinelMissingHash", u)
		}
	}
}

func drain(ch <-chan job) []job {
	var out []job
	for {
		select {
		case j := <-ch:
			out = append(out, j)
		default:
			return out
		}
	}
}
