// Package jellyfin is the Jellyfin HTTP client for the Ф8-U-3 auth source.
// The surface is intentionally narrow: one client, one POST
// (Users/AuthenticateByName), one response struct + a typed sentinel error.
//
// The client never logs and never persists anything — it validates a
// username+password against a Jellyfin server on every call and returns the
// immutable Jellyfin User.Id + Name. seasonfill mints its own session; the
// Jellyfin AccessToken is decoded but deliberately discarded.
package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultTimeout bounds a single AuthenticateByName round-trip when the
// caller supplies an *http.Client with no Timeout set.
const defaultTimeout = 15 * time.Second

// authHeader is the Jellyfin 10.11 authorization header for an
// unauthenticated AuthenticateByName call. The modern server rejects the
// legacy `X-Emby-Authorization` header — this exact `Authorization:
// MediaBrowser ...` form is required (jellyseerr#2249 / #2361). Token is
// empty (we have no token yet — this call obtains one). PIN: any drift here
// = a real 401 against a live Jellyfin.
const (
	clientName     = "seasonfill"
	clientDevice   = "seasonfill"
	clientDeviceID = "seasonfill"
	clientVersion  = "1.0.0"
)

// AuthorizationHeader is the exact MediaBrowser header value sent on
// AuthenticateByName. Exported so the client_test can assert the FULL string
// (regression pin).
const AuthorizationHeader = `MediaBrowser Client="` + clientName +
	`", Device="` + clientDevice +
	`", DeviceId="` + clientDeviceID +
	`", Version="` + clientVersion +
	`", Token=""`

// ErrJellyfinAuthFailed is returned on HTTP 401 from AuthenticateByName —
// bad username/password. The usecase maps it to a login-failed sentinel.
var ErrJellyfinAuthFailed = errors.New("jellyfin: authentication failed")

// JellyfinUser is the caller-facing identity: the immutable id + display
// name. AccessToken is intentionally omitted — seasonfill never persists it.
type JellyfinUser struct {
	ID   string
	Name string
}

// authResponse is the decode target for the AuthenticateByName payload.
type authResponse struct {
	User struct {
		ID   string `json:"Id"`
		Name string `json:"Name"`
	} `json:"User"`
	AccessToken string `json:"AccessToken"`
}

// authRequest is the JSON body: {"Username": ..., "Pw": ...}.
type authRequest struct {
	Username string `json:"Username"`
	Pw       string `json:"Pw"`
}

// Client is the Jellyfin HTTP client. Constructed per request by the handler
// from the live AuthRuntime base URL (cheap — stores fields only). The
// *http.Client is safe for concurrent use; Client owns no mutable state.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New constructs a Client. baseURL is trimmed of a trailing slash. A nil
// httpClient is replaced with a defaultTimeout client (defensive; the
// handler always passes one). baseURL is assumed non-empty — the handler
// gates on that before constructing the client.
func New(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

// AuthenticateByName POSTs {baseURL}/Users/AuthenticateByName with the
// MediaBrowser Authorization header and {"Username","Pw"} body. Returns the
// Jellyfin identity on 2xx; ErrJellyfinAuthFailed on 401; a wrapped error on
// any other non-2xx; a wrapped network/decode error otherwise.
func (c *Client) AuthenticateByName(ctx context.Context, username, password string) (JellyfinUser, error) {
	body, err := json.Marshal(authRequest{Username: username, Pw: password})
	if err != nil {
		return JellyfinUser{}, fmt.Errorf("jellyfin: marshal request: %w", err)
	}
	url := c.baseURL + "/Users/AuthenticateByName"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return JellyfinUser{}, fmt.Errorf("jellyfin: build request: %w", err)
	}
	req.Header.Set("Authorization", AuthorizationHeader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return JellyfinUser{}, fmt.Errorf("jellyfin: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return JellyfinUser{}, fmt.Errorf("jellyfin: read body: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return JellyfinUser{}, ErrJellyfinAuthFailed
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return JellyfinUser{}, fmt.Errorf("jellyfin: unexpected status %d: %s", resp.StatusCode, string(raw))
	}

	var out authResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return JellyfinUser{}, fmt.Errorf("jellyfin: decode body: %w", err)
	}
	if out.User.ID == "" {
		return JellyfinUser{}, fmt.Errorf("jellyfin: response missing User.Id")
	}
	return JellyfinUser{ID: out.User.ID, Name: out.User.Name}, nil
}
