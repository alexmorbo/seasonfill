//go:build integration

// D-2 / N-2g (story 508) — end-to-end coverage of the discovery search
// endpoint against testcontainers Postgres with the full migration
// chain applied.
//
// Scope: seed series + series_texts directly, hit GET
// /discovery/search via httptest. Covers the local-LIKE hit path and
// the TMDB fallback path (with a recording stubs/dispatch pair so the
// upsert + enqueue side effects are observable).
package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	"github.com/alexmorbo/seasonfill/internal/discovery/persistence"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type searchTestTMDB struct {
	resp  *tmdb.TVListResponse
	calls atomic.Int64
}

func (s *searchTestTMDB) SearchTV(_ context.Context, _, _ string, _ int) (*tmdb.TVListResponse, error) {
	s.calls.Add(1)
	return s.resp, nil
}

type searchTestStubs struct {
	db     *gorm.DB
	nextID atomic.Int64
	calls  atomic.Int64
}

func (s *searchTestStubs) EnsureStub(ctx context.Context, tmdbID shareddomain.TMDBID, title string, poster, _ *string) (shareddomain.SeriesID, error) {
	s.calls.Add(1)
	// Insert directly with raw SQL so we don't depend on GORM model
	// metadata (avoids the datatypes.JSON empty-default footgun).
	var id int64
	row := s.db.WithContext(ctx).Raw(
		`INSERT INTO series (tmdb_id, title, hydration, poster_asset, origin_countries, created_at, updated_at)
		 VALUES (?, ?, 'stub', ?, '[]', now(), now()) RETURNING id`,
		int64(tmdbID), title, poster).Row()
	if err := row.Scan(&id); err != nil {
		return 0, err
	}
	return shareddomain.SeriesID(id), nil
}

type searchTestDispatch struct {
	calls atomic.Int64
}

func (d *searchTestDispatch) Enqueue(_ string, _ int64, _ string) {
	d.calls.Add(1)
}

func mountSearchEndpoint(t *testing.T, uc *discoapp.SearchUseCase) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Curated-endpoint ports are nil-OK because the search route is
	// the only one mounted on this engine. We pass non-nil stub repos
	// so NewDiscoveryHandler does not panic.
	h := discoveryrest.NewDiscoveryHandler(
		searchStubListRepo{},
		searchStubWarming{},
		searchStubRefresh{},
		persistence.NewGenresPickerRepo(nil),
		persistence.NewNetworksPickerRepo(nil),
		uc, nil, log,
	)
	r := gin.New()
	r.GET("/discovery/search", h.Search)
	return r
}

// TestD2_SearchFallback_LocalHit_Postgres seeds 1 matching series and
// verifies LIKE search returns it with source="local".
func TestD2_SearchFallback_LocalHit_Postgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	for _, b := range allD1Backends(t) {
		if b.name != "postgres" {
			continue
		}
		t.Run(b.name, func(t *testing.T) {
			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
			require.NoError(t, err)

			suffix := uuid.NewString()[:8]
			_ = insertSeriesAndScanID(t, ctx, db, b.name, "Rick and Morty "+suffix)
			_ = insertSeriesAndScanID(t, ctx, db, b.name, "Breaking Bad "+suffix)

			repo := persistence.NewSearchRepository(gdb)
			tm := &searchTestTMDB{}
			uc := discoapp.NewSearchUseCase(repo, tm, &searchTestStubs{db: gdb}, &searchTestDispatch{},
				slog.New(slog.NewTextHandler(io.Discard, nil)))

			r := mountSearchEndpoint(t, uc)
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(ctx, "GET",
				"/discovery/search?q=Rick&lang=en-US", nil)
			r.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var resp struct {
				Items  []discoveryrest.DiscoverySeriesItem `json:"items"`
				Source string                              `json:"source"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Equal(t, "local", resp.Source)
			require.GreaterOrEqual(t, len(resp.Items), 1)
			require.Equal(t, int64(0), tm.calls.Load(), "TMDB must not be hit on local match")
		})
	}
}

// TestD2_SearchFallback_TMDBPath_StubsAndEnqueues uses a faked TMDB
// client that returns 2 rows, verifies they are stub-upserted into
// `series` and the dispatcher is enqueued once per stub.
func TestD2_SearchFallback_TMDBPath_StubsAndEnqueues(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	for _, b := range allD1Backends(t) {
		if b.name != "postgres" {
			continue
		}
		t.Run(b.name, func(t *testing.T) {
			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
			require.NoError(t, err)

			repo := persistence.NewSearchRepository(gdb)
			fakeTMDB := &searchTestTMDB{resp: &tmdb.TVListResponse{
				Results: []tmdb.TVListEntry{
					{ID: 9001, Name: "ZzzObscure One", PosterPath: "/p1.jpg", FirstAirDate: "2024-03-01"},
					{ID: 9002, Name: "ZzzObscure Two", PosterPath: "/p2.jpg"},
				},
			}}
			stubs := &searchTestStubs{db: gdb}
			disp := &searchTestDispatch{}
			uc := discoapp.NewSearchUseCase(repo, fakeTMDB, stubs, disp,
				slog.New(slog.NewTextHandler(io.Discard, nil)))

			r := mountSearchEndpoint(t, uc)
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(ctx, "GET",
				"/discovery/search?q=ZzzObscureNonsense&lang=en-US", nil)
			r.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var resp struct {
				Items  []discoveryrest.DiscoverySeriesItem `json:"items"`
				Source string                              `json:"source"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Equal(t, "tmdb", resp.Source)
			require.Len(t, resp.Items, 2)
			require.Equal(t, int64(2), stubs.calls.Load())
			require.Equal(t, int64(2), disp.calls.Load(),
				"every stub must be enqueued for hot enrichment")

			var seriesCount int64
			require.NoError(t, gdb.WithContext(ctx).Table("series").Count(&seriesCount).Error)
			require.GreaterOrEqual(t, seriesCount, int64(2))
		})
	}
}
