package rest

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

	appext "github.com/alexmorbo/seasonfill/internal/enrichment/app/externalservices"
	infra "github.com/alexmorbo/seasonfill/internal/shared/clients/externalservices"
	apports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
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

type fakeExtTester struct {
	outcome infra.Outcome
	msg     string
}

func (f fakeExtTester) Test(context.Context, infra.Settings) (infra.Outcome, string, time.Duration) {
	if f.outcome == "" {
		return infra.OutcomeOK, "", 17 * time.Millisecond
	}
	return f.outcome, f.msg, 17 * time.Millisecond
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

// newExtTestRouterWithTester wires a fake tester returning a custom
// outcome — used by the Story 489 (B-17) Upsert-422 test.
func newExtTestRouterWithTester(t *testing.T, tester fakeExtTester) (*gin.Engine, *fakeExtRepo, *appext.UseCase) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := newFakeExtRepo()
	uc := appext.NewUseCase(repo, nil, tester, nil, nil)
	h := NewExternalServicesHandler(uc, nil)
	r := gin.New()
	r.GET("/api/v1/external-services", h.List)
	r.PUT("/api/v1/external-services/:service", h.Upsert)
	r.POST("/api/v1/external-services/:service/test", h.Test)
	return r, repo, uc
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

// Story 489 (B-17): TMDB Upsert with a non-empty key + auth_failed
// probe returns 422 with the external_service_invalid_key slug. The
// REST layer does NOT persist the bad key — the underlying repo is
// untouched.
func TestExternalServicesHandler_Upsert_422OnInvalidKey(t *testing.T) {
	t.Parallel()
	r, repo, _ := newExtTestRouterWithTester(t, fakeExtTester{
		outcome: infra.OutcomeAuthFailed,
		msg:     "401 Invalid API key",
	})
	body, _ := json.Marshal(map[string]any{"enabled": true, "api_key": "bad-key"})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/v1/external-services/tmdb", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "external_service_invalid_key") {
		t.Fatalf("expected error slug in body, got %s", w.Body.String())
	}
	// Body must carry the {error, message} envelope.
	var parsed map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got, _ := parsed["error"].(string); got != "external_service_invalid_key" {
		t.Fatalf("error key: got %q", got)
	}
	if got, _ := parsed["message"].(string); got == "" {
		t.Fatalf("message must be non-empty")
	}
	// Repo unchanged.
	if _, ok := repo.row[infra.ServiceTMDB]; ok {
		t.Fatalf("repo must not persist a rejected key")
	}
}

// Story 489 (B-17): GET /external-services surfaces the new
// last_validation_status field once the use case has stamped it via
// the live 401 hook.
func TestExternalServicesHandler_List_SurfacesValidationFields(t *testing.T) {
	t.Parallel()
	r, _, uc := newExtTestRouterWithTester(t, fakeExtTester{})
	uc.ReportAuthFailure("tmdb", "401 Invalid API key")
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/external-services", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"last_validation_status":"invalid_key"`) {
		t.Fatalf("expected validation status in DTO, got %s", w.Body.String())
	}
}
