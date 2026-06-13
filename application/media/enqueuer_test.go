package media

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"testing"
)

func TestEnqueuer_Dedup(t *testing.T) {
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
	eq := NewEnqueuer(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	defer eq.Close()
	// Stuff > channelCap unique URLs without a consumer running.
	for i := 0; i < channelCap+50; i++ {
		eq.Enqueue(context.Background(), []EnqueueRequest{
			{UpstreamURL: "https://image.tmdb.org/t/p/w342/img" + strconv.Itoa(i) + ".jpg", Kind: "poster_w342", Extension: "jpg"},
		})
	}
	if len(eq.Channel()) != channelCap {
		t.Fatalf("channel: want %d got %d", channelCap, len(eq.Channel()))
	}
}

func TestBuildTMDBImageURL(t *testing.T) {
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
