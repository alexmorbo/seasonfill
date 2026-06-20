package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/admin/rest/healthcheck"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type episodesFakeSonarr struct {
	*fakeSonarr
	eps map[int]map[int][]series.Episode // seriesID → seasonNumber → eps
	err error
}

func (e *episodesFakeSonarr) ListEpisodes(_ context.Context, seriesID shareddomain.SonarrSeriesID, seasonNumber int) ([]series.Episode, error) {
	if e.err != nil {
		return nil, e.err
	}
	bySeason, ok := e.eps[int(seriesID)]
	if !ok {
		return nil, nil
	}
	return bySeason[seasonNumber], nil
}

func (e *episodesFakeSonarr) ListEpisodesBySeries(_ context.Context, _ shareddomain.SonarrSeriesID) ([]series.Episode, error) {
	return nil, nil
}

func doSeasonEpisodes(
	t *testing.T,
	name string,
	seriesID, seasonNumber int,
	clients map[string]ports.SonarrClient,
) *httptest.ResponseRecorder {
	t.Helper()
	c := healthcheck.New(openInstancesDB(t), nil)
	r := gin.New()
	h := NewInstancesHandler(c, buildRegistry(clients, nil, nil), nil)
	r.GET(
		"/api/v1/instances/:name/series/:id/seasons/:season/episodes",
		h.SeasonEpisodes,
	)
	url := "/api/v1/instances/" + name +
		"/series/" + strconv.Itoa(seriesID) +
		"/seasons/" + strconv.Itoa(seasonNumber) + "/episodes"
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestInstancesHandler_SeasonEpisodes_OK(t *testing.T) {
	t.Parallel()
	now := time.Now()
	past := now.Add(-7 * 24 * time.Hour)
	future := now.Add(7 * 24 * time.Hour)
	mf := &episodesFakeSonarr{
		fakeSonarr: &fakeSonarr{name: "alpha"},
		eps: map[int]map[int][]series.Episode{
			122: {
				5: {
					{ID: 5001, Number: 1, SeasonNumber: 5, Monitored: true, HasFile: true, AirDateUTC: past},
					{ID: 5002, Number: 2, SeasonNumber: 5, Monitored: true, HasFile: true, AirDateUTC: past},
					{ID: 5003, Number: 3, SeasonNumber: 5, Monitored: true, HasFile: false, AirDateUTC: past},
					{ID: 5004, Number: 4, SeasonNumber: 5, Monitored: true, HasFile: false, AirDateUTC: future},
					{ID: 5005, Number: 5, SeasonNumber: 5, Monitored: false, HasFile: false, AirDateUTC: past},
				},
			},
		},
	}
	w := doSeasonEpisodes(t, "alpha", 122, 5,
		map[string]ports.SonarrClient{"alpha": mf})
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Items []struct {
			Number    int  `json:"number"`
			Monitored bool `json:"monitored"`
			HasFile   bool `json:"has_file"`
			Aired     bool `json:"aired"`
		} `json:"items"`
		Total int `json:"total"`
		Have  int `json:"have"`
		Miss  int `json:"miss"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 5, body.Total)
	assert.Equal(t, 2, body.Have, "two aired+have")
	assert.Equal(t, 1, body.Miss, "only ep 3 is monitored+aired+missing")
	require.Len(t, body.Items, 5)
	// items sorted ascending
	for i, it := range body.Items {
		assert.Equal(t, i+1, it.Number)
	}
}

func TestInstancesHandler_SeasonEpisodes_Empty(t *testing.T) {
	t.Parallel()
	mf := &episodesFakeSonarr{
		fakeSonarr: &fakeSonarr{name: "alpha"},
		eps:        map[int]map[int][]series.Episode{},
	}
	w := doSeasonEpisodes(t, "alpha", 999, 1,
		map[string]ports.SonarrClient{"alpha": mf})
	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Items []struct{} `json:"items"`
		Total int        `json:"total"`
		Have  int        `json:"have"`
		Miss  int        `json:"miss"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Empty(t, body.Items)
	assert.Equal(t, 0, body.Total)
	assert.Equal(t, 0, body.Have)
	assert.Equal(t, 0, body.Miss)
}

func TestInstancesHandler_SeasonEpisodes_UnknownInstance(t *testing.T) {
	t.Parallel()
	w := doSeasonEpisodes(t, "ghost", 1, 1,
		map[string]ports.SonarrClient{})
	require.Equal(t, http.StatusNotFound, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "ghost")
}

func TestInstancesHandler_SeasonEpisodes_BadID(t *testing.T) {
	t.Parallel()
	mf := &episodesFakeSonarr{fakeSonarr: &fakeSonarr{name: "alpha"}}
	c := healthcheck.New(openInstancesDB(t), nil)
	r := gin.New()
	h := NewInstancesHandler(c, buildRegistry(
		map[string]ports.SonarrClient{"alpha": mf}, nil, nil), nil)
	r.GET(
		"/api/v1/instances/:name/series/:id/seasons/:season/episodes",
		h.SeasonEpisodes,
	)
	for _, tc := range []struct {
		name string
		url  string
	}{
		{"non-numeric id", "/api/v1/instances/alpha/series/abc/seasons/1/episodes"},
		{"zero id", "/api/v1/instances/alpha/series/0/seasons/1/episodes"},
		{"negative id", "/api/v1/instances/alpha/series/-1/seasons/1/episodes"},
		{"non-numeric season", "/api/v1/instances/alpha/series/1/seasons/xx/episodes"},
		{"negative season", "/api/v1/instances/alpha/series/1/seasons/-1/episodes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(
				context.Background(), http.MethodGet, tc.url, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code, tc.url)
		})
	}
}

