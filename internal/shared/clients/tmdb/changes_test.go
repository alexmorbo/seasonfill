package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestClient_GetTVChangesPage_Pagination walks the 3-page firehose and asserts
// the IDs concatenate in order, Page tracks the request, TotalPages is stable,
// and the adult flag is dropped (page 3 mixes adult:true and an omitted-adult
// row — both must surface as bare ids). Fixture is the REAL /tv/changes shape
// (L-02): results[].{id,adult} + page/total_pages/total_results.
func TestClient_GetTVChangesPage_Pagination(t *testing.T) {
	pages := map[string]string{
		"1": `{"results":[{"id":70101,"adult":false},{"id":1399,"adult":false}],"page":1,"total_pages":3,"total_results":6}`,
		"2": `{"results":[{"id":66732,"adult":false},{"id":1396,"adult":false}],"page":2,"total_pages":3,"total_results":6}`,
		"3": `{"results":[{"id":60625,"adult":true},{"id":456}],"page":3,"total_pages":3,"total_results":6}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/changes" {
			t.Errorf("path = %q want /tv/changes", r.URL.Path)
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
	lastTotal := 0
	for page := 1; ; page++ {
		p, err := c.GetTVChangesPage(context.Background(), start, end, page)
		if err != nil {
			t.Fatalf("GetTVChangesPage(p%d): %v", page, err)
		}
		if p.Page != page {
			t.Fatalf("page = %d want %d", p.Page, page)
		}
		got = append(got, p.IDs...)
		lastTotal = p.TotalPages
		if page >= p.TotalPages {
			break
		}
	}
	if lastTotal != 3 {
		t.Fatalf("total_pages = %d want 3", lastTotal)
	}
	want := []int64{70101, 1399, 66732, 1396, 60625, 456}
	if len(got) != len(want) {
		t.Fatalf("ids = %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids[%d] = %d want %d (full %v)", i, got[i], want[i], got)
		}
	}
}

// TestClient_GetTVChangesPage_DateAndPageQuery asserts the query string carries
// start_date/end_date as YYYY-MM-DD (UTC calendar dates) and page as the int.
// end is passed in a non-UTC zone to prove the client normalises to UTC: EST
// 2026-07-16 23:30 = UTC 2026-07-17 04:30 → end_date 2026-07-17.
func TestClient_GetTVChangesPage_DateAndPageQuery(t *testing.T) {
	var seenPath, seenStart, seenEnd, seenPage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenStart = r.URL.Query().Get("start_date")
		seenEnd = r.URL.Query().Get("end_date")
		seenPage = r.URL.Query().Get("page")
		_, _ = w.Write([]byte(`{"results":[],"page":7,"total_pages":7,"total_results":0}`))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	est := time.FixedZone("EST", -5*3600)
	start := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 16, 23, 30, 0, 0, est)

	if _, err := c.GetTVChangesPage(context.Background(), start, end, 7); err != nil {
		t.Fatalf("GetTVChangesPage: %v", err)
	}
	if seenPath != "/tv/changes" {
		t.Fatalf("path = %q want /tv/changes", seenPath)
	}
	if seenStart != "2026-07-14" {
		t.Fatalf("start_date = %q want 2026-07-14", seenStart)
	}
	if seenEnd != "2026-07-17" {
		t.Fatalf("end_date = %q want 2026-07-17 (UTC of 2026-07-16 23:30 EST)", seenEnd)
	}
	if seenPage != "7" {
		t.Fatalf("page = %q want 7", seenPage)
	}
}

// TestClient_GetTVChangesPage_429Retry drives the shared do()/doDirect retry
// path: a 429 with Retry-After:1 followed by a 200. Mirrors
// TestClient_Discover_429Retry (discover_test.go:308) — the recording clock
// no-ops the backoff sleep but records its duration.
func TestClient_GetTVChangesPage_429Retry(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"id":70101,"adult":false}],"page":1,"total_pages":1,"total_results":1}`))
	}))
	t.Cleanup(srv.Close)
	clk := newRecordingSleepClock()
	c := mustNewWithClock(t, srv.URL, "tk", clk)
	defer c.Close()

	day := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	p, err := c.GetTVChangesPage(context.Background(), day, day, 1)
	if err != nil {
		t.Fatalf("GetTVChangesPage: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("hits = %d want 2 (429 then 200)", hits.Load())
	}
	if len(p.IDs) != 1 || p.IDs[0] != 70101 {
		t.Fatalf("ids = %v want [70101]", p.IDs)
	}
	if got := clk.Last(); got != 1*time.Second {
		t.Fatalf("Retry-After wait = %v want 1s", got)
	}
}

// TestClient_GetTVChangesPage_DecodeError asserts malformed JSON surfaces as a
// wrapped decode error, not a silent zero page.
func TestClient_GetTVChangesPage_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results": "not-an-array"`)) // truncated + wrong type
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	day := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	if _, err := c.GetTVChangesPage(context.Background(), day, day, 1); err == nil {
		t.Fatal("expected decode error on malformed JSON")
	}
}
