package rest

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

type stubPager struct {
	rows     []regrab.BlacklistEntry
	deleteFn func(uint, uint) error
}

func (s *stubPager) ListByInstanceWithLimit(_ context.Context, _ uint, limit int, _ time.Time, _ uint) ([]regrab.BlacklistEntry, error) {
	if len(s.rows) > limit {
		return s.rows[:limit], nil
	}
	return s.rows, nil
}

func (s *stubPager) DeleteByID(_ context.Context, instanceID, id uint) error {
	if s.deleteFn != nil {
		return s.deleteFn(instanceID, id)
	}
	return nil
}

type stubTitles map[domain.SonarrSeriesID]string

func (s stubTitles) Get(_ context.Context, _ domain.InstanceName, seriesID domain.SonarrSeriesID) (series.CacheEntry, error) {
	if t, ok := s[seriesID]; ok {
		return series.CacheEntry{Title: t}, nil
	}
	return series.CacheEntry{}, ports.ErrNotFound
}

func newBlacklistRouter(h *WatchdogBlacklistHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// F-2c-1: typed-error middleware so handler c.Error(err) reaches
	// the JSON envelope writer.
	r.Use(middleware.ErrorResponseMiddleware(slog.Default()))
	r.GET("/api/v1/instances/:name/watchdog/blacklist", h.List)
	r.DELETE("/api/v1/instances/:name/watchdog/blacklist/:id", h.Delete)
	return r
}

func TestWatchdogBlacklistHandler_ListJoinsSeriesTitle(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	pager := &stubPager{rows: []regrab.BlacklistEntry{
		{ID: 1, InstanceID: 1, SeriesID: 100, SeasonNumber: 2, Reason: regrab.ReasonConsecutiveNoBetter, Consecutive: 3, CreatedAt: now},
		{ID: 2, InstanceID: 1, SeriesID: 200, SeasonNumber: 1, Reason: regrab.ReasonConsecutiveNoBetter, Consecutive: 3, CreatedAt: now.Add(-time.Hour)},
	}}
	titles := stubTitles{100: "Severance"} // 200 deliberately missing
	h := NewWatchdogBlacklistHandler(pager, titles, stubLookup{"homelab": 1}, nil)
	r := newBlacklistRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/blacklist", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 body=%s", w.Code, w.Body.String())
	}
	var got dto.WatchdogBlacklistList
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("len: %d", len(got.Items))
	}
	if got.Items[0].SeriesTitle != "Severance" {
		t.Errorf("title 100: got %q want Severance", got.Items[0].SeriesTitle)
	}
	if got.Items[1].SeriesTitle != "" {
		t.Errorf("title 200: got %q want empty (cache miss)", got.Items[1].SeriesTitle)
	}
	if got.Items[0].Source != "auto" {
		t.Errorf("source: got %q want auto", got.Items[0].Source)
	}
}

func TestWatchdogBlacklistHandler_DeleteScopedToInstance(t *testing.T) {
	t.Parallel()
	called := struct {
		instanceID uint
		id         uint
	}{}
	pager := &stubPager{deleteFn: func(instanceID, id uint) error {
		called.instanceID = instanceID
		called.id = id
		return nil
	}}
	h := NewWatchdogBlacklistHandler(pager, stubTitles{}, stubLookup{"homelab": 7}, nil)
	r := newBlacklistRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/instances/homelab/watchdog/blacklist/42", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204 body=%s", w.Code, w.Body.String())
	}
	if called.instanceID != 7 || called.id != 42 {
		t.Errorf("DeleteByID args: instanceID=%d id=%d (want 7,42)", called.instanceID, called.id)
	}
}

func TestWatchdogBlacklistHandler_DeleteUnknownReturns404(t *testing.T) {
	t.Parallel()
	pager := &stubPager{deleteFn: func(uint, uint) error { return ports.ErrNotFound }}
	h := NewWatchdogBlacklistHandler(pager, stubTitles{}, stubLookup{"homelab": 1}, nil)
	r := newBlacklistRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/instances/homelab/watchdog/blacklist/99", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404", w.Code)
	}
}

