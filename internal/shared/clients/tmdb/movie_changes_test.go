package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestClient_GetMovieChangesPage_Pagination walks a 2-page movie firehose and
// asserts the IDs concatenate in order, Page tracks the request, TotalPages is
// stable, and adult is dropped. Path must be /movie/changes (NOT /tv/changes).
func TestClient_GetMovieChangesPage_Pagination(t *testing.T) {
	pages := map[string]string{
		"1": `{"results":[{"id":693134,"adult":false},{"id":438631,"adult":false}],"page":1,"total_pages":2,"total_results":3}`,
		"2": `{"results":[{"id":912649,"adult":true}],"page":2,"total_pages":2,"total_results":3}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movie/changes" {
			t.Errorf("path = %q want /movie/changes", r.URL.Path)
		}
		body, ok := pages[r.URL.Query().Get("page")]
		if !ok {
			http.Error(w, "bad page", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	start := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)

	var got []int64
	for page := 1; ; page++ {
		p, err := c.GetMovieChangesPage(context.Background(), start, end, page)
		if err != nil {
			t.Fatalf("GetMovieChangesPage(p%d): %v", page, err)
		}
		if p.Page != page {
			t.Fatalf("page = %d want %d", p.Page, page)
		}
		got = append(got, p.IDs...)
		if page >= p.TotalPages {
			break
		}
	}
	want := []int64{693134, 438631, 912649}
	if len(got) != len(want) {
		t.Fatalf("ids = %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids[%d] = %d want %d (full %v)", i, got[i], want[i], got)
		}
	}
}

// TestClient_GetMovieChangesPage_DateQuery asserts start_date/end_date are
// formatted as UTC calendar days (a non-UTC end normalises to the correct day).
func TestClient_GetMovieChangesPage_DateQuery(t *testing.T) {
	var gotStart, gotEnd string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotStart = r.URL.Query().Get("start_date")
		gotEnd = r.URL.Query().Get("end_date")
		_, _ = w.Write([]byte(`{"results":[],"page":1,"total_pages":1,"total_results":0}`))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	est := time.FixedZone("EST", -5*3600)
	start := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 16, 23, 30, 0, 0, est) // = 2026-07-17 04:30 UTC

	if _, err := c.GetMovieChangesPage(context.Background(), start, end, 1); err != nil {
		t.Fatalf("GetMovieChangesPage: %v", err)
	}
	if gotStart != "2026-07-14" {
		t.Fatalf("start_date = %q want 2026-07-14", gotStart)
	}
	if gotEnd != "2026-07-17" {
		t.Fatalf("end_date = %q want 2026-07-17 (UTC-normalised)", gotEnd)
	}
}
