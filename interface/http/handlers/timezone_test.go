package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/runtime/tz"
)

// inMemoryStore implements tz.Store with a single field — same
// shape as the resolver_test fakeStore but local to the handler
// package so we don't reach across packages for a test double.
type inMemoryStore struct{ tz string }

func (s *inMemoryStore) GetTimezone(_ context.Context) (string, error) { return s.tz, nil }
func (s *inMemoryStore) SetTimezone(_ context.Context, name string) error {
	s.tz = name
	return nil
}

func newTimezoneTestRouter(t *testing.T, envTZ string) (*gin.Engine, *tz.Resolver) {
	t.Helper()
	t.Setenv("TZ", envTZ)
	gin.SetMode(gin.TestMode)
	resolver := tz.New(context.Background(), &inMemoryStore{}, nil)
	h := NewTimezoneHandler(resolver, nil)
	r := gin.New()
	r.GET("/api/v1/settings/timezone", h.Get)
	r.PATCH("/api/v1/settings/timezone", h.Patch)
	return r, resolver
}

func TestTimezoneHandler_Get_DefaultsToUTC(t *testing.T) {
	r, _ := newTimezoneTestRouter(t, "")
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/api/v1/settings/timezone", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var body dto.TimezoneResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "UTC", body.Timezone)
	assert.Equal(t, "default", body.Source)
	assert.False(t, body.RequiresRestart)
}

func TestTimezoneHandler_Get_HonorsEnv(t *testing.T) {
	r, _ := newTimezoneTestRouter(t, "Europe/Moscow")
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/api/v1/settings/timezone", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var body dto.TimezoneResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "Europe/Moscow", body.Timezone)
	assert.Equal(t, "env", body.Source)
}

func TestTimezoneHandler_Patch_Valid_PersistsAndFlipsSource(t *testing.T) {
	r, resolver := newTimezoneTestRouter(t, "")
	patchBody, _ := json.Marshal(dto.TimezonePatchRequest{Timezone: "America/New_York"})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPatch, "/api/v1/settings/timezone", bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.TimezoneResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "America/New_York", body.Timezone)
	assert.Equal(t, "db", body.Source)
	assert.True(t, body.RequiresRestart)
	assert.Equal(t, "America/New_York", resolver.Name())
}

func TestTimezoneHandler_Patch_Invalid_Returns400(t *testing.T) {
	r, _ := newTimezoneTestRouter(t, "")
	patchBody, _ := json.Marshal(dto.TimezonePatchRequest{Timezone: "Not/A/Zone"})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPatch, "/api/v1/settings/timezone", bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)

	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "INVALID_TIMEZONE", body.Code)
}

func TestTimezoneHandler_Patch_EmptyClearsOverride(t *testing.T) {
	r, resolver := newTimezoneTestRouter(t, "Europe/Moscow")
	// First, PATCH to set DB override.
	patchBody, _ := json.Marshal(dto.TimezonePatchRequest{Timezone: "America/New_York"})
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPatch, "/api/v1/settings/timezone", bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(httptest.NewRecorder(), req)
	require.Equal(t, "America/New_York", resolver.Name())

	// Now PATCH empty — should fall back to env.
	clearBody, _ := json.Marshal(dto.TimezonePatchRequest{Timezone: ""})
	w := httptest.NewRecorder()
	req2 := httptest.NewRequestWithContext(t.Context(),
		http.MethodPatch, "/api/v1/settings/timezone", bytes.NewReader(clearBody))
	req2.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req2)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.TimezoneResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "Europe/Moscow", body.Timezone)
	assert.Equal(t, "env", body.Source)
}
