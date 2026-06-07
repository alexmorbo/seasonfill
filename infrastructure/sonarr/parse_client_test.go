package sonarr_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
)

func TestClient_ParseRelease_HappyPath(t *testing.T) {
	const fixture = `{"title":"any","parsedEpisodeInfo":{"releaseTitle":"Foundation.S02.2160p.WEB-DL.HEVC","seasonNumber":2,"releaseGroup":"NTb","quality":{"quality":{"id":19,"name":"WEBDL-2160p","source":"webdl","resolution":2160}},"languages":[{"id":26,"name":"Russian"},{"id":1,"name":"English"}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/parse" || r.URL.Query().Get("title") == "" || r.Header.Get("X-Api-Key") != "k" {
			t.Fatalf("unexpected request: path=%s key=%s title=%s", r.URL.Path, r.Header.Get("X-Api-Key"), r.URL.Query().Get("title"))
		}
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()

	c := sonarr.New("test", srv.URL, "k", time.Second, nil)
	got, err := c.ParseRelease(context.Background(), "Foundation.S02.2160p.WEB-DL.HEVC")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	want := sonarr.ParseResult{
		Quality: "WEBDL-2160p", Source: "webdl", Resolution: 2160,
		Languages: []string{"Russian", "English"}, ReleaseGroup: "NTb",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}

func TestClient_ParseRelease_EmptyTitle(t *testing.T) {
	c := sonarr.New("test", "http://unreachable", "k", time.Second, nil)
	got, err := c.ParseRelease(context.Background(), "   ")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got.Quality != "" || len(got.Languages) != 0 {
		t.Fatalf("non-empty result for empty title: %+v", got)
	}
}

func TestClient_ParseRelease_UnrecognisedTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "bogus", "parsedEpisodeInfo": nil})
	}))
	defer srv.Close()
	c := sonarr.New("test", srv.URL, "k", time.Second, nil)
	got, err := c.ParseRelease(context.Background(), "bogus")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got.Quality != "" || got.Source != "" || got.Resolution != 0 {
		t.Fatalf("expected zero ParseResult, got %+v", got)
	}
}

func TestClient_ParseRelease_5xxPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := sonarr.New("test", srv.URL, "k", time.Second, nil)
	if _, err := c.ParseRelease(context.Background(), "any"); err == nil {
		t.Fatal("expected error on 503")
	}
}