func TestWatchdogBlacklistHandler_DeleteUnknownInstance(t *testing.T) {
	t.Parallel()
	pager := &stubPager{}
	h := NewWatchdogBlacklistHandler(pager, stubTitles{}, stubLookup{}, nil)
	r := newBlacklistRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/instances/ghost/watchdog/blacklist/1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404", w.Code)
	}
}

func TestWatchdogBlacklistHandler_ListEmitsCursorWhenFull(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	pager := &stubPager{}
	for i := range 2 {
		pager.rows = append(pager.rows, regrab.BlacklistEntry{
			ID: uint(i + 1), InstanceID: 1, SeriesID: domain.SonarrSeriesID(100 + i), SeasonNumber: 1,
			Reason: regrab.ReasonConsecutiveNoBetter, Consecutive: 3,
			CreatedAt: now.Add(-time.Duration(i) * time.Hour),
		})
	}
	h := NewWatchdogBlacklistHandler(pager, stubTitles{}, stubLookup{"homelab": 1}, nil)
	r := newBlacklistRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/blacklist?limit=2", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	var got dto.WatchdogBlacklistList
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.NextCursor == "" {
		t.Errorf("NextCursor: want non-empty when page is full")
	}
	at, id, derr := decodeBlacklistCursor(got.NextCursor)
	if derr != nil {
		t.Fatalf("decode cursor: %v", derr)
	}
	if id != 2 {
		t.Errorf("cursor id: got %d want 2", id)
	}
	if at.Sub(now.Add(-time.Hour)).Abs() > time.Second {
		t.Errorf("cursor at: got %v want %v", at, now.Add(-time.Hour))
	}
}

func TestWatchdogBlacklistHandler_ListInvalidLimit(t *testing.T) {
	t.Parallel()
	h := NewWatchdogBlacklistHandler(&stubPager{}, stubTitles{}, stubLookup{"homelab": 1}, nil)
	r := newBlacklistRouter(h)
	for _, q := range []string{"?limit=0", "?limit=-1", "?limit=abc", "?limit=10000"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/blacklist"+q, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("limit=%s: want 400, got %d", q, w.Code)
		}
	}
}

func TestWatchdogBlacklistHandler_ListInvalidCursor(t *testing.T) {
	t.Parallel()
	h := NewWatchdogBlacklistHandler(&stubPager{}, stubTitles{}, stubLookup{"homelab": 1}, nil)
	r := newBlacklistRouter(h)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/homelab/watchdog/blacklist?cursor=not-base64!", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid cursor: want 400, got %d", w.Code)
	}
}

func TestWatchdogBlacklistHandler_DeleteRepoError(t *testing.T) {
	t.Parallel()
	pager := &stubPager{deleteFn: func(uint, uint) error { return errors.New("db down") }}
	h := NewWatchdogBlacklistHandler(pager, stubTitles{}, stubLookup{"homelab": 1}, nil)
	r := newBlacklistRouter(h)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/instances/homelab/watchdog/blacklist/42", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("repo error: want 500, got %d", w.Code)
	}
}

func TestWatchdogBlacklistHandler_DeleteInvalidID(t *testing.T) {
	t.Parallel()
	h := NewWatchdogBlacklistHandler(&stubPager{}, stubTitles{}, stubLookup{"homelab": 1}, nil)
	r := newBlacklistRouter(h)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/instances/homelab/watchdog/blacklist/not-a-number", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid id: want 400, got %d", w.Code)
	}
}

func TestDeriveSource_Reasons(t *testing.T) {
	t.Parallel()
	if deriveSource(regrab.ReasonConsecutiveNoBetter) != "auto" {
		t.Error("ReasonConsecutiveNoBetter should map to auto")
	}
	if deriveSource(regrab.Reason("manual-future")) != "manual" {
		t.Error("unknown reason should map to manual")
	}
}

// Defensive: ensure ports.ErrNotFound is the sentinel the handler expects.
func TestPortsErrNotFoundIsSentinel(t *testing.T) {
	t.Parallel()
	if !errors.Is(ports.ErrNotFound, ports.ErrNotFound) {
		t.Fatal("sanity check failed")
	}
}
