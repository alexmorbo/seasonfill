package rest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/torrentsync"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/qbit"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// stubMovieMembership satisfies MovieMembershipReader (canon → active
// per-instance Radarr states).
type stubMovieMembership struct {
	entries []movie.StateEntry
	err     error
}

func (s stubMovieMembership) ListActiveByMovieID(_ context.Context, _ domain.MovieID) ([]movie.StateEntry, error) {
	return s.entries, s.err
}

// stubMovieState satisfies MovieStateReader ((instance, radarr id) → canon).
type stubMovieState struct {
	entry movie.StateEntry
	err   error
}

func (s stubMovieState) Get(_ context.Context, _ domain.InstanceName, _ int) (movie.StateEntry, error) {
	return s.entry, s.err
}

// stubMovieLookup satisfies torrentsync.MovieLookupRepo — the bridge rows
// that carry provenance.
type stubMovieLookup struct {
	entries []torrentsync.MovieMapEntry
	err     error
}

func (s stubMovieLookup) HashesForMovie(_ context.Context, _ domain.InstanceName, _ domain.RadarrMovieID) ([]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]string, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e.Hash)
	}
	return out, nil
}

func (s stubMovieLookup) EntriesForMovie(_ context.Context, _ domain.InstanceName, _ domain.RadarrMovieID) ([]torrentsync.MovieMapEntry, error) {
	return s.entries, s.err
}

var movieTorrentsClock = time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)

// buildMovieTorrentsRouter wires the real Query over an in-memory Store so
// the merge + projection are exercised end to end. repo is nil on purpose:
// every bridge hash is also live in the store, so the DB-fallback branch is
// never reached and no TorrentsRepo is needed.
func buildMovieTorrentsRouter(
	t *testing.T,
	canon stubCanon,
	membership stubMovieMembership,
	state stubMovieState,
	lookup torrentsync.MovieLookupRepo,
	liveHashes []string,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store := torrentsync.NewStore()
	store.EnsureInstance("radarr-main")
	for i, h := range liveHashes {
		store.Put("radarr-main", torrentsync.Entry{
			Info: qbit.TorrentInfo{
				Hash:        h,
				Name:        "Some.Movie.2026.2160p",
				StateRaw:    "uploading",
				StateGroup:  qbit.StateGroupSeeding,
				Size:        1 << 30,
				Category:    "radarr",
				TrackerHost: "tracker.example.com",
				AddedOn:     movieTorrentsClock.Add(-time.Duration(i) * time.Hour),
			},
			StateGroup: qbit.StateGroupSeeding,
			SyncedAt:   movieTorrentsClock,
		})
		store.SetMovieMapping("radarr-main", h, 77)
	}

	query := torrentsync.NewQuery(store, nil, nil).
		WithMovieLookup(lookup).
		WithClock(func() time.Time { return movieTorrentsClock })

	inner := NewMovieTorrentsHandler(query, state, nil)
	h := NewGlobalMovieTorrentsHandler(inner, canon, membership, nil)

	r := gin.New()
	r.GET("/movies/:tmdb_id/torrents", h.Get)
	return r
}

