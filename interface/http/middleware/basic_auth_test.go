package middleware

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func TestParseBasicHeader_Table(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		header   string
		wantUser string
		wantPass string
		wantOK   bool
	}{
		{"valid_simple", "Basic " + b64("admin:hunter22"), "admin", "hunter22", true},
		{"valid_lowercase_scheme", "basic " + b64("admin:hunter22"), "admin", "hunter22", true},
		{"valid_mixed_case_scheme", "BaSiC " + b64("admin:hunter22"), "admin", "hunter22", true},
		{"valid_empty_password", "Basic " + b64("admin:"), "admin", "", true},
		{"valid_password_with_colon", "Basic " + b64("admin:pa:ss:wd"), "admin", "pa:ss:wd", true},
		{"valid_unicode_password", "Basic " + b64("admin:пароль"), "admin", "пароль", true},
		{"valid_long_user", "Basic " + b64(strings.Repeat("u", 200)+":pw"), strings.Repeat("u", 200), "pw", true},

		{"empty_header", "", "", "", false},
		{"prefix_only", "Basic ", "", "", false},
		{"prefix_only_no_space", "Basic", "", "", false},
		{"wrong_scheme_bearer", "Bearer " + b64("admin:hunter22"), "", "", false},
		{"wrong_scheme_digest", "Digest " + b64("admin:hunter22"), "", "", false},
		{"no_scheme", b64("admin:hunter22"), "", "", false},
		{"malformed_base64", "Basic !!!not_b64!!!", "", "", false},
		{"base64_no_colon", "Basic " + b64("nocolonhere"), "", "", false},
		{"base64_empty", "Basic " + b64(""), "", "", false},
		{"empty_username", "Basic " + b64(":justpassword"), "", "", false},
		{"empty_user_empty_pass", "Basic " + b64(":"), "", "", false},
		{"whitespace_payload", "Basic    ", "", "", false},
		{"short_header_below_prefix_len", "Bas", "", "", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			user, pass, ok := parseBasicHeader(tc.header)
			assert.Equal(t, tc.wantOK, ok, "header=%q", tc.header)
			if tc.wantOK {
				assert.Equal(t, tc.wantUser, user)
				assert.Equal(t, tc.wantPass, pass)
			}
		})
	}
}

func TestParseBasicHeader_FirstColonSplit(t *testing.T) {
	t.Parallel()
	// Username cannot contain a colon per RFC 7617; password can. Split
	// on the FIRST colon so a password like "a:b:c" is preserved intact.
	u, p, ok := parseBasicHeader("Basic " + b64("user:a:b:c"))
	assert.True(t, ok)
	assert.Equal(t, "user", u)
	assert.Equal(t, "a:b:c", p)
}

func TestBasicRealmHeader_Format(t *testing.T) {
	t.Parallel()
	// Verbatim form — frontends rely on this exact value to render a
	// browser popup with the right realm label.
	assert.Equal(t, `Basic realm="Seasonfill"`, basicRealmHeader)
}
