package rest

import (
	"bytes"
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

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	notifapp "github.com/alexmorbo/seasonfill/internal/notification/app"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

type stubNotifier struct{ err error }

func (s stubNotifier) Send(context.Context, []byte, notifapp.Message) error { return s.err }

// stubAgentUsers resolves the per-user owner for the agents handler (Ф8-U-5).
type stubAgentUsers struct{}

func (stubAgentUsers) Get(context.Context) (admin.User, error) {
	return admin.User{ID: 1, Username: "admin", Role: admin.RoleAdmin}, nil
}
func (stubAgentUsers) GetByUsername(_ context.Context, name string) (admin.User, error) {
	return admin.User{ID: 1, Username: name, Role: admin.RoleAdmin}, nil
}
func (stubAgentUsers) FirstAdminID(context.Context) (int64, error) { return 1, nil }
func (stubAgentUsers) GetByOIDCSubject(context.Context, string) (admin.User, error) {
	return admin.User{}, nil
}
func (stubAgentUsers) GetByJellyfinUserID(context.Context, string) (admin.User, error) {
	return admin.User{}, nil
}
func (stubAgentUsers) Create(context.Context, admin.User) error { return nil }
func (stubAgentUsers) CreateFromOIDC(context.Context, string, string, string) (admin.User, error) {
	return admin.User{}, nil
}
func (stubAgentUsers) CreateFromJellyfin(context.Context, string, string, string) (admin.User, error) {
	return admin.User{}, nil
}
func (stubAgentUsers) UpdatePassword(context.Context, uint, string) error { return nil }
func (stubAgentUsers) UpdateSettings(context.Context, uint, ports.UserSettingsPatch) error {
	return nil
}
func (stubAgentUsers) UpdateLastLoginAt(context.Context, uint, time.Time) error { return nil }

func newRouter(t *testing.T, repo ports.NotificationAgentRepository, n notifapp.Notifier) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cipher, err := crypto.NewNotificationAgentCipher("rest-handler-test-key")
	require.NoError(t, err)
	uc := notifapp.NewAgentsUseCase(repo, cipher, n)
	h := NewAgentsHandler(uc, stubAgentUsers{}, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(middleware.UsernameContextKey, "admin") })
	r.GET("/notification-agents", h.List)
	r.GET("/notification-agents/:id", h.Get)
	r.POST("/notification-agents", h.Create)
	r.PUT("/notification-agents/:id", h.Update)
	r.DELETE("/notification-agents/:id", h.Delete)
	r.POST("/notification-agents/:id/test", h.Test)
	return r
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	var rd *http.Request
	if body != "" {
		rd = httptest.NewRequestWithContext(context.Background(), method, path, bytes.NewBufferString(body))
		rd.Header.Set("Content-Type", "application/json")
	} else {
		rd = httptest.NewRequestWithContext(context.Background(), method, path, nil)
	}
	r.ServeHTTP(w, rd)
	return w
}

func TestHandler_Create_MaskedNoToken(t *testing.T) {
	t.Parallel()
	var stored ports.NotificationAgent
	repo := &ports.NotificationAgentRepositoryMock{
		CreateFunc: func(_ context.Context, _ int64, a ports.NotificationAgent) (int64, error) {
			stored = a
			stored.ID = 1
			return 1, nil
		},
		GetFunc: func(context.Context, int64, int64) (ports.NotificationAgent, error) {
			return stored, nil
		},
	}
	r := newRouter(t, repo, stubNotifier{})
	const token = "SUPER-SECRET-TOKEN"
	w := do(r, http.MethodPost, "/notification-agents",
		`{"name":"tg","url":"telegram://`+token+`@telegram?chats=1","enabled":true}`)

	require.Equal(t, http.StatusCreated, w.Code)
	body := w.Body.String()
	assert.NotContains(t, body, token, "response must not leak the URL token")
	assert.NotContains(t, body, "url")
	var view map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &view))
	assert.Equal(t, true, view["configured"])
	assert.Equal(t, "telegram", view["scheme"])
}

