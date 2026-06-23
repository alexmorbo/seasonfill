package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	p := filepath.Join("testdata", name)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

func TestClient_Trending_HappyPath(t *testing.T) {
	body := loadFixture(t, "trending_tv_day_page1.json")
	var seenPath, seenLang, seenPage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenLang = r.URL.Query().Get("language")
		seenPage = r.URL.Query().Get("page")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	resp, err := c.Trending(context.Background(), TrendingDay, "en-US", 1)
	if err != nil {
		t.Fatalf("Trending: %v", err)
	}
	if seenPath != "/trending/tv/day" {
		t.Fatalf("path = %q want /trending/tv/day", seenPath)
	}
	if seenLang != "en-US" {
		t.Fatalf("language = %q", seenLang)
	}
	if seenPage != "1" {
		t.Fatalf("page = %q", seenPage)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results = %d want 2", len(resp.Results))
	}
	if resp.Results[0].ID != 1396 || resp.Results[0].Name != "Breaking Bad" {
		t.Fatalf("results[0] = %+v", resp.Results[0])
	}
	if resp.Results[0].VoteAverage <= 0 || len(resp.Results[0].OriginCountry) != 1 {
		t.Fatalf("results[0] missing parsed fields: %+v", resp.Results[0])
	}
}

func TestClient_Trending_NilLanguage_DefaultsToEnUS(t *testing.T) {
	body := loadFixture(t, "trending_tv_day_page1.json")
	var seenLang string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenLang = r.URL.Query().Get("language")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	if _, err := c.Trending(context.Background(), TrendingWeek, "", 1); err != nil {
		t.Fatalf("Trending: %v", err)
	}
	if seenLang != DefaultLanguage {
		t.Fatalf("empty lang must default to %q, got %q", DefaultLanguage, seenLang)
	}
}

func TestClient_Trending_InvalidScope(t *testing.T) {
	c := mustNew(t, "http://example.invalid", "tk")
	defer c.Close()
	if _, err := c.Trending(context.Background(), TrendingScope("nope"), "en-US", 1); err == nil {
		t.Fatal("expected invalid scope error")
	}
}

func TestClient_Popular_HappyPath(t *testing.T) {
	body := loadFixture(t, "popular_tv_page1.json")
	var seenPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	resp, err := c.Popular(context.Background(), "en-US", 1)
	if err != nil {
		t.Fatalf("Popular: %v", err)
	}
	if seenPath != "/tv/popular" {
		t.Fatalf("path = %q", seenPath)
	}
	if len(resp.Results) != 1 || resp.Results[0].ID != 60625 {
		t.Fatalf("results = %+v", resp.Results)
	}
}

func TestClient_DiscoverTV_HappyPath(t *testing.T) {
	body := loadFixture(t, "discover_tv_basic.json")
	var seenQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.Query()
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	gte := "2016-01-01"
	voteGte := 7.5
	filter := DiscoverFilter{
		WithGenres:      []int{18, 35},
		FirstAirDateGte: &gte,
		VoteAverageGte:  &voteGte,
		SortBy:          "popularity.desc",
	}
	if _, err := c.DiscoverTV(context.Background(), filter, 1); err != nil {
		t.Fatalf("DiscoverTV: %v", err)
	}
	want := map[string]string{
		"with_genres":        "18,35",
		"first_air_date.gte": "2016-01-01",
		"vote_average.gte":   "7.5",
		"sort_by":            "popularity.desc",
		"language":           "en-US",
		"page":               "1",
		"include_adult":      "false",
	}
	for k, v := range want {
		if got := seenQuery.Get(k); got != v {
			t.Errorf("query[%s] = %q want %q", k, got, v)
		}
	}
}

func TestClient_DiscoverTV_StatusOpOr(t *testing.T) {
	body := loadFixture(t, "discover_tv_basic.json")
	var seenRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRawQuery = r.URL.RawQuery
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	filter := DiscoverFilter{
		WithStatus:   []int{0, 3},
		WithStatusOp: "or",
	}
	if _, err := c.DiscoverTV(context.Background(), filter, 1); err != nil {
		t.Fatalf("DiscoverTV: %v", err)
	}
	// %7C is the URL-encoded pipe.
	if !contains(seenRawQuery, "with_status=0%7C3") {
		t.Fatalf("expected with_status=0%%7C3 in raw query, got %q", seenRawQuery)
	}
}

