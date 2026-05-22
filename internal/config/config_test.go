package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInterpolateEnv_WithDefault(t *testing.T) {
	t.Setenv("FOO", "")
	out := InterpolateEnv([]byte(`level: ${FOO:-debug}`))
	assert.Equal(t, "level: debug", string(out))
}

func TestInterpolateEnv_FromEnv(t *testing.T) {
	t.Setenv("FOO", "info")
	out := InterpolateEnv([]byte(`level: ${FOO:-debug}`))
	assert.Equal(t, "level: info", string(out))
}

func TestInterpolateEnv_MissingNoDefault(t *testing.T) {
	out := InterpolateEnv([]byte(`x: ${UNSET_VAR_XYZ}`))
	assert.Equal(t, "x: ", string(out))
}

func TestLoadFromBytes_Minimal(t *testing.T) {
	t.Setenv("SEASONFILL_API_KEY", "secret")
	t.Setenv("SONARR_URL", "http://sonarr.local")
	t.Setenv("SONARR_KEY", "key123")

	raw := []byte(`
http:
  auth:
    enabled: true
    api_key: ${SEASONFILL_API_KEY}
sonarr_instances:
  - name: main
    url: ${SONARR_URL}
    api_key: ${SONARR_KEY}
`)

	cfg, err := LoadFromBytes(raw)
	require.NoError(t, err)
	assert.Equal(t, "sqlite", cfg.Database.Driver)
	assert.Equal(t, "secret", cfg.HTTP.Auth.APIKey)
	require.Len(t, cfg.SonarrInstances, 1)
	assert.Equal(t, "http://sonarr.local", cfg.SonarrInstances[0].URL)
}

func TestLoadFromBytes_MissingInstance(t *testing.T) {
	raw := []byte(`
http:
  auth:
    enabled: false
`)
	_, err := LoadFromBytes(raw)
	require.Error(t, err)
}

func TestValidate_PostgresWithoutDSN(t *testing.T) {
	cfg := Defaults()
	cfg.Database.Driver = "postgres"
	cfg.HTTP.Auth.Enabled = false
	cfg.SonarrInstances = []SonarrInstance{{Name: "x", URL: "u", APIKey: "k"}}
	err := cfg.Validate()
	assert.ErrorIs(t, err, ErrPostgresDSN)
}

func TestValidate_AuthEnabledNoKey(t *testing.T) {
	cfg := Defaults()
	cfg.HTTP.Auth.Enabled = true
	cfg.HTTP.Auth.APIKey = ""
	cfg.SonarrInstances = []SonarrInstance{{Name: "x", URL: "u", APIKey: "k"}}
	err := cfg.Validate()
	assert.ErrorIs(t, err, ErrAuthKeyRequired)
}

func TestValidate_UnknownDriver(t *testing.T) {
	cfg := Defaults()
	cfg.Database.Driver = "mongo"
	cfg.HTTP.Auth.Enabled = false
	cfg.SonarrInstances = []SonarrInstance{{Name: "x", URL: "u", APIKey: "k"}}
	err := cfg.Validate()
	assert.ErrorIs(t, err, ErrUnknownDriver)
}

func TestApplyInstanceDefaults_HealthCheck_ClampsNegativeIntervals(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		SonarrInstances: []SonarrInstance{
			{
				Name: "neg-auth",
				HealthCheck: HealthCheckConfig{
					RecheckIntervalAuth:    -1 * time.Hour,
					RecheckIntervalNetwork: 30 * time.Second,
				},
			},
			{
				Name: "neg-net",
				HealthCheck: HealthCheckConfig{
					RecheckIntervalAuth:    10 * time.Minute,
					RecheckIntervalNetwork: -1 * time.Second,
				},
			},
		},
	}
	cfg.ApplyInstanceDefaults()
	assert.Equal(t, 5*time.Minute, cfg.SonarrInstances[0].HealthCheck.RecheckIntervalAuth,
		"negative auth interval must clamp to 5m default")
	assert.Equal(t, 30*time.Second, cfg.SonarrInstances[0].HealthCheck.RecheckIntervalNetwork,
		"positive network interval must survive defaulting")
	assert.Equal(t, 10*time.Minute, cfg.SonarrInstances[1].HealthCheck.RecheckIntervalAuth,
		"positive auth interval must survive defaulting")
	assert.Equal(t, time.Minute, cfg.SonarrInstances[1].HealthCheck.RecheckIntervalNetwork,
		"negative network interval must clamp to 1m default")
}

func TestApplyInstanceDefaults_HealthCheck_ZeroBecomesDefaults(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		SonarrInstances: []SonarrInstance{
			{Name: "zero", HealthCheck: HealthCheckConfig{}},
		},
	}
	cfg.ApplyInstanceDefaults()
	assert.Equal(t, 5*time.Minute, cfg.SonarrInstances[0].HealthCheck.RecheckIntervalAuth)
	assert.Equal(t, time.Minute, cfg.SonarrInstances[0].HealthCheck.RecheckIntervalNetwork)
}

func TestApplyInstanceDefaults_GUIDAfterFailedImport_Defaults(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		SonarrInstances: []SonarrInstance{{
			Name: "main", URL: "http://sonarr:8989", APIKey: "k",
		}},
	}
	cfg.ApplyInstanceDefaults()
	require.Equal(t, 48*time.Hour, cfg.SonarrInstances[0].Cooldown.GUIDAfterFailedImport)
}

