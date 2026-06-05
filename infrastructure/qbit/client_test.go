package qbit

import (
	"context"
	encjson "encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/domain"
)

// fakeQbit emulates the qBit Web API surface this wrapper exercises:
// /api/v2/auth/login, /api/v2/torrents/info, /api/v2/torrents/trackers.
type fakeQbit struct {
	srv             *httptest.Server
	user            string
	pass            string
	loginCalls      atomic.Int32
	failLogin       atomic.Bool
	ipBanned        atomic.Bool
	torrents        []map[string]any
	trackersByHash  map[string][]map[string]any
	trackerNotFound bool
}

func newFakeQbit(user, pass string) *fakeQbit {
	f := &fakeQbit{user: user, pass: pass, trackersByHash: map[string][]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		f.loginCalls.Add(1)
		if f.ipBanned.Load() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		u := r.Form.Get("username")
		p := r.Form.Get("password")
		if f.failLogin.Load() || u != f.user || p != f.pass {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Fails."))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test-session"})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Ok."))
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		cat := r.URL.Query().Get("category")
		out := make([]map[string]any, 0, len(f.torrents))
		for _, t := range f.torrents {
			if cat != "" && fmt.Sprint(t["category"]) != cat {
				continue
			}
			out = append(out, t)
		}
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, out)
	})
	mux.HandleFunc("/api/v2/torrents/trackers", func(w http.ResponseWriter, r *http.Request) {
		hash := r.URL.Query().Get("hash")
		if f.trackerNotFound {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		trk, ok := f.trackersByHash[hash]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, trk)
	})
	f.srv = httptest.NewServer(mux)
	return f
}

func (f *fakeQbit) close() { f.srv.Close() }

func writeJSON(w http.ResponseWriter, v any) {
	if err := encjson.NewEncoder(w).Encode(v); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func TestNewClient_Validation(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want error
	}{
		{"empty url", Config{}, ErrInvalidConfig},
		{"bad scheme", Config{URL: "ftp://qbit"}, ErrInvalidConfig},
		{"empty host", Config{URL: "http://"}, ErrInvalidConfig},
		{"parse error", Config{URL: "http://%zzz"}, ErrInvalidConfig},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewClient(tc.cfg)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestClient_AnonLogin(t *testing.T) {
	f := newFakeQbit("", "")
	defer f.close()
	c, err := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("anon login should succeed without round-trip, got %v", err)
	}
	if f.loginCalls.Load() != 0 {
		t.Fatalf("anon login should not call upstream, got %d calls", f.loginCalls.Load())
	}
}

func TestClient_BasicLoginSucceeds(t *testing.T) {
	f := newFakeQbit("admin", "secret")
	defer f.close()
	c, err := NewClient(Config{URL: f.srv.URL, Username: "admin", Password: "secret", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("login: %v", err)
	}
	if f.loginCalls.Load() == 0 {
		t.Fatalf("expected at least one login call")
	}
}

func TestClient_LoginBadCredentialsMapsAuth(t *testing.T) {
	f := newFakeQbit("admin", "secret")
	defer f.close()
	c, _ := NewClient(Config{URL: f.srv.URL, Username: "admin", Password: "WRONG", Timeout: 2 * time.Second})
	err := c.Login(context.Background())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, domain.ErrInstanceUnauthorized) {
		t.Fatalf("want ErrInstanceUnauthorized in chain, got %v", err)
	}
}

func TestClient_LoginIPBannedMapsAuth(t *testing.T) {
	f := newFakeQbit("admin", "secret")
	defer f.close()
	f.ipBanned.Store(true)
	c, _ := NewClient(Config{URL: f.srv.URL, Username: "admin", Password: "secret", Timeout: 2 * time.Second})
	err := c.Login(context.Background())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, domain.ErrInstanceUnauthorized) {
		t.Fatalf("want ErrInstanceUnauthorized in chain, got %v", err)
	}
}

