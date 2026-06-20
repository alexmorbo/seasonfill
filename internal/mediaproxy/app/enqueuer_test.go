package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"strconv"
	"testing"
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