func TestApplyInstanceDefaults_GUIDAfterFailedImport_KeepsExplicit(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		SonarrInstances: []SonarrInstance{{
			Name: "main", URL: "http://sonarr:8989", APIKey: "k",
			Cooldown: CooldownConfig{GUIDAfterFailedImport: 6 * time.Hour},
		}},
	}
	cfg.ApplyInstanceDefaults()
	require.Equal(t, 6*time.Hour, cfg.SonarrInstances[0].Cooldown.GUIDAfterFailedImport)
}

func TestLoadFromBytes_CookieSecretEnv(t *testing.T) {
	t.Setenv("SEASONFILL_API_KEY", "admin-key")
	t.Setenv("SEASONFILL_AUTH_COOKIE_SECRET", "from-env-32-bytes-long-secret!")
	t.Setenv("SONARR_URL", "http://sonarr.local")
	t.Setenv("SONARR_KEY", "k")

	raw := []byte(`
http:
  auth:
    enabled: true
    api_key: ${SEASONFILL_API_KEY}
    cookie_secret: ${SEASONFILL_AUTH_COOKIE_SECRET}
sonarr_instances:
  - name: main
    url: ${SONARR_URL}
    api_key: ${SONARR_KEY}
`)
	cfg, err := LoadFromBytes(raw)
	require.NoError(t, err)
	assert.Equal(t, "from-env-32-bytes-long-secret!", cfg.HTTP.Auth.CookieSecret)
}

func TestDefaults_CookieSecretEmpty(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	assert.Empty(t, cfg.HTTP.Auth.CookieSecret,
		"empty default triggers the auto-gen fallback in loader.go (Q-2)")
	assert.False(t, cfg.HTTP.Auth.SecureCookie,
		"default SecureCookie=false keeps http://localhost dev working (M1)")
}

func TestLoadFromBytes_SecureCookieEnv(t *testing.T) {
	t.Setenv("SEASONFILL_API_KEY", "admin-key")
	t.Setenv("SEASONFILL_AUTH_COOKIE_SECRET", "s")
	t.Setenv("SEASONFILL_AUTH_SECURE_COOKIE", "true")
	t.Setenv("SONARR_URL", "http://sonarr.local")
	t.Setenv("SONARR_KEY", "k")

	raw := []byte(`
http:
  auth:
    enabled: true
    api_key: ${SEASONFILL_API_KEY}
    cookie_secret: ${SEASONFILL_AUTH_COOKIE_SECRET}
    secure_cookie: ${SEASONFILL_AUTH_SECURE_COOKIE}
sonarr_instances:
  - name: main
    url: ${SONARR_URL}
    api_key: ${SONARR_KEY}
`)
	cfg, err := LoadFromBytes(raw)
	require.NoError(t, err)
	assert.True(t, cfg.HTTP.Auth.SecureCookie,
		"SEASONFILL_AUTH_SECURE_COOKIE=true must flip the field (M1)")
}

func TestValidate_AuthEnabledRequiresCookieSecret(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.HTTP.Auth.Enabled = true
	cfg.HTTP.Auth.APIKey = "k"
	cfg.HTTP.Auth.CookieSecret = ""
	cfg.SonarrInstances = []SonarrInstance{{Name: "m", URL: "http://x", APIKey: "k"}}
	err := cfg.Validate()
	require.Error(t, err, "Validate must reject Enabled=true with empty CookieSecret (M2)")
	assert.Contains(t, err.Error(), "cookie_secret")
}

func TestValidate_AuthEnabledWithCookieSecret_OK(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.HTTP.Auth.Enabled = true
	cfg.HTTP.Auth.APIKey = "k"
	cfg.HTTP.Auth.CookieSecret = "some-secret"
	cfg.SonarrInstances = []SonarrInstance{{Name: "m", URL: "http://x", APIKey: "k"}}
	require.NoError(t, cfg.Validate(), "happy path: Enabled=true + non-empty CookieSecret → nil (M2)")
}

func TestDefaults_WebhookEmpty(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	assert.Empty(t, cfg.Webhook.Secret, "empty secret is the documented fallback (Q-1)")
	assert.Empty(t, cfg.Webhook.AllowedInstances, "empty allow-list means accept any (Q-8)")
}

func TestLoadFromBytes_WebhookSection(t *testing.T) {
	t.Setenv("SEASONFILL_API_KEY", "main-key")
	t.Setenv("SONARR_URL", "http://sonarr.local")
	t.Setenv("SONARR_KEY", "k")
	t.Setenv("SEASONFILL_WEBHOOK_SECRET", "hook-secret-123")

	raw := []byte(`
http:
  auth:
    enabled: true
    api_key: ${SEASONFILL_API_KEY}
webhook:
  secret: ${SEASONFILL_WEBHOOK_SECRET}
  allowed_instances: [sonarr-main, sonarr-tv]
sonarr_instances:
  - name: sonarr-main
    url: ${SONARR_URL}
    api_key: ${SONARR_KEY}
`)
	cfg, err := LoadFromBytes(raw)
	require.NoError(t, err)
	assert.Equal(t, "hook-secret-123", cfg.Webhook.Secret)
	assert.Equal(t, []string{"sonarr-main", "sonarr-tv"}, cfg.Webhook.AllowedInstances)
}

func TestValidate_AcceptsEmptyWebhook(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.HTTP.Auth.Enabled = false
	cfg.SonarrInstances = []SonarrInstance{{Name: "x", URL: "u", APIKey: "k"}}
	require.NoError(t, cfg.Validate())
}