func TestClient_DiscoverTV_StatusOpAnd(t *testing.T) {
	body := loadFixture(t, "discover_tv_basic.json")
	var seenRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRawQuery = r.URL.RawQuery
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	filter := DiscoverFilter{
		WithStatus:   []int{0, 3},
		WithStatusOp: "and",
	}
	if _, err := c.DiscoverTV(context.Background(), filter, 1); err != nil {
		t.Fatalf("DiscoverTV: %v", err)
	}
	// %2C is the URL-encoded comma.
	if !contains(seenRawQuery, "with_status=0%2C3") {
		t.Fatalf("expected with_status=0%%2C3 in raw query, got %q", seenRawQuery)
	}
}

func TestClient_DiscoverTV_TypeOpOr(t *testing.T) {
	body := loadFixture(t, "discover_tv_basic.json")
	var seenRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRawQuery = r.URL.RawQuery
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	filter := DiscoverFilter{
		WithType:   []int{0, 4},
		WithTypeOp: "or",
	}
	if _, err := c.DiscoverTV(context.Background(), filter, 1); err != nil {
		t.Fatalf("DiscoverTV: %v", err)
	}
	if !contains(seenRawQuery, "with_type=0%7C4") {
		t.Fatalf("expected with_type=0%%7C4, got %q", seenRawQuery)
	}
}

func TestClient_DiscoverTV_TypeOpAnd(t *testing.T) {
	body := loadFixture(t, "discover_tv_basic.json")
	var seenRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRawQuery = r.URL.RawQuery
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	filter := DiscoverFilter{
		WithType:   []int{0, 4},
		WithTypeOp: "and",
	}
	if _, err := c.DiscoverTV(context.Background(), filter, 1); err != nil {
		t.Fatalf("DiscoverTV: %v", err)
	}
	if !contains(seenRawQuery, "with_type=0%2C4") {
		t.Fatalf("expected with_type=0%%2C4, got %q", seenRawQuery)
	}
}

func TestClient_DiscoverTV_HardcodedIncludeAdultFalse(t *testing.T) {
	body := loadFixture(t, "discover_tv_basic.json")
	var seenAdult string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAdult = r.URL.Query().Get("include_adult")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	if _, err := c.DiscoverTV(context.Background(), DiscoverFilter{}, 1); err != nil {
		t.Fatalf("DiscoverTV: %v", err)
	}
	if seenAdult != "false" {
		t.Fatalf("include_adult must be hardcoded false, got %q", seenAdult)
	}
}

func TestClient_SearchTV_HappyPath(t *testing.T) {
	body := loadFixture(t, "search_tv_breaking_bad.json")
	var seenQuery, seenLang, seenAdult string
	var seenPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenQuery = r.URL.Query().Get("query")
		seenLang = r.URL.Query().Get("language")
		seenAdult = r.URL.Query().Get("include_adult")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	resp, err := c.SearchTV(context.Background(), "Breaking Bad", "en-US", 1)
	if err != nil {
		t.Fatalf("SearchTV: %v", err)
	}
	if seenPath != "/search/tv" {
		t.Fatalf("path = %q", seenPath)
	}
	if seenQuery != "Breaking Bad" {
		t.Fatalf("query = %q", seenQuery)
	}
	if seenLang != "en-US" {
		t.Fatalf("language = %q", seenLang)
	}
	if seenAdult != "false" {
		t.Fatalf("include_adult = %q want false", seenAdult)
	}
	if len(resp.Results) != 1 || resp.Results[0].ID != 1396 {
		t.Fatalf("results = %+v", resp.Results)
	}
}

func TestClient_SearchTV_EmptyQuery(t *testing.T) {
	c := mustNew(t, "http://example.invalid", "tk")
	defer c.Close()
	if _, err := c.SearchTV(context.Background(), "  ", "en-US", 1); err == nil {
		t.Fatal("expected empty-query error")
	}
}

