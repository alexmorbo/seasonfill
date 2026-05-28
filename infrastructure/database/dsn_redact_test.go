package database

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/config"
)

func TestRedactDSN(t *testing.T) {
	t.Parallel()

	const secret = "S3cr3tLeakCanary"

	cases := []struct {
		name      string
		dsn       string
		secret    string // password that must be ABSENT from output ("" = no check)
		wantExact string // exact expected output ("" = skip exact check)
	}{
		{
			name:   "url form with password",
			dsn:    "postgres://user:" + secret + "@host:5432/dbname?sslmode=require",
			secret: secret,
		},
		{
			name:   "postgresql scheme with password",
			dsn:    "postgresql://user:" + secret + "@host:5432/dbname",
			secret: secret,
		},
		{
			name:      "url form no password",
			dsn:       "postgres://user@host:5432/dbname?sslmode=require",
			wantExact: "postgres://user@host:5432/dbname?sslmode=require",
		},
		{
			name:      "no userinfo at all",
			dsn:       "postgres://host:5432/dbname",
			wantExact: "postgres://host:5432/dbname",
		},
		{
			name:   "libpq keyword form",
			dsn:    "host=localhost port=5432 user=foo password=" + secret + " dbname=bar sslmode=require",
			secret: secret,
		},
		{
			name:   "libpq quoted password with spaces",
			dsn:    "host=localhost user=foo password='" + secret + " more' dbname=bar",
			secret: secret,
		},
		{
			name:   "malformed url unescaped percent in password",
			dsn:    "postgres://user:" + secret + "%pw@host:5432/dbname",
			secret: secret,
		},
		{
			name:      "empty string",
			dsn:       "",
			wantExact: "",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := redactDSN(tc.dsn)
			if tc.secret != "" {
				assert.NotContains(t, got, tc.secret,
					"redacted DSN must not contain the password")
				assert.Contains(t, got, redactedSecret,
					"redacted DSN should contain the placeholder")
			}
			if tc.wantExact != "" || tc.dsn == "" {
				assert.Equal(t, tc.wantExact, got)
			}
		})
	}
}

func TestOpen_Postgres_ErrorDoesNotLeakPassword(t *testing.T) {
	t.Parallel()

	const sentinel = "S3cr3tLeakCanary"
	// Unescaped '%' makes the URL malformed — the exact 2026-05-25 leak shape.
	dsn := "postgres://canary:" + sentinel + "%bad@127.0.0.1:1/seasonfill?sslmode=disable"

	_, err := Open(config.DatabaseConfig{
		Driver: "postgres",
		Postgres: config.PostgresConfig{
			DSN:             dsn,
			MaxOpenConns:    1,
			MaxIdleConns:    1,
			ConnMaxLifetime: time.Second,
		},
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), sentinel,
		"postgres open error must not contain the password")
}

func TestOpen_Postgres_LibpqErrorDoesNotLeakPassword(t *testing.T) {
	t.Parallel()

	const sentinel = "S3cr3tLeakCanary"
	dsn := "host=127.0.0.1 port=1 user=canary password=" + sentinel + " dbname=seasonfill sslmode=disable"

	_, err := Open(config.DatabaseConfig{
		Driver: "postgres",
		Postgres: config.PostgresConfig{
			DSN:             dsn,
			MaxOpenConns:    1,
			ConnMaxLifetime: time.Second,
		},
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), sentinel,
		"postgres open error must not contain the password")
}

func TestScrubPassword(t *testing.T) {
	t.Parallel()

	text := "dial tcp: bad password 'topsecret' supplied"
	got := scrubPassword(text, "postgres://u:topsecret@h/db")
	assert.NotContains(t, got, "topsecret")
	assert.Contains(t, got, redactedSecret)

	// No password in DSN — text returned unchanged.
	unchanged := scrubPassword("some error", "postgres://h/db")
	assert.Equal(t, "some error", unchanged)
}

func TestRedactDSN_NeverPanics(t *testing.T) {
	t.Parallel()
	inputs := []string{
		"", "://", "postgres://", ":::@@@", "password=", "password",
		"postgres://u:p%%%@h", strings.Repeat("password=x ", 100),
	}
	for _, in := range inputs {
		assert.NotPanics(t, func() { _ = redactDSN(in) })
		assert.NotPanics(t, func() { _ = dsnPassword(in) })
	}
}
