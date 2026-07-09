package edge

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeSyncedAt is an injected stub for SectionSyncedAtReader so the ETag
// middleware tests need no DB. It records the args of the last call.
type fakeSyncedAt struct {
	stamp *time.Time
	err   error

	gotSeriesID domain.SeriesID
	gotSection  string
	gotSeason   int
}

func (f *fakeSyncedAt) SectionSyncedAt(_ context.Context, id domain.SeriesID, section string, season int) (*time.Time, error) {
	f.gotSeriesID = id
	f.gotSection = section
	f.gotSeason = season
	return f.stamp, f.err
}

// newETagEngine wires one route + the middleware + a sentinel handler that
// flips *called and writes a 200 body. route is the gin pattern (e.g.
// "/series/:id"); the sentinel body lets tests assert 304 carries no body.
func newETagEngine(t *testing.T, route string, reader SectionSyncedAtReader, called *bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET(route, ETagMiddleware(reader, nil), func(c *gin.Context) {
		*called = true
		c.String(http.StatusOK, "SENTINEL-BODY")
	})
	return r
}

func TestETagMiddleware_EmitsHeadersOnMiss(t *testing.T) {
	stamp := time.Unix(1_700_000_000, 0).UTC()
	reader := &fakeSyncedAt{stamp: &stamp}
	var called bool
	r := newETagEngine(t, "/series/:id", reader, &called)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/series/42?lang=ru", nil)
	r.ServeHTTP(w, req)

	require.True(t, called, "handler must run on cache miss")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "SENTINEL-BODY", w.Body.String())

	wantETag := fmt.Sprintf(`W/"42-%d-ru-skeleton"`, stamp.Unix())
	assert.Equal(t, wantETag, w.Header().Get("ETag"))
	assert.Equal(t, "private, max-age=60, stale-while-revalidate=600",
		w.Header().Get("Cache-Control"))
	assert.Empty(t, w.Header().Get("Vary"), "must NOT emit Vary (L-1)")

	// section + id + season threaded correctly.
	assert.Equal(t, domain.SeriesID(42), reader.gotSeriesID)
	assert.Equal(t, "skeleton", reader.gotSection)
	assert.Equal(t, 0, reader.gotSeason)
}

func TestETagMiddleware_304OnExactMatch(t *testing.T) {
	stamp := time.Unix(1_700_000_000, 0).UTC()
	reader := &fakeSyncedAt{stamp: &stamp}
	var called bool
	r := newETagEngine(t, "/series/:id", reader, &called)

	serverETag := fmt.Sprintf(`W/"7-%d-en-skeleton"`, stamp.Unix())
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/series/7?lang=en", nil)
	req.Header.Set("If-None-Match", serverETag)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotModified, w.Code)
	assert.Empty(t, w.Body.String(), "304 must carry no body")
	assert.False(t, called, "handler must NOT run on 304")
}

func TestETagMiddleware_MissOnNonMatchingINM(t *testing.T) {
	stamp := time.Unix(1_700_000_000, 0).UTC()
	reader := &fakeSyncedAt{stamp: &stamp}
	var called bool
	r := newETagEngine(t, "/series/:id", reader, &called)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/series/7?lang=en", nil)
	req.Header.Set("If-None-Match", `W/"stale-value"`)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called, "handler must run when INM does not match")
	assert.NotEmpty(t, w.Header().Get("ETag"))
}

func TestETagMiddleware_304OnCommaListMatch(t *testing.T) {
	stamp := time.Unix(1_700_000_000, 0).UTC()
	reader := &fakeSyncedAt{stamp: &stamp}
	var called bool
	r := newETagEngine(t, "/series/:id", reader, &called)

	serverETag := fmt.Sprintf(`W/"7-%d-en-skeleton"`, stamp.Unix())
	list := fmt.Sprintf(`W/"other-1" ,  %s , W/"other-2"`, serverETag)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/series/7?lang=en", nil)
	req.Header.Set("If-None-Match", list)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotModified, w.Code)
	assert.False(t, called)
}

