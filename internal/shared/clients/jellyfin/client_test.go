package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticateByName_Success(t *testing.T) {
	var gotAuth, gotPath, gotMethod string
	var gotBody authRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotMethod = r.Method
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"User":{"Id":"jf-1","Name":"Alice"},"AccessToken":"tok-xyz"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	u, err := c.AuthenticateByName(context.Background(), "alice", "pw")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if u.ID != "jf-1" || u.Name != "Alice" {
		t.Fatalf("got %+v, want {jf-1 Alice}", u)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/Users/AuthenticateByName" {
		t.Errorf("path = %q", gotPath)
	}
	// The seerr#2249 pin — assert the EXACT header value.
	const wantAuth = `MediaBrowser Client="seasonfill", Device="seasonfill", DeviceId="seasonfill", Version="1.0.0", Token=""`
	if gotAuth != wantAuth {
		t.Errorf("Authorization = %q\n           want %q", gotAuth, wantAuth)
	}
	if gotAuth != AuthorizationHeader {
		t.Errorf("Authorization != exported AuthorizationHeader const")
	}
	if gotBody.Username != "alice" || gotBody.Pw != "pw" {
		t.Errorf("body = %+v, want {alice pw}", gotBody)
	}
}

func TestAuthenticateByName_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := New(srv.URL, srv.Client())
	_, err := c.AuthenticateByName(context.Background(), "x", "y")
	if !errors.Is(err, ErrJellyfinAuthFailed) {
		t.Fatalf("err = %v, want ErrJellyfinAuthFailed", err)
	}
}

func TestAuthenticateByName_500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()
	c := New(srv.URL, srv.Client())
	_, err := c.AuthenticateByName(context.Background(), "x", "y")
	if err == nil || errors.Is(err, ErrJellyfinAuthFailed) {
		t.Fatalf("err = %v, want non-nil non-auth error", err)
	}
}

func TestAuthenticateByName_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer srv.Close()
	c := New(srv.URL, srv.Client())
	if _, err := c.AuthenticateByName(context.Background(), "x", "y"); err == nil {
		t.Fatal("want decode error")
	}
}

func TestAuthenticateByName_MissingID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"User":{"Name":"Nameless"},"AccessToken":"t"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, srv.Client())
	if _, err := c.AuthenticateByName(context.Background(), "x", "y"); err == nil {
		t.Fatal("want missing-id error")
	}
}
