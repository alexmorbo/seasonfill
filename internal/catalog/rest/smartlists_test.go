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

	"github.com/alexmorbo/seasonfill/internal/catalog/app/smartlists"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// fakeSmartListsRepo drives the REAL usecase (instance fan-out + shelf
// assembly + DTO mapping) without a DB.
type fakeSmartListsRepo struct {
	instances      []string
	ended          map[string][]ports.SmartListSeriesRow
	endedCount     map[string]int
	returning      map[string][]ports.SmartListSeriesRow
	returningCount map[string]int
	hiatus         map[string][]ports.SmartListSeriesRow
	hiatusCount    map[string]int
	err            error
}

func (f *fakeSmartListsRepo) DistinctInstances(context.Context) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.instances, nil
}
func (f *fakeSmartListsRepo) EndedIncomplete(_ context.Context, inst string, _ time.Time, _ int) ([]ports.SmartListSeriesRow, error) {
	return f.ended[inst], nil
}
func (f *fakeSmartListsRepo) EndedIncompleteCount(_ context.Context, inst string, _ time.Time) (int, error) {
	return f.endedCount[inst], nil
}
func (f *fakeSmartListsRepo) ReturningSoon(_ context.Context, inst string, _, _ time.Time, _ int) ([]ports.SmartListSeriesRow, error) {
	return f.returning[inst], nil
}
func (f *fakeSmartListsRepo) ReturningSoonCount(_ context.Context, inst string, _, _ time.Time) (int, error) {
	return f.returningCount[inst], nil
}
func (f *fakeSmartListsRepo) Hiatus(_ context.Context, inst string, _, _ time.Time, _ int) ([]ports.SmartListSeriesRow, error) {
	return f.hiatus[inst], nil
}
func (f *fakeSmartListsRepo) HiatusCount(_ context.Context, inst string, _, _ time.Time) (int, error) {
	return f.hiatusCount[inst], nil
}

func newSmartListsHandler(repo ports.SmartListsRepository) *SmartListsHandler {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	uc := smartlists.NewUseCase(repo).WithClock(func() time.Time { return now })
	return NewSmartListsHandler(uc, nil)
}

func TestSmartListsHandler_Get_OK(t *testing.T) {
	t.Parallel()
	next := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	repo := &fakeSmartListsRepo{
		instances:      []string{"main"},
		ended:          map[string][]ports.SmartListSeriesRow{"main": {{SeriesID: 42, SonarrID: 31, Title: "The Expanse", MissingCount: 5}}},
		endedCount:     map[string]int{"main": 7},
		returning:      map[string][]ports.SmartListSeriesRow{"main": {{SeriesID: 88, SonarrID: 12, Title: "Foundation", NextAirDate: &next}}},
		returningCount: map[string]int{"main": 1},
		hiatus:         map[string][]ports.SmartListSeriesRow{"main": {{SeriesID: 90, SonarrID: 7, Title: "Severance", LastAiredAt: &last}}},
		hiatusCount:    map[string]int{"main": 1},
	}
	h := newSmartListsHandler(repo)

	r := gin.New()
	r.GET("/api/v1/insights/lists", h.Get)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/insights/lists", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.SmartListsReportDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Instances, 1)
	inst := body.Instances[0]
	assert.Equal(t, "main", inst.InstanceName)
	require.Len(t, inst.Shelves, 3)

	assert.Equal(t, "ended_incomplete", inst.Shelves[0].Key)
	assert.Equal(t, 7, inst.Shelves[0].Count)
	require.Len(t, inst.Shelves[0].Series, 1)
	require.NotNil(t, inst.Shelves[0].Series[0].MissingCount)
	assert.Equal(t, 5, *inst.Shelves[0].Series[0].MissingCount)

	assert.Equal(t, "returning_soon", inst.Shelves[1].Key)
	require.NotNil(t, inst.Shelves[1].Series[0].NextAirDate)

	assert.Equal(t, "hiatus", inst.Shelves[2].Key)
	require.NotNil(t, inst.Shelves[2].Series[0].LastAiredAt)
}

func TestSmartListsHandler_Get_InstanceFilter(t *testing.T) {
	t.Parallel()
	repo := &fakeSmartListsRepo{
		instances:  []string{"anime", "main"}, // must be bypassed by filter
		endedCount: map[string]int{"main": 2},
	}
	h := newSmartListsHandler(repo)

	r := gin.New()
	r.GET("/api/v1/insights/lists", h.Get)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/insights/lists?instance=main", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.SmartListsReportDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Instances, 1)
	assert.Equal(t, "main", body.Instances[0].InstanceName)
	assert.Equal(t, 2, body.Instances[0].Shelves[0].Count)
}

func TestSmartListsHandler_Get_Error500(t *testing.T) {
	t.Parallel()
	repo := &fakeSmartListsRepo{err: errors.New("db down")}
	h := newSmartListsHandler(repo)

	r := gin.New()
	r.GET("/api/v1/insights/lists", h.Get)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/insights/lists", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusInternalServerError, w.Code)

	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "smart lists unavailable", body.Error)
}