func TestETagMiddleware_304OnWildcard(t *testing.T) {
	stamp := time.Unix(1_700_000_000, 0).UTC()
	reader := &fakeSyncedAt{stamp: &stamp}
	var called bool
	r := newETagEngine(t, "/series/:id", reader, &called)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/series/7?lang=en", nil)
	req.Header.Set("If-None-Match", "*")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotModified, w.Code)
	assert.False(t, called, "wildcard * must match current representation")
}

func TestETagMiddleware_FailOpenOnLookupError(t *testing.T) {
	reader := &fakeSyncedAt{err: fmt.Errorf("db down")}
	var called bool
	r := newETagEngine(t, "/series/:id", reader, &called)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/series/7?lang=en", nil)
	req.Header.Set("If-None-Match", "*") // would 304 if we did not fail open first
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "lookup error must never 500")
	assert.True(t, called, "handler must run on fail-open")
	assert.Empty(t, w.Header().Get("ETag"), "no ETag when lookup failed")
}

func TestETagMiddleware_FailOpenOnNilStamp(t *testing.T) {
	reader := &fakeSyncedAt{stamp: nil} // never synced -> NULL
	var called bool
	r := newETagEngine(t, "/series/:id", reader, &called)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/series/7?lang=en", nil)
	req.Header.Set("If-None-Match", "*")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called)
	assert.Empty(t, w.Header().Get("ETag"), "no ETag on NULL stamp")
}

func TestETagMiddleware_FailOpenOnZeroStamp(t *testing.T) {
	zero := time.Time{}
	reader := &fakeSyncedAt{stamp: &zero}
	var called bool
	r := newETagEngine(t, "/series/:id", reader, &called)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/series/7", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called)
	assert.Empty(t, w.Header().Get("ETag"), "no ETag on zero-time stamp")
}

func TestETagMiddleware_NilReaderPassThrough(t *testing.T) {
	var called bool
	r := newETagEngine(t, "/series/:id", nil, &called)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/series/7", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called)
	assert.Empty(t, w.Header().Get("ETag"))
}

func TestExtractSection(t *testing.T) {
	cases := []struct {
		name       string
		route      string
		url        string
		wantSect   string
		wantSeason int
		wantOK     bool
	}{
		{"skeleton", "/series/:id", "/series/1", "skeleton", 0, true},
		{"overview", "/series/:id/overview", "/series/1/overview", "overview", 0, true},
		{"cast", "/series/:id/cast", "/series/1/cast", "cast", 0, true},
		{"recs", "/series/:id/recommendations", "/series/1/recommendations", "recs", 0, true},
		{"season-n", "/series/:id/season/:n", "/series/1/season/3", "season", 3, true},
		{"season-episodes", "/series/:id/seasons/:season/episodes", "/series/1/seasons/5/episodes", "season", 5, true},
		{"unmatched", "/series/:id/torrents", "/series/1/torrents", "", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			var gotSect string
			var gotSeason int
			var gotOK bool
			r := gin.New()
			r.GET(tc.route, func(c *gin.Context) {
				gotSect, gotSeason, gotOK = extractSection(c)
				c.Status(http.StatusOK)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.url, nil))

			assert.Equal(t, tc.wantOK, gotOK)
			assert.Equal(t, tc.wantSect, gotSect)
			assert.Equal(t, tc.wantSeason, gotSeason)
		})
	}
}

func TestETagMiddleware_SeasonTag(t *testing.T) {
	stamp := time.Unix(1_700_000_000, 0).UTC()
	reader := &fakeSyncedAt{stamp: &stamp}
	var called bool
	r := newETagEngine(t, "/series/:id/season/:n", reader, &called)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/series/9/season/4?lang=ru", nil)
	r.ServeHTTP(w, req)

	require.True(t, called)
	wantETag := fmt.Sprintf(`W/"9-%d-ru-season:4"`, stamp.Unix())
	assert.Equal(t, wantETag, w.Header().Get("ETag"))
	assert.Equal(t, "season", reader.gotSection)
	assert.Equal(t, 4, reader.gotSeason)
}