// 429 retry path — shared by all 4 methods via c.do; one test covers it.
func TestClient_Discover_429Retry(t *testing.T) {
	body := loadFixture(t, "discover_tv_basic.json")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	clk := newRecordingSleepClock()
	c := mustNewWithClock(t, srv.URL, "tk", clk)
	defer c.Close()

	resp, err := c.DiscoverTV(context.Background(), DiscoverFilter{}, 1)
	if err != nil {
		t.Fatalf("DiscoverTV: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("hits = %d want 2", hits.Load())
	}
	if len(resp.Results) == 0 {
		t.Fatalf("empty results after retry")
	}
	if got := clk.Last(); got != 1*time.Second {
		t.Fatalf("expected 1s Retry-After wait, got %v", got)
	}
}

// Gremlins ratchet for buildDiscoverQuery — covers every allow-list field
// AND the pointer-nil vs pointer-zero distinction (pointer to 0 must
// EMIT the param; nil pointer must omit it).
func TestBuildDiscoverQuery_AllowList(t *testing.T) {
	s := "x"
	f := 0.0
	i := 0
	cases := []struct {
		name   string
		filter DiscoverFilter
		want   map[string]string
		omit   []string
	}{
		{
			name:   "empty_filter_emits_only_required",
			filter: DiscoverFilter{},
			want: map[string]string{
				"language":      "en-US",
				"page":          "1",
				"include_adult": "false",
			},
			omit: []string{
				"with_genres", "without_genres", "first_air_date.gte",
				"first_air_date.lte", "vote_average.gte", "vote_average.lte",
				"vote_count.gte", "with_runtime.gte", "with_runtime.lte",
				"with_original_language", "with_networks", "with_origin_country",
				"with_keywords", "with_watch_providers", "watch_region",
				"with_status", "with_type", "sort_by",
			},
		},
		{
			name: "every_field_set",
			filter: DiscoverFilter{
				WithGenres:         []int{18, 35},
				WithoutGenres:      []int{10764},
				FirstAirDateGte:    &s,
				FirstAirDateLte:    &s,
				VoteAverageGte:     &f,
				VoteAverageLte:     &f,
				VoteCountGte:       &i,
				WithRuntimeGte:     &i,
				WithRuntimeLte:     &i,
				WithOriginalLang:   &s,
				WithNetworks:       []int{213},
				WithOriginCountry:  &s,
				WithKeywords:       []int{210024},
				WithWatchProviders: []int{8},
				WatchRegion:        &s,
				WithStatus:         []int{0, 3},
				WithStatusOp:       "and",
				WithType:           []int{0, 4},
				WithTypeOp:         "or",
				SortBy:             "popularity.desc",
			},
			want: map[string]string{
				"with_genres":            "18,35",
				"without_genres":         "10764",
				"first_air_date.gte":     "x",
				"first_air_date.lte":     "x",
				"vote_average.gte":       "0",
				"vote_average.lte":       "0",
				"vote_count.gte":         "0",
				"with_runtime.gte":       "0",
				"with_runtime.lte":       "0",
				"with_original_language": "x",
				"with_networks":          "213",
				"with_origin_country":    "x",
				"with_keywords":          "210024",
				"with_watch_providers":   "8",
				"watch_region":           "x",
				"with_status":            "0,3",
				"with_type":              "0|4",
				"sort_by":                "popularity.desc",
				"language":               "en-US",
				"page":                   "1",
				"include_adult":          "false",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := buildDiscoverQuery(tc.filter, "en-US", 1)
			for k, v := range tc.want {
				if got := q.Get(k); got != v {
					t.Errorf("q[%s] = %q want %q", k, got, v)
				}
			}
			for _, k := range tc.omit {
				if q.Has(k) {
					t.Errorf("q[%s] must be omitted, got %q", k, q.Get(k))
				}
			}
		})
	}
}

func TestPageOrOne(t *testing.T) {
	for _, tc := range []struct{ in, want int }{
		{0, 1}, {-1, 1}, {1, 1}, {2, 2}, {500, 500},
	} {
		if got := pageOrOne(tc.in); got != tc.want {
			t.Errorf("pageOrOne(%d) = %d want %d", tc.in, got, tc.want)
		}
	}
}

func TestOpSeparator(t *testing.T) {
	cases := map[string]string{
		"":     "|",
		"or":   "|",
		"OR":   "|",
		"and":  ",",
		"AND":  ",",
		"junk": "|",
	}
	for in, want := range cases {
		if got := opSeparator(in); got != want {
			t.Errorf("opSeparator(%q) = %q want %q", in, got, want)
		}
	}
}

func TestJoinInts(t *testing.T) {
	if got := joinInts(nil, ","); got != "" {
		t.Errorf("nil → %q", got)
	}
	if got := joinInts([]int{1}, ","); got != "1" {
		t.Errorf("single → %q", got)
	}
	if got := joinInts([]int{1, 2, 3}, "|"); got != "1|2|3" {
		t.Errorf("triple → %q", got)
	}
}

// contains is a tiny strings.Contains shim that keeps the import surface
// of discover_test.go small; the rest of the file already uses url.Values.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
