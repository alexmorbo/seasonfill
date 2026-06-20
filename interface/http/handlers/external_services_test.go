package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	appext "github.com/alexmorbo/seasonfill/application/externalservices"
	apports "github.com/alexmorbo/seasonfill/application/ports"
	infra "github.com/alexmorbo/seasonfill/internal/shared/clients/externalservices"
)

type fakeExtRepo struct {
	row map[infra.Service]infra.Settings
}

func newFakeExtRepo() *fakeExtRepo {
	return &fakeExtRepo{row: map[infra.Service]infra.Settings{}}
}

func (r *fakeExtRepo) Get(_ context.Context, svc infra.Service) (infra.Settings, error) {
	s, ok := r.row[svc]
	if !ok {
		return infra.Settings{}, apports.ErrNotFound
	}
	return s, nil
}

func (r *fakeExtRepo) List(_ context.Context) ([]infra.Settings, error) {
	out := []infra.Settings{}
	for _, svc := range infra.AllServices {
		if s, ok := r.row[svc]; ok {
			out = append(out, s)
		} else {
			out = append(out, infra.Settings{Service: svc})
		}
	}
	return out, nil
}

func (r *fakeExtRepo) Upsert(_ context.Context, s infra.Settings) error {
	r.row[s.Service] = s
	return nil
}

func (r *fakeExtRepo) MarkTest(_ context.Context, svc infra.Service, _ time.Time, o infra.Outcome, m string) error {
	s := r.row[svc]
	s.LastTestOutcome = o
	s.LastTestMessage = m
	r.row[svc] = s
	return nil
}

type fakeExtTester struct{}

func (fakeExtTester) Test(context.Context, infra.Settings) (infra.Outcome, string, time.Duration) {
	return infra.OutcomeOK, "", 17 * time.Millisecond
}

func newExtTestRouter(t *testing.T) (*gin.Engine, *fakeExtRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := newFakeExtRepo()
	uc := appext.NewUseCase(repo, nil, fakeExtTester{}, nil, nil)
	h := NewExternalServicesHandler(uc, nil)
	r := gin.New()
	r.GET("/api/v1/external-services", h.List)
	r.PUT("/api/v1/external-services/:service", h.Upsert)
	r.POST("/api/v1/external-services/:service/test", h.Test)
	return r, repo
}

func TestExternalServicesHandler_List_MaskedShape(t *testing.T) {
	t.Parallel()
	r, repo := newExtTestRouter(t)
	repo.row[infra.ServiceTMDB] = infra.Settings{
		Service: infra.ServiceTMDB, APIKey: "supersecret", APIKeyLast4: "cret",
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/external-services", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "supersecret") {
		t.Fatalf("plaintext key leaked: %s", body)
	}
	if !strings.Contains(body, "****cret") {
		t.Fatalf("masked key missing: %s", body)
	}
}

func TestExternalServicesHandler_Upsert_PUTSemantics(t *testing.T) {
	t.Parallel()
	r, repo := newExtTestRouter(t)
	repo.row[infra.ServiceOMDB] = infra.Settings{Service: infra.ServiceOMDB, APIKey: "old", APIKeyLast4: "old"}
	body, _ := json.Marshal(map[string]any{"enabled": true, "proxy_url": ""})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/v1/external-services/omdb", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
	got := repo.row[infra.ServiceOMDB]
	if got.APIKey != "old" {
		t.Fatalf("nil api_key must preserve: %+v", got)
	}
	if got.ProxyURL != "" {
		t.Fatalf("empty proxy_url must clear: %+v", got)
	}
	if !got.Enabled {
		t.Fatalf("enabled must propagate")
	}
}

func TestExternalServicesHandler_Test_PersistsOutcome(t *testing.T) {
	t.Parallel()
	r, repo := newExtTestRouter(t)
	repo.row[infra.ServiceTMDB] = infra.Settings{Service: infra.ServiceTMDB, APIKey: "k"}
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/external-services/tmdb/test", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
	if repo.row[infra.ServiceTMDB].LastTestOutcome != infra.OutcomeOK {
		t.Fatalf("outcome not persisted")
	}
}

func TestExternalServicesHandler_InvalidService(t *testing.T) {
	t.Parallel()
	r, _ := newExtTestRouter(t)
	for _, path := range []string{"/api/v1/external-services/imdb", "/api/v1/external-services/imdb/test"} {
		w := httptest.NewRecorder()
		method := http.MethodPut
		if strings.HasSuffix(path, "/test") {
			method = http.MethodPost
		}
		req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("path %s code %d", path, w.Code)
		}
	}
}
