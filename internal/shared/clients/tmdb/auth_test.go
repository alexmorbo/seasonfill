package tmdb

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestDetectAuthFormat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		token string
		want  AuthFormat
	}{
		{"v3_lowercase_hex_32", "80b85503e3cca9aa92f99ab20f473fb1", AuthFormatV3},
		{"v3_uppercase_hex_32", "80B85503E3CCA9AA92F99AB20F473FB1", AuthFormatV3},
		{"v3_mixed_case_hex_32", "80b85503E3CCA9AA92F99AB20F473fb1", AuthFormatV3},
		// 31 chars — one short, must NOT classify as v3.
		{"v3_too_short", "80b85503e3cca9aa92f99ab20f473fb", AuthFormatUnknown},
		// 33 chars — one long, must NOT classify as v3.
		{"v3_too_long", "80b85503e3cca9aa92f99ab20f473fb1a", AuthFormatUnknown},
		// 32 chars but contains 'g' — not hex.
		{"v3_non_hex_32", "80b85503e3cca9aa92f99ab20f473fbg", AuthFormatUnknown},
		// Realistic v4 JWT shape (header.payload.signature, all base64).
		{"v4_jwt_three_segments", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.abc123sig", AuthFormatV4},
		// JWT-shaped but only 2 dots → still 3 segments — valid.
		{"v4_jwt_minimal", "eyJ.payload.sig", AuthFormatV4},
		// Two-segment "JWT" — missing signature — does NOT classify as v4.
		{"v4_jwt_only_two_segments", "eyJ.payload", AuthFormatUnknown},
		// eyJ-prefix but four segments — extra dot → does NOT classify.
		{"v4_jwt_four_segments", "eyJ.a.b.c", AuthFormatUnknown},
		// Empty → unknown (never v4, never v3).
		{"empty", "", AuthFormatUnknown},
		// Gibberish that happens to be 32 chars but with spaces → unknown.
		{"gibberish_with_spaces", "this is not a valid tmdb token ", AuthFormatUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DetectAuthFormat(tc.token)
			if got != tc.want {
				t.Fatalf("DetectAuthFormat(%q) = %s, want %s", tc.token, got, tc.want)
			}
		})
	}
}

func TestAuthFormat_String(t *testing.T) {
	t.Parallel()
	cases := map[AuthFormat]string{
		AuthFormatV3:      "v3",
		AuthFormatV4:      "v4",
		AuthFormatUnknown: "unknown",
	}
	for f, want := range cases {
		if got := f.String(); got != want {
			t.Fatalf("AuthFormat(%d).String() = %q, want %q", f, got, want)
		}
	}
}

func TestApplyAuth_V3_SetsQueryParam_NoHeader(t *testing.T) {
	t.Parallel()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.themoviedb.org/3/tv/333", nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	ApplyAuth(req, "80b85503e3cca9aa92f99ab20f473fb1", AuthFormatV3)

	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("v3 must NOT set Authorization header, got %q", got)
	}
	if got := req.URL.Query().Get("api_key"); got != "80b85503e3cca9aa92f99ab20f473fb1" {
		t.Fatalf("v3 must set api_key query, got %q", got)
	}
}

func TestApplyAuth_V3_PreservesExistingQueryParams(t *testing.T) {
	t.Parallel()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.themoviedb.org/3/tv/333?language=en-US&append_to_response=credits", nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	ApplyAuth(req, "80b85503e3cca9aa92f99ab20f473fb1", AuthFormatV3)

	q := req.URL.Query()
	if q.Get("language") != "en-US" {
		t.Fatalf("v3 must preserve language=, got %q", q.Get("language"))
	}
	if q.Get("append_to_response") != "credits" {
		t.Fatalf("v3 must preserve append_to_response=, got %q", q.Get("append_to_response"))
	}
	if q.Get("api_key") != "80b85503e3cca9aa92f99ab20f473fb1" {
		t.Fatalf("v3 must add api_key=, got %q", q.Get("api_key"))
	}
}

func TestApplyAuth_V4_SetsBearerHeader_NoQueryParam(t *testing.T) {
	t.Parallel()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.themoviedb.org/3/tv/333", nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	const jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.abc123sig"
	ApplyAuth(req, jwt, AuthFormatV4)

	if got := req.Header.Get("Authorization"); got != "Bearer "+jwt {
		t.Fatalf("v4 must set Authorization=Bearer <token>, got %q", got)
	}
	if got := req.URL.Query().Get("api_key"); got != "" {
		t.Fatalf("v4 must NOT set api_key query, got %q", got)
	}
}

func TestApplyAuth_Unknown_FallsBackToBearer(t *testing.T) {
	t.Parallel()
	// AuthFormatUnknown preserves the pre-471 behaviour: Bearer header.
	// This keeps short test tokens (e.g. "tk" in TestClient_BearerAuth)
	// working unchanged.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.themoviedb.org/3/tv/1", nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	ApplyAuth(req, "tk", AuthFormatUnknown)

	if got := req.Header.Get("Authorization"); got != "Bearer tk" {
		t.Fatalf("Unknown must fall back to Bearer, got %q", got)
	}
	if got := req.URL.Query().Get("api_key"); got != "" {
		t.Fatalf("Unknown must NOT set api_key query, got %q", got)
	}
}

func TestApplyAuth_NilOrEmpty_NoOp(t *testing.T) {
	t.Parallel()
	// Defensive: nil request → no panic. Empty token → no header
	// set (the construction-time guard in New() blocks this in prod;
	// tests occasionally construct partial requests so we hold the
	// invariant).
	ApplyAuth(nil, "token", AuthFormatV4) // must not panic
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com", nil)
	ApplyAuth(req, "", AuthFormatV4)
	if h := req.Header.Get("Authorization"); h != "" {
		t.Fatalf("empty token must not set header, got %q", h)
	}
	// Sanity: the URL is otherwise untouched.
	if !strings.HasSuffix(req.URL.String(), "example.com") {
		t.Fatalf("url mutated unexpectedly: %s", req.URL.String())
	}
}
