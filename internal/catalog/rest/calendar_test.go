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

	"github.com/alexmorbo/seasonfill/internal/catalog/app/calendar"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

type fakeCalUC struct {
	last calendar.Query
	rep  calendar.Report
	err  error
}

func (f *fakeCalUC) Build(_ context.Context, q calendar.Query) (calendar.Report, error) {
	f.last = q
	return f.rep, f.err
}

func serveCalendar(t *testing.T, uc CalendarUseCase, url string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewCalendarHandler(uc, nil)
	r := gin.New()
	r.GET("/api/v1/calendar", h.Get)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCalendarHandler_ParamParsing(t *testing.T) {
	t.Parallel()
	uc := &fakeCalUC{}
	w := serveCalendar(t, uc,
		"/api/v1/calendar?from=2026-01-01&to=2026-02-01&scope=followed&instance=main&only-premieres=true&lang=ru-RU")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), uc.last.From)
	// `to` widened to end-of-day.
	assert.Equal(t, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).Add(24*time.Hour-time.Nanosecond), uc.last.To)
	assert.Equal(t, "followed", uc.last.Scope)
	assert.Equal(t, "main", uc.last.Instance)
	assert.True(t, uc.last.OnlyPremieres)
	assert.Equal(t, "ru-RU", uc.last.Lang)
}

func TestCalendarHandler_DefaultsAndOnlyLibraryShortcut(t *testing.T) {
	t.Parallel()
	uc := &fakeCalUC{}
	w := serveCalendar(t, uc, "/api/v1/calendar?only-library=1")
	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, uc.last.From.IsZero(), "no from → usecase defaults")
	assert.True(t, uc.last.To.IsZero(), "no to → usecase defaults")
	assert.Equal(t, "library", uc.last.Scope, "only-library forces scope=library")
}

func TestCalendarHandler_BadDate400(t *testing.T) {
	t.Parallel()
	uc := &fakeCalUC{}
	w := serveCalendar(t, uc, "/api/v1/calendar?from=nonsense")
	require.Equal(t, http.StatusBadRequest, w.Code)
	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body.Error, "from")
}

func TestCalendarHandler_Error500(t *testing.T) {
	t.Parallel()
	uc := &fakeCalUC{err: errors.New("db down")}
	w := serveCalendar(t, uc, "/api/v1/calendar")
	require.Equal(t, http.StatusInternalServerError, w.Code)
	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "calendar unavailable", body.Error)
}

func TestCalendarHandler_DTOMapping(t *testing.T) {
	t.Parallel()
	air := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	ms := "premiere"
	uc := &fakeCalUC{rep: calendar.Report{
		GeneratedAt: air, From: air.AddDate(0, -3, 0), To: air.AddDate(0, 3, 0),
		Days: []calendar.Day{{Date: "2026-06-25", Events: []calendar.Event{{
			SeriesID: 42, Title: "The Expanse", Season: 1, Episode: 1, AirDate: air,
			State: "downloaded", InLibraryInstances: []string{"main"},
			SeasonPremiere: true, Milestone: &ms, MediaType: "tv",
		}}}},
	}}
	w := serveCalendar(t, uc, "/api/v1/calendar")
	require.Equal(t, http.StatusOK, w.Code)
	var body dto.CalendarDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Days, 1)
	require.Len(t, body.Days[0].Events, 1)
	ev := body.Days[0].Events[0]
	assert.Equal(t, "2026-06-25", body.Days[0].Date)
	assert.Equal(t, "The Expanse", ev.Title)
	assert.Equal(t, "downloaded", ev.State)
	assert.Equal(t, "tv", ev.MediaType)
	require.NotNil(t, ev.Milestone)
	assert.Equal(t, "premiere", *ev.Milestone)
	assert.True(t, ev.SeasonPremiere)
}