func TestMovieTorrentsHandler(t *testing.T) {
	tid := domain.TMDBID(550)
	okCanon := stubCanon{canon: movie.Canon{ID: 7, TMDBID: &tid, Title: "Fight Club"}}
	okMembership := stubMovieMembership{entries: []movie.StateEntry{
		{InstanceName: "radarr-zeta", RadarrMovieID: 99, MovieID: 7},
		{InstanceName: "radarr-main", RadarrMovieID: 77, MovieID: 7},
	}}
	okState := stubMovieState{entry: movie.StateEntry{
		InstanceName: "radarr-main", RadarrMovieID: 77, MovieID: 7,
	}}
	okLookup := stubMovieLookup{entries: []torrentsync.MovieMapEntry{
		{
			Hash:       "aaaa",
			Source:     torrentsync.MovieMapSourceWebhook,
			Provenance: torrentsync.MovieProvenanceManualImport,
		},
	}}

	tests := []struct {
		name       string
		url        string
		canon      stubCanon
		membership stubMovieMembership
		state      stubMovieState
		lookup     torrentsync.MovieLookupRepo
		liveHashes []string
		// preflight issues an identical request first and replays its
		// ETag as If-None-Match on the asserted request.
		preflight  bool
		wantStatus int
	}{
		{
			name:       "200 merged inventory",
			url:        "/movies/550/torrents",
			canon:      okCanon,
			membership: okMembership,
			state:      okState,
			lookup:     okLookup,
			liveHashes: []string{"aaaa"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "304 when If-None-Match matches the current ETag",
			url:        "/movies/550/torrents",
			canon:      okCanon,
			membership: okMembership,
			state:      okState,
			lookup:     okLookup,
			liveHashes: []string{"aaaa"},
			preflight:  true,
			wantStatus: http.StatusNotModified,
		},
		{
			name:       "404 unknown tmdb id",
			url:        "/movies/12345/torrents",
			canon:      stubCanon{err: ports.ErrNotFound},
			membership: okMembership,
			state:      okState,
			lookup:     okLookup,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "404 movie in zero radarr libraries",
			url:        "/movies/550/torrents",
			canon:      okCanon,
			membership: stubMovieMembership{},
			state:      okState,
			lookup:     okLookup,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "400 non-numeric tmdb id",
			url:        "/movies/not-a-number/torrents",
			canon:      okCanon,
			membership: okMembership,
			state:      okState,
			lookup:     okLookup,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := buildMovieTorrentsRouter(t, tc.canon, tc.membership, tc.state, tc.lookup, tc.liveHashes)

			var ifNoneMatch string
			if tc.preflight {
				pre := httptest.NewRecorder()
				r.ServeHTTP(pre, httptest.NewRequestWithContext(
					context.Background(), http.MethodGet, tc.url, nil))
				require.Equal(t, http.StatusOK, pre.Code)
				ifNoneMatch = pre.Header().Get("ETag")
				require.NotEmpty(t, ifNoneMatch)
			}

			w := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, tc.url, nil)
			if ifNoneMatch != "" {
				req.Header.Set("If-None-Match", ifNoneMatch)
			}
			r.ServeHTTP(w, req)
			require.Equal(t, tc.wantStatus, w.Code)
		})
	}
}

// TestMovieTorrentsHandler_Shape asserts the 200 body: the lex-first Radarr
// instance is reported, provenance is surfaced per row, and season_number is
// never emitted (movies have no seasons).
func TestMovieTorrentsHandler_Shape(t *testing.T) {
	tid := domain.TMDBID(550)
	r := buildMovieTorrentsRouter(t,
		stubCanon{canon: movie.Canon{ID: 7, TMDBID: &tid, Title: "Fight Club"}},
		stubMovieMembership{entries: []movie.StateEntry{
			{InstanceName: "radarr-zeta", RadarrMovieID: 99, MovieID: 7},
			{InstanceName: "radarr-main", RadarrMovieID: 77, MovieID: 7},
		}},
		stubMovieState{entry: movie.StateEntry{
			InstanceName: "radarr-main", RadarrMovieID: 77, MovieID: 7,
		}},
		stubMovieLookup{entries: []torrentsync.MovieMapEntry{
			{
				Hash:       "aaaa",
				Source:     torrentsync.MovieMapSourceWebhook,
				Provenance: torrentsync.MovieProvenanceManualImport,
			},
		}},
		[]string{"aaaa"},
	)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(
		context.Background(), http.MethodGet, "/movies/550/torrents", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, w.Header().Get("ETag"))
	assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))

	raw, err := io.ReadAll(w.Body)
	require.NoError(t, err)

	var body dto.MovieTorrentsResponse
	require.NoError(t, json.Unmarshal(raw, &body))

	// Lex-first ACTIVE instance wins ("radarr-main" < "radarr-zeta"), and
	// its INSTANCE-LOCAL radarr_movie_id is what got threaded through.
	assert.Equal(t, domain.InstanceName("radarr-main"), body.Instance)
	assert.Equal(t, 77, body.RadarrMovieID)
	assert.Equal(t, domain.MovieID(7), body.MovieID)
	assert.Equal(t, 550, body.TMDBID)
	assert.Equal(t, 1, body.TotalCount)
	assert.Equal(t, 1, body.LiveCount)
	assert.Equal(t, 0, body.SyncAgeSeconds)
	assert.True(t, body.SyncedAt.Equal(movieTorrentsClock))

	require.Len(t, body.Torrents, 1)
	row := body.Torrents[0]
	assert.Equal(t, domain.QbitHash("aaaa"), row.Hash)
	assert.True(t, row.Live)
	assert.True(t, row.Present)
	require.NotNil(t, row.Provenance)
	assert.Equal(t, string(torrentsync.MovieProvenanceManualImport), *row.Provenance)
	assert.Nil(t, row.SeasonNumber)

	// Wire-level: `season_number` must not appear at all — a decoded nil
	// pointer cannot distinguish "absent" from "null".
	assert.NotContains(t, string(raw), "season_number")
	assert.Contains(t, string(raw), `"provenance":"manual_import"`)
}