func TestHandler_Create_Validation(t *testing.T) {
	t.Parallel()
	repo := &ports.NotificationAgentRepositoryMock{}
	r := newRouter(t, repo, stubNotifier{})

	// missing url (binding:required) → 400
	w := do(r, http.MethodPost, "/notification-agents", `{"name":"tg"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// unknown event_type → 400 (usecase validation)
	w = do(r, http.MethodPost, "/notification-agents",
		`{"name":"tg","url":"telegram://t@x","event_types":["bogus"]}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_List_Get_Delete(t *testing.T) {
	t.Parallel()
	cipher, err := crypto.NewNotificationAgentCipher("rest-handler-test-key")
	require.NoError(t, err)
	enc, err := cipher.Seal([]byte("discord://T@id"))
	require.NoError(t, err)
	agent := ports.NotificationAgent{ID: 1, Name: "d", Enabled: true, ConfigEncrypted: enc, EventTypes: []string{"grab.failed"}}
	repo := &ports.NotificationAgentRepositoryMock{
		ListByOwnerFunc: func(context.Context, int64) ([]ports.NotificationAgent, error) {
			return []ports.NotificationAgent{agent}, nil
		},
		GetFunc:    func(context.Context, int64, int64) (ports.NotificationAgent, error) { return agent, nil },
		DeleteFunc: func(context.Context, int64, int64) error { return nil },
	}
	r := newRouter(t, repo, stubNotifier{})

	w := do(r, http.MethodGet, "/notification-agents", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "discord://")

	w = do(r, http.MethodGet, "/notification-agents/1", "")
	require.Equal(t, http.StatusOK, w.Code)

	w = do(r, http.MethodDelete, "/notification-agents/1", "")
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestHandler_NotFound(t *testing.T) {
	t.Parallel()
	repo := &ports.NotificationAgentRepositoryMock{
		GetFunc: func(context.Context, int64, int64) (ports.NotificationAgent, error) {
			return ports.NotificationAgent{}, ports.ErrNotFound
		},
	}
	r := newRouter(t, repo, stubNotifier{})
	w := do(r, http.MethodGet, "/notification-agents/42", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandler_Test_SuccessAndFailure(t *testing.T) {
	t.Parallel()
	cipher, err := crypto.NewNotificationAgentCipher("rest-handler-test-key")
	require.NoError(t, err)
	enc, err := cipher.Seal([]byte("telegram://LEAKY-TOKEN@telegram?chats=1"))
	require.NoError(t, err)
	repo := &ports.NotificationAgentRepositoryMock{
		GetFunc: func(context.Context, int64, int64) (ports.NotificationAgent, error) {
			return ports.NotificationAgent{ID: 1, ConfigEncrypted: enc}, nil
		},
	}

	// success → 200 {ok:true}
	r := newRouter(t, repo, stubNotifier{})
	w := do(r, http.MethodPost, "/notification-agents/1/test", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ok":true`)

	// failure → 502 SEND_FAILED, no token in body
	r = newRouter(t, repo, stubNotifier{err: errors.New("telegram://LEAKY-TOKEN@telegram send failed")})
	w = do(r, http.MethodPost, "/notification-agents/1/test", "")
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "SEND_FAILED")
	assert.NotContains(t, w.Body.String(), "LEAKY-TOKEN")
}

func TestHandler_Test_NotFoundForNonOwner(t *testing.T) {
	t.Parallel()
	repo := &ports.NotificationAgentRepositoryMock{
		GetFunc: func(context.Context, int64, int64) (ports.NotificationAgent, error) {
			return ports.NotificationAgent{}, ports.ErrNotFound
		},
	}
	r := newRouter(t, repo, stubNotifier{})
	w := do(r, http.MethodPost, "/notification-agents/7/test", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}