func TestClient_LoginNetworkFailureMapsNetwork(t *testing.T) {
	// Point at a server that immediately closes — upstream wraps the
	// transport error which we then join with ErrInstanceNetwork.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()
	c, _ := NewClient(Config{URL: srv.URL, Username: "admin", Password: "secret", Timeout: 2 * time.Second})
	err := c.Login(context.Background())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, domain.ErrInstanceNetwork) {
		t.Fatalf("want ErrInstanceNetwork in chain, got %v", err)
	}
}

func TestClient_ListTorrents_Filtered(t *testing.T) {
	f := newFakeQbit("", "")
	defer f.close()
	f.torrents = []map[string]any{
		{"hash": "AAA", "name": "S01", "category": "sonarr", "state": "uploading", "added_on": int64(1700000000)},
		{"hash": "BBB", "name": "Other", "category": "radarr", "state": "uploading", "added_on": int64(1700000100)},
	}
	c, _ := NewClient(Config{URL: f.srv.URL, Category: "sonarr", Timeout: 2 * time.Second})
	got, err := c.ListTorrents(context.Background())
	if err != nil {
		t.Fatalf("ListTorrents: %v", err)
	}
	if len(got) != 1 || got[0].Hash != "AAA" || got[0].Category != "sonarr" {
		t.Fatalf("unexpected result: %+v", got)
	}
	if got[0].AddedOn.IsZero() {
		t.Fatal("AddedOn should be set")
	}
}

func TestClient_ListTorrents_AllCategories(t *testing.T) {
	f := newFakeQbit("", "")
	defer f.close()
	f.torrents = []map[string]any{
		{"hash": "AAA", "name": "S01", "category": "sonarr", "state": "uploading", "added_on": int64(1700000000)},
		{"hash": "BBB", "name": "Other", "category": "radarr", "state": "uploading", "added_on": int64(1700000100)},
	}
	c, _ := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	got, err := c.ListTorrents(context.Background())
	if err != nil {
		t.Fatalf("ListTorrents: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
}

func TestClient_GetTrackers_OK(t *testing.T) {
	f := newFakeQbit("", "")
	defer f.close()
	f.trackersByHash["AAA"] = []map[string]any{
		{"url": "http://tr1/announce", "status": 2, "msg": "Working"},
		{"url": "http://tr2/announce", "status": 4, "msg": "Torrent not found"},
	}
	c, _ := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	got, err := c.GetTrackers(context.Background(), "AAA")
	if err != nil {
		t.Fatalf("GetTrackers: %v", err)
	}
	if len(got) != 2 || got[0].Status != 2 || got[1].Msg != "Torrent not found" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestClient_GetTrackers_NotFound(t *testing.T) {
	f := newFakeQbit("", "")
	defer f.close()
	c, _ := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	_, err := c.GetTrackers(context.Background(), "missing")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, ErrTorrentNotFound) {
		t.Fatalf("want ErrTorrentNotFound, got %v", err)
	}
}

func TestClient_GetTrackers_EmptyHash(t *testing.T) {
	f := newFakeQbit("", "")
	defer f.close()
	c, _ := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	_, err := c.GetTrackers(context.Background(), "")
	if !errors.Is(err, ErrTorrentNotFound) {
		t.Fatalf("want ErrTorrentNotFound, got %v", err)
	}
}

func TestClient_Close_NoOp(t *testing.T) {
	f := newFakeQbit("", "")
	defer f.close()
	c, _ := NewClient(Config{URL: f.srv.URL, Timeout: 2 * time.Second})
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Login(context.Background()); err == nil {
		t.Fatal("Login after Close should fail")
	}
	if _, err := c.ListTorrents(context.Background()); err == nil {
		t.Fatal("ListTorrents after Close should fail")
	}
	if _, err := c.GetTrackers(context.Background(), "x"); err == nil {
		t.Fatal("GetTrackers after Close should fail")
	}
}
