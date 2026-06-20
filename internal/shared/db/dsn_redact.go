package database

import (
	"net/url"
	"regexp"
	"strings"
)

const redactedSecret = "xxxxx"

// urlPasswordRe strips the password from a userinfo block in a URL-form DSN
// when net/url cannot parse it (e.g. an unescaped '%' in the password). It
// matches "scheme://user:password@" and drops the ":password" part. The
// password is matched non-greedily up to the first '@'.
var urlPasswordRe = regexp.MustCompile(`(://[^:/?#@\s]*):[^@\s]*@`)

// libpqPasswordRe strips the value of a libpq keyword/value "password=" token.
// It handles single-quoted values (which may contain spaces) and bare values
// (terminated by whitespace).
var libpqPasswordRe = regexp.MustCompile(`(?i)(\bpassword\s*=\s*)('[^']*'|[^\s]*)`)

// redactDSN returns dsn with any embedded password replaced by a placeholder.
// It handles both the URL form (postgres://user:pass@host/db) and the libpq
// keyword/value form (host=... password=secret ...). On any parse failure it
// over-redacts rather than risk leaking the secret. A DSN with no password is
// returned unchanged.
func redactDSN(dsn string) string {
	if dsn == "" {
		return dsn
	}

	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		if _, hasPw := u.User.Password(); hasPw {
			u.User = url.UserPassword(u.User.Username(), redactedSecret)
			return u.String()
		}
		// userinfo present but no password — fall through to keyword form.
	}

	// URL-form fallback for inputs url.Parse rejects (the leak case) and for
	// well-formed URLs that round-trip cleanly.
	if strings.Contains(dsn, "://") {
		return urlPasswordRe.ReplaceAllString(dsn, "$1:"+redactedSecret+"@")
	}

	// libpq keyword/value form.
	if libpqPasswordRe.MatchString(dsn) {
		return libpqPasswordRe.ReplaceAllString(dsn, "${1}"+redactedSecret)
	}

	return dsn
}

// scrubPassword removes the bare password (extracted from dsn) from arbitrary
// text such as a driver error message. The driver may echo the password
// without surrounding DSN structure, so redactDSN alone is insufficient.
func scrubPassword(text, dsn string) string {
	pw := dsnPassword(dsn)
	if pw == "" {
		return text
	}
	return strings.ReplaceAll(text, pw, redactedSecret)
}

// dsnPassword extracts the password embedded in dsn, or "" if none is present.
// It is used to scrub the raw password out of underlying driver error text,
// which may surface the bare secret without surrounding DSN structure.
func dsnPassword(dsn string) string {
	if dsn == "" {
		return ""
	}

	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok {
			return pw
		}
	}

	// URL-form fallback: pull the password out of "://user:pass@".
	if strings.Contains(dsn, "://") {
		if m := regexp.MustCompile(`://[^:/?#@\s]*:([^@\s]*)@`).FindStringSubmatch(dsn); m != nil {
			return m[1]
		}
	}

	// libpq keyword/value form.
	if m := libpqPasswordRe.FindStringSubmatch(dsn); m != nil {
		return strings.Trim(m[2], "'")
	}

	return ""
}
