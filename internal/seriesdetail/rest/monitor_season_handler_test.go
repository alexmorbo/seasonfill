package rest

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

type fakeMonitorExec struct {
	res         seriesdetail.MonitorSeasonResult
	err         error
	called      bool
	gotInstance domain.InstanceName
	gotSeries   domain.SeriesID
	gotSeason   int
	gotSearch   bool
}

func (f *fakeMonitorExec) Execute(_ context.Context, name domain.InstanceName, id domain.SeriesID, season int, search bool) (seriesdetail.MonitorSeasonResult, error) {
	f.called = true
	f.gotInstance = name
	f.gotSeries = id
	f.gotSeason = season
	f.gotSearch = search
	return f.res, f.err
}

// newMonitorRouter mounts gin + the typed-error middleware so the handler's
// c.Error(err) dispatch reaches the JSON envelope writer.
func newMonitorRouter(h *MonitorSeasonHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(slog.New(slog.NewTextHandler(io.Discard, nil))))
	r.POST("/api/v1/instances/:name/series/:id/seasons/:season/monitor", h.Post)
	return r
}

func doMonitor(r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, reader)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestMonitorSeasonHandler_success_200(t *testing.T) {
	fake := &fakeMonitorExec{res: seriesdetail.MonitorSeasonResult{
		SonarrSeriesID: 122, SeasonNumber: 2, Monitored: true, Searched: true,
	}}
	r := newMonitorRouter(NewMonitorSeasonHandler(fake, nil))

	w := doMonitor(r, "/api/v1/instances/main/series/42/seasons/2/monitor", `{"search": true}`)
	require.Equal(t, http.StatusOK, w.Code)

	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "main", out["instance"])
	assert.EqualValues(t, 42, out["series_id"])
	assert.EqualValues(t, 2, out["season_number"])
	assert.Equal(t, true, out["monitored"])
	assert.Equal(t, true, out["searched"])
}

func TestMonitorSeasonHandler_empty_body_defaults_search_true(t *testing.T) {
	fake := &fakeMonitorExec{res: seriesdetail.MonitorSeasonResult{SeasonNumber: 2, Monitored: true, Searched: true}}
	r := newMonitorRouter(NewMonitorSeasonHandler(fake, nil))

	w := doMonitor(r, "/api/v1/instances/main/series/42/seasons/2/monitor", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, fake.called)
	assert.True(t, fake.gotSearch, "empty body must default search=true")
}

func TestMonitorSeasonHandler_search_false_passthrough(t *testing.T) {
	fake := &fakeMonitorExec{res: seriesdetail.MonitorSeasonResult{SeasonNumber: 2, Monitored: true}}
	r := newMonitorRouter(NewMonitorSeasonHandler(fake, nil))

	w := doMonitor(r, "/api/v1/instances/main/series/42/seasons/2/monitor", `{"search": false}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, fake.called)
	assert.False(t, fake.gotSearch)
}

func TestMonitorSeasonHandler_invalid_series_id_400(t *testing.T) {
	fake := &fakeMonitorExec{}
	r := newMonitorRouter(NewMonitorSeasonHandler(fake, nil))

	for _, id := range []string{"0", "-3", "abc"} {
		w := doMonitor(r, "/api/v1/instances/main/series/"+id+"/seasons/2/monitor", "")
		assert.Equalf(t, http.StatusBadRequest, w.Code, "id=%q", id)
	}
	assert.False(t, fake.called)
}

func TestMonitorSeasonHandler_invalid_season_400(t *testing.T) {
	fake := &fakeMonitorExec{}
	r := newMonitorRouter(NewMonitorSeasonHandler(fake, nil))

	w := doMonitor(r, "/api/v1/instances/main/series/42/seasons/abc/monitor", "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.False(t, fake.called)
}

func TestMonitorSeasonHandler_not_in_instance_404(t *testing.T) {
	fake := &fakeMonitorExec{err: &sharedErrors.InstanceNotFoundError{Name: "main"}}
	r := newMonitorRouter(NewMonitorSeasonHandler(fake, nil))

	w := doMonitor(r, "/api/v1/instances/main/series/42/seasons/2/monitor", "")
	require.Equal(t, http.StatusNotFound, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "instance_not_found", out["error"])
}

func TestMonitorSeasonHandler_sonarr_unreachable_502(t *testing.T) {
	fake := &fakeMonitorExec{err: &sharedErrors.SonarrUnreachableError{Instance: "main"}}
	r := newMonitorRouter(NewMonitorSeasonHandler(fake, nil))

	w := doMonitor(r, "/api/v1/instances/main/series/42/seasons/2/monitor", "")
	require.Equal(t, http.StatusBadGateway, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "sonarr_unreachable", out["error"])
}
