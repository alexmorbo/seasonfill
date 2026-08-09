package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/icsfeed"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

type fakeICSService struct {
	renderBody  string
	renderErr   error
	minted      icsfeed.Minted
	mintErr     error
	revokeEpoch int64
	revokeErr   error
	sawToken    string
}

func (f *fakeICSService) Render(_ context.Context, token string) (string, error) {
	f.sawToken = token
	return f.renderBody, f.renderErr
}

func (f *fakeICSService) Mint(_ context.Context, _ string) (icsfeed.Minted, error) {
	return f.minted, f.mintErr
}

func (f *fakeICSService) Revoke(_ context.Context) (int64, error) {
	return f.revokeEpoch, f.revokeErr
}

func newICSEngine(svc ICSService) *gin.Engine {
	h := NewICSHandler(svc, nil)
	r := gin.New()
	r.GET("/api/v1/calendar.ics", h.Consume)
	r.GET("/api/v1/calendar.ics/token", h.Mint)
	r.POST("/api/v1/calendar.ics/revoke", h.Revoke)
	return r
}

func TestICSConsume_200(t *testing.T) {
	t.Parallel()
	svc := &fakeICSService{renderBody: "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"}
	r := newICSEngine(svc)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/calendar.ics?token=abc.def", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/calendar; charset=utf-8", w.Header().Get("Content-Type"))
	assert.True(t, strings.HasPrefix(w.Body.String(), "BEGIN:VCALENDAR"))
	assert.Equal(t, "abc.def", svc.sawToken)
}

func TestICSConsume_401_EmptyToken(t *testing.T) {
	t.Parallel()
	svc := &fakeICSService{}
	r := newICSEngine(svc)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/calendar.ics", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "invalid or revoked calendar token", body.Error)
	assert.Empty(t, svc.sawToken, "handler must not call the service on empty token")
}

func TestICSConsume_401_Revoked(t *testing.T) {
	t.Parallel()
	svc := &fakeICSService{renderErr: icsfeed.ErrRevoked}
	r := newICSEngine(svc)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/calendar.ics?token=stale.token", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "invalid or revoked calendar token", body.Error)
}

func TestICSConsume_500(t *testing.T) {
	t.Parallel()
	svc := &fakeICSService{renderErr: errors.New("db down")}
	r := newICSEngine(svc)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/calendar.ics?token=abc.def", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "calendar feed unavailable", body.Error)
}

func TestICSMint_200(t *testing.T) {
	t.Parallel()
	svc := &fakeICSService{minted: icsfeed.Minted{Token: "tok en+/", Scope: "all"}}
	r := newICSEngine(svc)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/calendar.ics/token?scope=all", nil)
	req.Host = "sf.arr.morbo.dev"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		ICSURL    string `json:"ics_url"`
		WebcalURL string `json:"webcal_url"`
		Scope     string `json:"scope"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "all", body.Scope)
	assert.True(t, strings.HasPrefix(body.ICSURL, "https://sf.arr.morbo.dev/api/v1/calendar.ics?token="),
		"got %q", body.ICSURL)
	assert.True(t, strings.HasPrefix(body.WebcalURL, "webcal://sf.arr.morbo.dev/api/v1/calendar.ics?token="),
		"got %q", body.WebcalURL)
	// token is URL-escaped in the query
	assert.Contains(t, body.ICSURL, "token=tok+en%2B%2F")
}

func TestICSMint_500(t *testing.T) {
	t.Parallel()
	svc := &fakeICSService{mintErr: errors.New("db down")}
	r := newICSEngine(svc)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/calendar.ics/token", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestICSRevoke_200(t *testing.T) {
	t.Parallel()
	svc := &fakeICSService{revokeEpoch: 3}
	r := newICSEngine(svc)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/calendar.ics/revoke", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Epoch int64 `json:"epoch"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, int64(3), body.Epoch)
}