func TestEtagMatches(t *testing.T) {
	const server = `W/"1-2-ru-cast"`
	cases := []struct {
		name   string
		header string
		want   bool
	}{
		{"empty", "", false},
		{"exact", server, true},
		{"wildcard", "*", true},
		{"list-with-match", `W/"x", ` + server + ` , W/"y"`, true},
		{"list-no-match", `W/"x", W/"y"`, false},
		{"whitespace", "   " + server + "   ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, etagMatches(tc.header, server))
		})
	}
}

func TestETagMiddleware_CastLimitChangesValidator(t *testing.T) {
	stamp := time.Unix(1_700_000_000, 0).UTC()
	reader := &fakeSyncedAt{stamp: &stamp}
	var called bool
	r := newETagEngine(t, "/series/:id/cast", reader, &called)

	etagFor := func(url string) string {
		w := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
		r.ServeHTTP(w, req)
		return w.Header().Get("ETag")
	}

	full := etagFor("/series/42/cast?lang=ru")
	lim8 := etagFor("/series/42/cast?lang=ru&limit=8")
	lim200 := etagFor("/series/42/cast?lang=ru&limit=200")
	zero := etagFor("/series/42/cast?lang=ru&limit=0")

	require.NotEmpty(t, full)
	assert.NotEqual(t, full, lim8, "?limit=8 MUST NOT share the full-page ETag (304 body mismatch)")
	assert.NotEqual(t, lim8, lim200, "distinct limits must yield distinct validators")
	assert.Equal(t, full, zero, "limit=0 is the full page — same validator as absent")
	// The full-page cast key keeps its historic shape (no -lim suffix).
	assert.Equal(t, fmt.Sprintf(`W/"42-%d-ru-cast"`, stamp.Unix()), full)
	assert.Equal(t, fmt.Sprintf(`W/"42-%d-ru-cast-lim8"`, stamp.Unix()), lim8)
}

// TestETagMiddleware_CastSortChangesValidator covers Story 1087b: the ?sort=
// param reorders the cast body, so non-default sorts must fold into the ETag
// key while the default (episodes / absent) keeps the 1087a un-suffixed shape.
func TestETagMiddleware_CastSortChangesValidator(t *testing.T) {
	stamp := time.Unix(1_700_000_000, 0).UTC()
	reader := &fakeSyncedAt{stamp: &stamp}
	var called bool
	r := newETagEngine(t, "/series/:id/cast", reader, &called)

	etagFor := func(url string) string {
		w := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
		r.ServeHTTP(w, req)
		return w.Header().Get("ETag")
	}

	def := etagFor("/series/42/cast?lang=ru")               // default (episodes)
	eps := etagFor("/series/42/cast?lang=ru&sort=episodes") // explicit default
	credit := etagFor("/series/42/cast?lang=ru&sort=credit")
	name := etagFor("/series/42/cast?lang=ru&sort=name")
	last := etagFor("/series/42/cast?lang=ru&sort=last_appearance")

	assert.Equal(t, def, eps, "explicit episodes == default (un-suffixed)")
	assert.Equal(t, fmt.Sprintf(`W/"42-%d-ru-cast"`, stamp.Unix()), def,
		"default cast key keeps the 1087a shape (no -srt suffix)")
	assert.NotEqual(t, def, credit, "credit sort must not share the default ETag")
	assert.NotEqual(t, def, name, "name sort must not share the default ETag")
	assert.NotEqual(t, def, last, "last_appearance sort must not share the default ETag")
	assert.NotEqual(t, credit, name, "distinct sorts → distinct validators")
	assert.NotEqual(t, credit, last, "distinct sorts → distinct validators")
	assert.NotEqual(t, name, last, "distinct sorts → distinct validators")
	assert.Equal(t, fmt.Sprintf(`W/"42-%d-ru-cast-srtcredit"`, stamp.Unix()), credit)
	assert.Equal(t, fmt.Sprintf(`W/"42-%d-ru-cast-srtname"`, stamp.Unix()), name)
	// F-06: the last_appearance sort (edge builder etag.go) must fold the
	// -srtlast suffix. The sort→suffix parse is duplicated in the seriesdetail
	// rest package (can't import edge), so pin the suffix here to catch drift.
	assert.Equal(t, fmt.Sprintf(`W/"42-%d-ru-cast-srtlast"`, stamp.Unix()), last)
}
