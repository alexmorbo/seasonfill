//go:build integration

// D-2 / N-2f (story 507 Commit B) — end-to-end coverage of the
// seven curated read endpoints against testcontainers Postgres
// with the full migration chain applied.
//
// Scope: seed series + discovery_lists directly (the worker isn't
// wired in the test); construct the handler with real repos + fake
// warming probe + fake refresh; hit each endpoint via httptest.
package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	discopersistence "github.com/alexmorbo/seasonfill/internal/discovery/persistence"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
)

type integWarming struct{}

func (integWarming) IsWarming() bool { return false }

type integRefresh struct{ called int }

func (r *integRefresh) RefreshNow(_ context.Context, _ disco.Kind, _, _ string) error {
	r.called++
	return nil
}

func TestD2_DiscoveryEndpoints(t *testing.T) {
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

			lang := "en-US"
			suffix := uuid.NewString()[:8]
			// Seed 2 series + 2 discovery_lists rows for trending_day.
			s1 := insertSeriesAndScanID(t, ctx, db, b.name, "d2e1-"+suffix)
			s2 := insertSeriesAndScanID(t, ctx, db, b.name, "d2e2-"+suffix)
			now := time.Now().UTC()
			for i, sid := range []int64{s1, s2} {
				_, err := db.ExecContext(ctx,
					`INSERT INTO discovery_lists (kind, param, language, series_id, position, refreshed_at)
					 VALUES ($1, '', $2, $3, $4, $5)`,
					"trending_day", lang, sid, i+1, now)
				require.NoError(t, err)
			}

			// Seed genres + a network so picker endpoints return rows.
			_, err = db.ExecContext(ctx,
				`INSERT INTO genres (tmdb_id, created_at, updated_at) VALUES ($1, now(), now())`,
				18)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx,
				`INSERT INTO genres_i18n (genre_id, language, name, updated_at)
				 VALUES ((SELECT id FROM genres WHERE tmdb_id=18), $1, $2, now())`,
				lang, "Drama-"+suffix)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx,
				`INSERT INTO networks (tmdb_id, name, created_at, updated_at)
				 VALUES ($1, $2, now(), now())`,
				213, "Netflix-"+suffix)
			require.NoError(t, err)

			repo := discopersistence.NewListRepository(gdb)
			genres := discopersistence.NewGenresPickerRepo(gdb)
			networks := discopersistence.NewNetworksPickerRepo(gdb)
			rf := &integRefresh{}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			h := discoveryrest.NewDiscoveryHandler(repo, integWarming{}, rf, genres, networks, nil, nil, log)

			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.GET("/discovery/trending", h.Trending)
			r.GET("/discovery/popular", h.Popular)
			r.GET("/discovery/genre/:id", h.ByGenre)
			r.GET("/discovery/network/:id", h.ByNetwork)
			r.GET("/discovery/keyword/:id", h.ByKeyword)
			r.GET("/discovery/genres", h.PickerGenres)
			r.GET("/discovery/networks", h.PickerNetworks)

			// 1. Trending day — 2 seeded rows.
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(ctx, "GET", "/discovery/trending?scope=day&lang="+lang, nil)
			r.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)
			var resp discoveryrest.DiscoveryListResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Len(t, resp.Items, 2)

			// 2. Popular — none seeded → empty 200.
			rec = httptest.NewRecorder()
			req = httptest.NewRequestWithContext(ctx, "GET", "/discovery/popular?lang="+lang, nil)
			r.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)

			// 3. Picker genres.
			rec = httptest.NewRecorder()
			req = httptest.NewRequestWithContext(ctx, "GET", "/discovery/genres?lang="+lang, nil)
			r.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Contains(t, rec.Body.String(), "Drama-"+suffix)

			// 4. Picker networks.
			rec = httptest.NewRecorder()
			req = httptest.NewRequestWithContext(ctx, "GET", "/discovery/networks?lang="+lang, nil)
			r.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Contains(t, rec.Body.String(), "Netflix-"+suffix)

			// 5. ByGenre — empty → triggers RefreshNow stub.
			rec = httptest.NewRecorder()
			req = httptest.NewRequestWithContext(ctx, "GET", "/discovery/genre/18?lang="+lang, nil)
			r.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, 1, rf.called)

			// 6. Invalid scope.
			rec = httptest.NewRecorder()
			req = httptest.NewRequestWithContext(ctx, "GET", "/discovery/trending?scope=foo", nil)
			r.ServeHTTP(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code)

			// 7. Invalid id on long-tail.
			rec = httptest.NewRecorder()
			req = httptest.NewRequestWithContext(ctx, "GET", "/discovery/network/abc", nil)
			r.ServeHTTP(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}
