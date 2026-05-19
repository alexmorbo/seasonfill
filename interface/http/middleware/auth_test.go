package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

var ginTestModeOnce sync.Once

func setupAuthRouter(expectedKey string) *gin.Engine {
	ginTestModeOnce.Do(func() { gin.SetMode(gin.TestMode) })
	r := gin.New()
	api := r.Group("/api")
	api.Use(APIKeyAuth(expectedKey))
	api.GET("/ping", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

func TestAPIKeyAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		expectedKey string
		sentKey     string
		wantStatus  int
	}{
		{name: "valid key passes", expectedKey: "secret123", sentKey: "secret123", wantStatus: http.StatusOK},
		{name: "wrong key rejected", expectedKey: "secret123", sentKey: "wrong", wantStatus: http.StatusUnauthorized},
		{name: "missing key rejected", expectedKey: "secret123", sentKey: "", wantStatus: http.StatusUnauthorized},
		{name: "empty expected always rejects", expectedKey: "", sentKey: "anything", wantStatus: http.StatusUnauthorized},
		{name: "empty expected with empty sent rejects too", expectedKey: "", sentKey: "", wantStatus: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := setupAuthRouter(tt.expectedKey)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
			if tt.sentKey != "" {
				req.Header.Set("X-Api-Key", tt.sentKey)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}
