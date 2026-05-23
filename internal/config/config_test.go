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

func TestApplyInstanceDefaults_Mode_DefaultsToAuto(t *testing.T) {
	t.Parallel()
	cfg := &Config{SonarrInstances: []SonarrInstance{
		{Name: "a", URL: "u", APIKey: "k"},
		{Name: "b", URL: "u", APIKey: "k", Mode: "manual"},
	}}
	cfg.ApplyInstanceDefaults()
	assert.Equal(t, "auto", cfg.SonarrInstances[0].Mode)
	assert.Equal(t, "manual", cfg.SonarrInstances[1].Mode)
}

func TestValidate_Mode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mode    string
		wantErr bool
	}{
		{name: "auto ok", mode: "auto"},
		{name: "manual ok", mode: "manual"},
		{name: "empty ok (defaulted at load)", mode: ""},
		{name: "unknown rejected", mode: "yolo", wantErr: true},
		{name: "case-sensitive Auto rejected", mode: "Auto", wantErr: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := Defaults()
			cfg.HTTP.Auth.Enabled = false
			cfg.SonarrInstances = []SonarrInstance{{Name: "x", URL: "u", APIKey: "k", Mode: tt.mode}}
			err := cfg.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInstanceMode)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestApplyInstanceDefaults_Timeout_ZeroBecomesDefault(t *testing.T) {
	t.Parallel()
	cfg := &Config{SonarrInstances: []SonarrInstance{{
		Name: "main", URL: "u", APIKey: "k",
	}}}
	cfg.ApplyInstanceDefaults()
	assert.Equal(t, 10*time.Second, cfg.SonarrInstances[0].Timeout,
		"zero Timeout must default to 10s")
}

func TestApplyInstanceDefaults_Timeout_NegativeClampsToDefault(t *testing.T) {
	t.Parallel()
	cfg := &Config{SonarrInstances: []SonarrInstance{{
		Name: "neg", URL: "u", APIKey: "k",
		Timeout: -5 * time.Second,
	}}}
	cfg.ApplyInstanceDefaults()
	assert.Equal(t, 10*time.Second, cfg.SonarrInstances[0].Timeout,
		"negative Timeout must clamp to 10s default")
}

func TestApplyInstanceDefaults_Timeout_PreservesExplicit(t *testing.T) {
	t.Parallel()
	cfg := &Config{SonarrInstances: []SonarrInstance{{
		Name: "main", URL: "u", APIKey: "k",
		Timeout: 25 * time.Second,
	}}}
	cfg.ApplyInstanceDefaults()
	assert.Equal(t, 25*time.Second, cfg.SonarrInstances[0].Timeout,
		"explicit Timeout must survive defaulting")
}

func TestApplyInstanceDefaults_SearchTimeout_DefaultsToSixTimesBase(t *testing.T) {
	t.Parallel()
	cfg := &Config{SonarrInstances: []SonarrInstance{{
		Name: "main", URL: "u", APIKey: "k",
		Timeout: 10 * time.Second,
	}}}
	cfg.ApplyInstanceDefaults()
	assert.Equal(t, 60*time.Second, cfg.SonarrInstances[0].SearchTimeout,
		"unset SearchTimeout must default to Timeout*6")
}

func TestApplyInstanceDefaults_SearchTimeout_TracksCustomBase(t *testing.T) {
	t.Parallel()
	cfg := &Config{SonarrInstances: []SonarrInstance{{
		Name: "main", URL: "u", APIKey: "k",
		Timeout: 20 * time.Second,
	}}}
	cfg.ApplyInstanceDefaults()
	assert.Equal(t, 120*time.Second, cfg.SonarrInstances[0].SearchTimeout,
		"SearchTimeout default scales with Timeout (Timeout*6)")
}

func TestApplyInstanceDefaults_SearchTimeout_PreservesExplicit(t *testing.T) {
	t.Parallel()
	cfg := &Config{SonarrInstances: []SonarrInstance{{
		Name: "main", URL: "u", APIKey: "k",
		Timeout:       10 * time.Second,
		SearchTimeout: 90 * time.Second,
	}}}
	cfg.ApplyInstanceDefaults()
	assert.Equal(t, 90*time.Second, cfg.SonarrInstances[0].SearchTimeout,
		"explicit SearchTimeout must survive defaulting")
}

func TestApplyInstanceDefaults_SearchTimeout_NegativeClampsToDefault(t *testing.T) {
	t.Parallel()
	cfg := &Config{SonarrInstances: []SonarrInstance{{
		Name: "neg", URL: "u", APIKey: "k",
		Timeout:       10 * time.Second,
		SearchTimeout: -1 * time.Second,
	}}}
	cfg.ApplyInstanceDefaults()
	require.Equal(t, 60*time.Second, cfg.SonarrInstances[0].SearchTimeout,
		"negative SearchTimeout must clamp to Timeout*6")
}

func TestApplyInstanceDefaults_TimeoutThenSearch_ZeroBoth(t *testing.T) {
	// When both Timeout and SearchTimeout are zero, defaulter must
	// resolve Timeout first (=10s) and then SearchTimeout (=60s).
	// Guards against an ordering regression where SearchTimeout is
	// defaulted off an as-yet-unset (zero) Timeout, yielding 0.
	t.Parallel()
	cfg := &Config{SonarrInstances: []SonarrInstance{{
		Name: "main", URL: "u", APIKey: "k",
	}}}
	cfg.ApplyInstanceDefaults()
	assert.Equal(t, 10*time.Second, cfg.SonarrInstances[0].Timeout)
	assert.Equal(t, 60*time.Second, cfg.SonarrInstances[0].SearchTimeout)
}

func TestValidate_PasswordMutex(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.HTTP.Auth.APIKey = "k"
	cfg.SonarrInstances = []SonarrInstance{{Name: "a", URL: "http://x", APIKey: "y"}}
	cfg.HTTP.Auth.WebPassword = "plain"
	cfg.HTTP.Auth.WebPasswordHash = "$2a$12$abc"
	err := cfg.Validate()
	require.ErrorIs(t, err, ErrAuthPasswordMutex)
}

func TestDefaults_NewAuthFields(t *testing.T) {
	t.Parallel()
	d := Defaults()
	assert.Equal(t, "admin", d.HTTP.Auth.WebUser)
	assert.Equal(t, 12*time.Hour, d.HTTP.Auth.SessionTTL)
	assert.Empty(t, d.HTTP.Auth.WebPassword)
	assert.Empty(t, d.HTTP.Auth.WebPasswordHash)
}
