package qbit

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// writeRecorder captures the hashes form value and served status for each
// qBit write endpoint the wrappers hit.
type writeRecorder struct {
	stopCalls, startCalls, recheckCalls    atomic.Int32
	stopHashes, startHashes, recheckHashes atomic.Value // string
	stopStatus, startStatus, recheckStatus atomic.Int32 // 0 => 200 OK
}

func (rec *writeRecorder) handler(calls *atomic.Int32, captured *atomic.Value, status *atomic.Int32) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		captured.Store(r.Form.Get("hashes"))
		if s := status.Load(); s != 0 {
			w.WriteHeader(int(s))
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// newWriteFake returns an anon fakeQbit with the write endpoints
// (stop/start/recheck) plus the app/webapiVersion probe wired to rec.
// webapiVersion is pinned to 2.11.0 so PauseCtx/ResumeCtx select the
// stop/start endpoint spellings deterministically.
func newWriteFake(t *testing.T) (*fakeQbit, *writeRecorder) {
	t.Helper()
	f := newFakeQbit("", "")
	rec := &writeRecorder{}
	mux := f.srv.Config.Handler.(*http.ServeMux)
	mux.HandleFunc("/api/v2/app/webapiVersion", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("2.11.0"))
	})
	mux.HandleFunc("/api/v2/torrents/stop", rec.handler(&rec.stopCalls, &rec.stopHashes, &rec.stopStatus))
	mux.HandleFunc("/api/v2/torrents/start", rec.handler(&rec.startCalls, &rec.startHashes, &rec.startStatus))
	mux.HandleFunc("/api/v2/torrents/recheck", rec.handler(&rec.recheckCalls, &rec.recheckHashes, &rec.recheckStatus))
	return f, rec
}

func loadString(v *atomic.Value) string {
	if s, ok := v.Load().(string); ok {
		return s
	}
	return ""
}

func TestClient_Pause_OK(t *testing.T) {
	t.Parallel()
	f, rec := newWriteFake(t)
	defer f.close()
	c, err := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Pause(context.Background(), "ABC"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if rec.stopCalls.Load() != 1 {
		t.Fatalf("stop endpoint hit %d times, want 1", rec.stopCalls.Load())
	}
	if got := loadString(&rec.stopHashes); got != "ABC" {
		t.Fatalf("hashes = %q, want ABC", got)
	}
}

func TestClient_Resume_OK(t *testing.T) {
	t.Parallel()
	f, rec := newWriteFake(t)
	defer f.close()
	c, err := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Resume(context.Background(), "ABC"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if rec.startCalls.Load() != 1 {
		t.Fatalf("start endpoint hit %d times, want 1", rec.startCalls.Load())
	}
	if got := loadString(&rec.startHashes); got != "ABC" {
		t.Fatalf("hashes = %q, want ABC", got)
	}
}

func TestClient_Recheck_OK(t *testing.T) {
	t.Parallel()
	f, rec := newWriteFake(t)
	defer f.close()
	c, err := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Recheck(context.Background(), "ABC"); err != nil {
		t.Fatalf("Recheck: %v", err)
	}
	if rec.recheckCalls.Load() != 1 {
		t.Fatalf("recheck endpoint hit %d times, want 1", rec.recheckCalls.Load())
	}
	if got := loadString(&rec.recheckHashes); got != "ABC" {
		t.Fatalf("hashes = %q, want ABC", got)
	}
}

func TestClient_Pause_EmptyHash(t *testing.T) {
	t.Parallel()
	f, rec := newWriteFake(t)
	defer f.close()
	c, _ := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	err := c.Pause(context.Background(), "")
	if !errors.Is(err, ErrTorrentNotFound) {
		t.Fatalf("want ErrTorrentNotFound, got %v", err)
	}
	if rec.stopCalls.Load() != 0 {
		t.Fatalf("empty hash must not reach the wire, got %d calls", rec.stopCalls.Load())
	}
}

func TestClient_Resume_EmptyHash(t *testing.T) {
	t.Parallel()
	f, _ := newWriteFake(t)
	defer f.close()
	c, _ := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	if err := c.Resume(context.Background(), ""); !errors.Is(err, ErrTorrentNotFound) {
		t.Fatalf("want ErrTorrentNotFound, got %v", err)
	}
}

func TestClient_Recheck_EmptyHash(t *testing.T) {
	t.Parallel()
	f, _ := newWriteFake(t)
	defer f.close()
	c, _ := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	if err := c.Recheck(context.Background(), ""); !errors.Is(err, ErrTorrentNotFound) {
		t.Fatalf("want ErrTorrentNotFound, got %v", err)
	}
}

func TestClient_Pause_ServerErrorMapsNetwork(t *testing.T) {
	t.Parallel()
	f, rec := newWriteFake(t)
	defer f.close()
	rec.stopStatus.Store(http.StatusInternalServerError)
	c, _ := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	err := c.Pause(context.Background(), "ABC")
	if err == nil {
		t.Fatal("want error on 500, got nil")
	}
	if !errors.Is(err, sharedErrors.ErrInstanceNetwork) {
		t.Fatalf("want ErrInstanceNetwork in chain, got %v", err)
	}
}

func TestClient_Recheck_ServerErrorMapsNetwork(t *testing.T) {
	t.Parallel()
	f, rec := newWriteFake(t)
	defer f.close()
	rec.recheckStatus.Store(http.StatusInternalServerError)
	c, _ := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	err := c.Recheck(context.Background(), "ABC")
	if err == nil {
		t.Fatal("want error on 500, got nil")
	}
	if !errors.Is(err, sharedErrors.ErrInstanceNetwork) {
		t.Fatalf("want ErrInstanceNetwork in chain, got %v", err)
	}
}

func TestClient_Pause_AfterCloseFails(t *testing.T) {
	t.Parallel()
	f, rec := newWriteFake(t)
	defer f.close()
	c, _ := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Pause(context.Background(), "ABC"); err == nil {
		t.Fatal("Pause after Close should fail")
	}
	if err := c.Resume(context.Background(), "ABC"); err == nil {
		t.Fatal("Resume after Close should fail")
	}
	if err := c.Recheck(context.Background(), "ABC"); err == nil {
		t.Fatal("Recheck after Close should fail")
	}
	if rec.stopCalls.Load() != 0 || rec.startCalls.Load() != 0 || rec.recheckCalls.Load() != 0 {
		t.Fatal("closed client must not reach the wire")
	}
}
