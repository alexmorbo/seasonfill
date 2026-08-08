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

	"github.com/alexmorbo/seasonfill/internal/catalog/app/gaps"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// fakeGapRepo satisfies ports.GapRepository so the handler test drives the
// REAL usecase (instance fan-out + nested assembly + DTO mapping) without
// a DB. It records the instance filter it was asked to enumerate.
type fakeGapRepo struct {
	instances   []string
	missing     map[string]int
	wholeSeason map[string]int
	episodes    map[string][]ports.GapEpisodeRow
	err         error
}

func (f *fakeGapRepo) DistinctInstances(context.Context) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.instances, nil
}
func (f *fakeGapRepo) MissingEpisodeCount(_ context.Context, instance string, _ time.Time) (int, error) {
	return f.missing[instance], nil
}
func (f *fakeGapRepo) WholeSeasonMissingCount(_ context.Context, instance string, _ time.Time) (int, error) {
	return f.wholeSeason[instance], nil
}
func (f *fakeGapRepo) GapEpisodes(_ context.Context, instance string, _ time.Time, _ int) ([]ports.GapEpisodeRow, error) {
	return f.episodes[instance], nil
}

func newGapsHandler(repo ports.GapRepository) *GapsHandler {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	uc := gaps.NewUseCase(repo).WithClock(func() time.Time { return now })
	return NewGapsHandler(uc, nil)
}

func TestGapsHandler_Get_OK(t *testing.T) {
	t.Parallel()
	air := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	repo := &fakeGapRepo{
		instances:   []string{"main"},
		missing:     map[string]int{"main": 2},
		wholeSeason: map[string]int{"main": 1},
		episodes: map[string][]ports.GapEpisodeRow{
			"main": {
				{SeriesID: 42, Title: "The Expanse", SeasonNumber: 2, EpisodeNumber: 1, EpisodeID: 100, AirDate: &air, SeasonAiredMonitored: 2, SeasonMissing: 2},
				{SeriesID: 42, Title: "The Expanse", SeasonNumber: 2, EpisodeNumber: 2, EpisodeID: 101, AirDate: &air, SeasonAiredMonitored: 2, SeasonMissing: 2},
			},
		},
	}
	h := newGapsHandler(repo)

	r := gin.New()
	r.GET("/api/v1/insights/gaps", h.Get)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/insights/gaps", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.GapReportDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Instances, 1)
	inst := body.Instances[0]
	assert.Equal(t, "main", inst.InstanceName)
	assert.Equal(t, 2, inst.MissingEpisodeCount)
	assert.Equal(t, 1, inst.WholeSeasonMissingCount)
	require.Len(t, inst.Series, 1)
	assert.Equal(t, domain.SeriesID(42), inst.Series[0].SeriesID)
	require.Len(t, inst.Series[0].Seasons, 1)
	assert.True(t, inst.Series[0].Seasons[0].WholeSeasonMissing)
	require.Len(t, inst.Series[0].Seasons[0].Episodes, 2)
	assert.Equal(t, domain.EpisodeID(100), inst.Series[0].Seasons[0].Episodes[0].EpisodeID)
}

func TestGapsHandler_Get_InstanceFilter(t *testing.T) {
	t.Parallel()
	repo := &fakeGapRepo{
		instances:   []string{"anime", "main"}, // must be bypassed by filter
		missing:     map[string]int{"main": 3},
		wholeSeason: map[string]int{"main": 0},
		episodes:    map[string][]ports.GapEpisodeRow{},
	}
	h := newGapsHandler(repo)

	r := gin.New()
	r.GET("/api/v1/insights/gaps", h.Get)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/insights/gaps?instance=main", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.GapReportDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Instances, 1)
	assert.Equal(t, "main", body.Instances[0].InstanceName)
	assert.Equal(t, 3, body.Instances[0].MissingEpisodeCount)
}

func TestGapsHandler_Get_Error500(t *testing.T) {
	t.Parallel()
	repo := &fakeGapRepo{err: errors.New("db down")}
	h := newGapsHandler(repo)

	r := gin.New()
	r.GET("/api/v1/insights/gaps", h.Get)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/insights/gaps", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusInternalServerError, w.Code)

	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "gaps unavailable", body.Error)
}
