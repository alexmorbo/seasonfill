package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/health"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// fakeHealthRepo satisfies ports.HealthRepository so handler tests drive
// the REAL usecase (exercising cutoff computation + deferred signal +
// DTO mapping) without a DB.
type fakeHealthRepo struct {
	tvdb   []ports.HealthSeriesItem
	poster []ports.HealthSeriesItem
	stale  []ports.HealthStaleItem
	grabs  []ports.HealthGrabItem
	dead   []ports.HealthInboxItem
	err    error
}

func (f *fakeHealthRepo) MissingTVDBID(context.Context, int) (int, []ports.HealthSeriesItem, error) {
	if f.err != nil {
		return 0, nil, f.err
	}
	return len(f.tvdb), f.tvdb, nil
}
func (f *fakeHealthRepo) MissingPoster(context.Context, int) (int, []ports.HealthSeriesItem, error) {
	return len(f.poster), f.poster, nil
}
func (f *fakeHealthRepo) StaleEnrichment(context.Context, ports.StaleCutoffs, int) (int, []ports.HealthStaleItem, error) {
	return len(f.stale), f.stale, nil
}
func (f *fakeHealthRepo) StuckGrabs(context.Context, time.Time, int) (int, []ports.HealthGrabItem, error) {
	return len(f.grabs), f.grabs, nil
}
func (f *fakeHealthRepo) DeadLetters(context.Context, int) (int, []ports.HealthInboxItem, error) {
	return len(f.dead), f.dead, nil
}

func newHealthHandler(repo ports.HealthRepository) *HealthHandler {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	uc := health.NewUseCase(repo).WithClock(func() time.Time { return now })
	return NewHealthHandler(uc, nil)
}

func TestHealthHandler_Get_OK(t *testing.T) {
	t.Parallel()
	synced := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	repo := &fakeHealthRepo{
		tvdb:   []ports.HealthSeriesItem{{SeriesID: 1, Title: "A"}},
		poster: []ports.HealthSeriesItem{{SeriesID: 2, Title: "B"}, {SeriesID: 3, Title: "C"}},
		stale:  []ports.HealthStaleItem{{SeriesID: 4, Title: "D", Tier: "hot", SyncedAt: &synced}},
		grabs:  []ports.HealthGrabItem{{ID: "g1", InstanceName: "main", SeriesTitle: "Hijack", SeasonNumber: 2}},
		dead:   nil,
	}
	h := newHealthHandler(repo)

	r := gin.New()
	r.GET("/api/v1/insights/health", h.Get)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/insights/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.HealthDashboardDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	assert.Equal(t, 1, body.MissingTVDBID.Count)
	assert.Equal(t, domain.SeriesID(1), body.MissingTVDBID.Items[0].SeriesID)
	assert.Equal(t, 2, body.MissingPoster.Count)
	assert.Equal(t, 1, body.StaleEnrichment.Count)
	assert.Equal(t, "hot", body.StaleEnrichment.Items[0].Tier)
	assert.Equal(t, 1, body.StuckGrabs.Count)
	assert.Contains(t, body.StuckGrabs.Note, "seasonfill_webhook_orphan_total")
	assert.Equal(t, 0, body.DeadLetters.Count)

	// Deferred signal envelope.
	assert.True(t, body.RateLimitPressure.Deferred)
	assert.Equal(t, "seasonfill_sonarr_rate_oversubscribed", body.RateLimitPressure.Metric)
	assert.NotEmpty(t, body.RateLimitPressure.Reason)

	assert.False(t, body.GeneratedAt.IsZero())
}

// TestHealthHandler_Get_EmptyState — all signals zero; every items array
// serializes as [] (non-null) so the FE renders empty lists.
func TestHealthHandler_Get_EmptyState(t *testing.T) {
	t.Parallel()
	h := newHealthHandler(&fakeHealthRepo{})

	r := gin.New()
	r.GET("/api/v1/insights/health", h.Get)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/insights/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// Non-null arrays in the raw JSON.
	raw := w.Body.String()
	assert.Contains(t, raw, `"items":[]`)
	assert.NotContains(t, raw, `"items":null`)

	var body dto.HealthDashboardDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Zero(t, body.MissingTVDBID.Count)
	assert.NotNil(t, body.MissingPoster.Items)
	assert.Empty(t, body.DeadLetters.Items)
}

func TestHealthHandler_Get_RepoError_500(t *testing.T) {
	t.Parallel()
	h := newHealthHandler(&fakeHealthRepo{err: errors.New("db down")})

	r := gin.New()
	r.GET("/api/v1/insights/health", h.Get)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/insights/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusInternalServerError, w.Code)

	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "health unavailable", body.Error)
}
