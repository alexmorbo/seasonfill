package rest_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	grabdomain "github.com/alexmorbo/seasonfill/internal/grab/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
	appta "github.com/alexmorbo/seasonfill/internal/torrentaction/app"
	tarest "github.com/alexmorbo/seasonfill/internal/torrentaction/rest"
)

const hHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

type grabsStub struct {
	rec grabdomain.Record
	err error
}

func (g grabsStub) FindLatestSuccessByHash(context.Context, string) (grabdomain.Record, error) {
	return g.rec, g.err
}

type ctrlStub struct {
	actErr   error
	loginErr error
}

func (c ctrlStub) Login(context.Context) error         { return c.loginErr }
func (c ctrlStub) Pause(context.Context, string) error { return c.actErr }
func (c ctrlStub) Resume(context.Context, string) error {
	return c.actErr
}
func (c ctrlStub) Recheck(context.Context, string) error { return c.actErr }
func (c ctrlStub) Close() error                          { return nil }

type provStub struct{ ctrl appta.TorrentController }

func (p provStub) ClientFor(context.Context, shareddomain.InstanceName) (appta.TorrentController, error) {
	return p.ctrl, nil
}

type auditStub struct{}

func (auditStub) Write(context.Context, appta.AuditRecord) error { return nil }

func newRouter(t *testing.T, grabs appta.Grabs, ctrl appta.TorrentController) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	uc := appta.New(grabs, provStub{ctrl: ctrl}, auditStub{}, nil)
	h := tarest.NewHandler(uc, nil)
	r := gin.New()
	// Mirror the guarded chain: seed the actor context + the error middleware.
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	r.Use(func(c *gin.Context) { c.Set(middleware.UsernameContextKey, "tester"); c.Next() })
	r.Use(middleware.ErrorResponseMiddleware(discard))
	r.POST("/api/v1/instances/:name/torrents/:hash/pause", h.Pause)
	r.POST("/api/v1/instances/:name/torrents/:hash/resume", h.Resume)
	r.POST("/api/v1/instances/:name/torrents/:hash/recheck", h.Recheck)
	return r
}

func post(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func okRecord() grabdomain.Record {
	return grabdomain.Record{ID: uuid.New(), InstanceName: "main"}
}

func TestHandler_Success_200(t *testing.T) {
	r := newRouter(t, grabsStub{rec: okRecord()}, ctrlStub{})
	w := post(t, r, "/api/v1/instances/main/torrents/"+hHash+"/pause")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"ok"`)
}

func TestHandler_ForeignHash_404(t *testing.T) {
	grabs := grabsStub{err: errors.Join(
		&sharedErrors.GrabNotFoundError{ID: "hash:" + hHash}, sharedports.ErrNotFound)}
	r := newRouter(t, grabs, ctrlStub{})
	w := post(t, r, "/api/v1/instances/main/torrents/"+hHash+"/resume")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandler_QbitUnreachable_502(t *testing.T) {
	ctrl := ctrlStub{actErr: errors.Join(errors.New("timeout"), sharedErrors.ErrInstanceNetwork)}
	r := newRouter(t, grabsStub{rec: okRecord()}, ctrl)
	w := post(t, r, "/api/v1/instances/main/torrents/"+hHash+"/recheck")
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

func TestHandler_QbitUnauthorized_502(t *testing.T) {
	ctrl := ctrlStub{loginErr: errors.Join(errors.New("403 forbidden"), sharedErrors.ErrInstanceUnauthorized)}
	r := newRouter(t, grabsStub{rec: okRecord()}, ctrl)
	w := post(t, r, "/api/v1/instances/main/torrents/"+hHash+"/pause")
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

func TestHandler_InvalidHash_400(t *testing.T) {
	r := newRouter(t, grabsStub{rec: okRecord()}, ctrlStub{})
	w := post(t, r, "/api/v1/instances/main/torrents/not-a-hash/pause")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
