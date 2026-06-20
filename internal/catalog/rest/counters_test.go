package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// fakeCounterRepo lets handler tests skip the DB entirely.
type fakeCounterRepo struct {
	buckets []ports.CounterBucket
	avg     float64
	err     error
}

func (f *fakeCounterRepo) BucketCounters(_ context.Context, _ domain.InstanceName, _ ports.CounterWindow, _ time.Time) ([]ports.CounterBucket, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.buckets, nil
}

func (f *fakeCounterRepo) AvgGrabsLast7Days(_ context.Context, _ domain.InstanceName, _ time.Time) (float64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.avg, nil
}

func registryWith(names ...string) InstanceRegistry {
	m := map[string]scan.Instance{}
	for _, n := range names {
		m[n] = scan.Instance{}
	}
	return InstanceRegistry{Load: func() map[string]scan.Instance { return m }}
}

func TestCountersHandler_ForInstance_OK(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 7, 15, 30, 0, 0, time.UTC)
	repo := &fakeCounterRepo{
		buckets: []ports.CounterBucket{
			{BucketStart: now.Truncate(time.Hour).Add(-1 * time.Hour), Grabs: 3, Imports: 2, Fails: 1},
			{BucketStart: now.Truncate(time.Hour), Grabs: 1, Imports: 1, Fails: 0},
		},
		avg: 9.5,
	}
	h := NewCountersHandler(registryWith("alpha"), repo, nil).
		WithClock(func() time.Time { return now })

	r := gin.New()
	r.GET("/api/v1/instances/:name/counters", h.ForInstance)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/alpha/counters?window=24h", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.InstanceCountersDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, domain.InstanceName("alpha"), body.InstanceName)
	assert.Equal(t, "24h", body.Window)
	assert.Equal(t, 4, body.Totals.Grabs)
	assert.Equal(t, 3, body.Totals.Imports)
	assert.Equal(t, 1, body.Totals.Fails)
	assert.InDelta(t, 9.5, body.AvgGrabs7d, 0.0001)
	require.Len(t, body.Sparkline, 2)
}

func TestCountersHandler_ForInstance_InvalidWindow(t *testing.T) {
	t.Parallel()
	h := NewCountersHandler(registryWith("alpha"), &fakeCounterRepo{}, nil)
	r := gin.New()
	r.GET("/api/v1/instances/:name/counters", h.ForInstance)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/alpha/counters?window=8h", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)

	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body.Error, "invalid window")
}

func TestCountersHandler_ForInstance_UnknownInstance(t *testing.T) {
	t.Parallel()
	h := NewCountersHandler(registryWith("alpha"), &fakeCounterRepo{}, nil)
	r := gin.New()
	r.GET("/api/v1/instances/:name/counters", h.ForInstance)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/ghost/counters", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestCountersHandler_ForInstance_DefaultWindowIs24h(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 7, 15, 30, 0, 0, time.UTC)
	h := NewCountersHandler(registryWith("alpha"), &fakeCounterRepo{}, nil).
		WithClock(func() time.Time { return now })
	r := gin.New()
	r.GET("/api/v1/instances/:name/counters", h.ForInstance)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/alpha/counters", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.InstanceCountersDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "24h", body.Window)
}

func TestCountersHandler_Aggregate_OK(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 7, 15, 30, 0, 0, time.UTC)
	repo := &fakeCounterRepo{
		buckets: []ports.CounterBucket{
			{BucketStart: now.Truncate(time.Hour), Grabs: 1, Imports: 1, Fails: 0},
		},
		avg: 3.0,
	}
	h := NewCountersHandler(registryWith("alpha", "beta"), repo, nil).
		WithClock(func() time.Time { return now })

	r := gin.New()
	r.GET("/api/v1/counters", h.Aggregate)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/counters?window=7d", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.CountersAggregateDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Items, 2)
	// Sorted alphabetically — alpha first.
	assert.Equal(t, domain.InstanceName("alpha"), body.Items[0].InstanceName)
	assert.Equal(t, domain.InstanceName("beta"), body.Items[1].InstanceName)
}

func TestCountersHandler_Aggregate_InvalidWindow(t *testing.T) {
	t.Parallel()
	h := NewCountersHandler(registryWith("alpha"), &fakeCounterRepo{}, nil)
	r := gin.New()
	r.GET("/api/v1/counters", h.Aggregate)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/counters?window=8h", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}
