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

	"github.com/alexmorbo/seasonfill/internal/catalog/app/stats"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

type fakeStatsRepo struct {
	instances []string
	totals    map[string]ports.StatsTotals
	genres    map[string][]ports.StatsKindBucket
	networks  map[string][]ports.StatsKindBucket
	grabs     map[string]ports.StatsGrabCounts
	torrents  map[string]ports.StatsTorrentTotals
	err       error
}

func (f *fakeStatsRepo) DistinctInstances(context.Context) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.instances, nil
}
func (f *fakeStatsRepo) Totals(_ context.Context, i string) (ports.StatsTotals, error) {
	return f.totals[i], nil
}
func (f *fakeStatsRepo) ByGenre(_ context.Context, i string, _ int) ([]ports.StatsKindBucket, error) {
	return f.genres[i], nil
}
func (f *fakeStatsRepo) ByNetwork(_ context.Context, i string, _ int) ([]ports.StatsKindBucket, error) {
	return f.networks[i], nil
}
func (f *fakeStatsRepo) GrabSuccess(_ context.Context, i string) (ports.StatsGrabCounts, error) {
	return f.grabs[i], nil
}
func (f *fakeStatsRepo) TorrentTotals(_ context.Context, i string) (ports.StatsTorrentTotals, error) {
	return f.torrents[i], nil
}

func newStatsHandler(repo ports.StatsRepository) *StatsHandler {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	uc := stats.NewUseCase(repo).WithClock(func() time.Time { return now })
	return NewStatsHandler(uc, nil)
}

func TestStatsHandler_Get_OK(t *testing.T) {
	t.Parallel()
	repo := &fakeStatsRepo{
		instances: []string{"main"},
		totals:    map[string]ports.StatsTotals{"main": {SeriesCount: 3, EpisodesOnDisk: 17, TotalSizeBytes: 4500}},
		genres:    map[string][]ports.StatsKindBucket{"main": {{Name: "Drama", SeriesCount: 2, SizeBytes: 4000}}},
		networks:  map[string][]ports.StatsKindBucket{"main": {{Name: "HBO", SeriesCount: 2, SizeBytes: 4000}}},
		grabs:     map[string]ports.StatsGrabCounts{"main": {Grabbed: 1, Imported: 3, Failed: 1}},
		torrents:  map[string]ports.StatsTorrentTotals{"main": {TorrentCount: 2, TotalUploadedBytes: 400, TotalDownloadedBytes: 100, AvgRatio: 3.0}},
	}
	h := newStatsHandler(repo)

	r := gin.New()
	r.GET("/api/v1/insights/stats", h.Get)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/insights/stats", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.StatsReportDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Instances, 1)
	inst := body.Instances[0]
	assert.Equal(t, "main", inst.InstanceName)
	assert.Equal(t, 3, inst.Totals.SeriesCount)
	assert.Equal(t, int64(4500), inst.Totals.TotalSizeBytes)
	require.Len(t, inst.ByGenre, 1)
	assert.Equal(t, "Drama", inst.ByGenre[0].Genre)
	require.Len(t, inst.ByNetwork, 1)
	assert.Equal(t, "HBO", inst.ByNetwork[0].Network)
	assert.Equal(t, 3, inst.GrabSuccess.Imported)
	assert.InDelta(t, 0.75, inst.GrabSuccess.SuccessRate, 1e-9) // 3/(3+1)
	assert.Equal(t, 2, inst.TorrentTotals.TorrentCount)
	assert.InDelta(t, 3.0, inst.TorrentTotals.AvgRatio, 1e-9)
}

func TestStatsHandler_Get_InstanceFilter(t *testing.T) {
	t.Parallel()
	repo := &fakeStatsRepo{
		instances: []string{"anime", "main"}, // must be bypassed by filter
		totals:    map[string]ports.StatsTotals{"main": {SeriesCount: 9}},
	}
	h := newStatsHandler(repo)

	r := gin.New()
	r.GET("/api/v1/insights/stats", h.Get)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/insights/stats?instance=main", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.StatsReportDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Instances, 1)
	assert.Equal(t, "main", body.Instances[0].InstanceName)
	assert.Equal(t, 9, body.Instances[0].Totals.SeriesCount)
}

func TestStatsHandler_Get_Error500(t *testing.T) {
	t.Parallel()
	repo := &fakeStatsRepo{err: errors.New("db down")}
	h := newStatsHandler(repo)

	r := gin.New()
	r.GET("/api/v1/insights/stats", h.Get)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/insights/stats", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusInternalServerError, w.Code)

	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "stats unavailable", body.Error)
}