func TestInstancesHandler_SeasonEpisodes_SonarrUnauthorized(t *testing.T) {
	t.Parallel()
	mf := &episodesFakeSonarr{
		fakeSonarr: &fakeSonarr{name: "alpha"},
		err:        domain.ErrInstanceUnauthorized,
	}
	w := doSeasonEpisodes(t, "alpha", 1, 1,
		map[string]ports.SonarrClient{"alpha": mf})
	require.Equal(t, http.StatusBadGateway, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "sonarr unauthorized", body["error"])
}

func TestInstancesHandler_SeasonEpisodes_SonarrUnavailable(t *testing.T) {
	t.Parallel()
	mf := &episodesFakeSonarr{
		fakeSonarr: &fakeSonarr{name: "alpha"},
		err:        errors.New("dial tcp: nope"),
	}
	w := doSeasonEpisodes(t, "alpha", 1, 1,
		map[string]ports.SonarrClient{"alpha": mf})
	require.Equal(t, http.StatusBadGateway, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "sonarr unavailable", body["error"])
}

func TestInstancesHandler_SeasonEpisodes_Season0(t *testing.T) {
	t.Parallel()
	now := time.Now()
	mf := &episodesFakeSonarr{
		fakeSonarr: &fakeSonarr{name: "alpha"},
		eps: map[int]map[int][]series.Episode{
			122: {
				0: {
					{ID: 1, Number: 1, SeasonNumber: 0, Monitored: false, HasFile: true, AirDateUTC: now.Add(-24 * time.Hour)},
				},
			},
		},
	}
	w := doSeasonEpisodes(t, "alpha", 122, 0,
		map[string]ports.SonarrClient{"alpha": mf})
	require.Equal(t, http.StatusOK, w.Code, "season 0 must not be filtered")
	var body struct {
		Items []struct{ Number int } `json:"items"`
		Total int                    `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 1, body.Total)
}

func TestInstancesHandler_SeasonEpisodes_SortedAscending(t *testing.T) {
	t.Parallel()
	now := time.Now().Add(-30 * 24 * time.Hour)
	mf := &episodesFakeSonarr{
		fakeSonarr: &fakeSonarr{name: "alpha"},
		eps: map[int]map[int][]series.Episode{
			1: {
				1: {
					{Number: 7, SeasonNumber: 1, Monitored: true, HasFile: false, AirDateUTC: now},
					{Number: 2, SeasonNumber: 1, Monitored: true, HasFile: true, AirDateUTC: now},
					{Number: 1, SeasonNumber: 1, Monitored: true, HasFile: true, AirDateUTC: now},
					{Number: 4, SeasonNumber: 1, Monitored: true, HasFile: false, AirDateUTC: now},
				},
			},
		},
	}
	w := doSeasonEpisodes(t, "alpha", 1, 1,
		map[string]ports.SonarrClient{"alpha": mf})
	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Items []struct{ Number int } `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	nums := make([]int, len(body.Items))
	for i, it := range body.Items {
		nums[i] = it.Number
	}
	assert.Equal(t, []int{1, 2, 4, 7}, nums)
}
