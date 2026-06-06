package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaults(t *testing.T) {
	snap := Defaults()
	assert.True(t, snap.Cron.Enabled)
	assert.Equal(t, "0 */6 * * *", snap.Cron.Schedule)
	assert.False(t, snap.Cron.OnStart)
	assert.Equal(t, time.Minute, snap.Cron.Jitter)
	assert.Equal(t, 60*time.Second, snap.Scan.ShutdownGrace)
	assert.Equal(t, 15*time.Minute, snap.Scan.CooldownSweep)
	assert.True(t, snap.DryRun)
	assert.Equal(t, 30, snap.GlobalRateLimit.RPM)
	assert.Equal(t, 10, snap.GlobalRateLimit.Burst)
	assert.Equal(t, 12*time.Hour, snap.Auth.SessionTTL)
	assert.False(t, snap.Auth.SecureCookie)
	assert.Equal(t, []string{"127.0.0.1", "::1"}, snap.Auth.TrustedProxies)
	assert.Empty(t, snap.Instances)
}

func TestApplyInstanceDefaults(t *testing.T) {
	inst := &InstanceSnapshot{}
	ApplyInstanceDefaults(inst)

	assert.Equal(t, 10*time.Second, inst.Timeout)
	assert.Equal(t, 60*time.Second, inst.SearchTimeout)
	assert.Equal(t, "smart", inst.Cooldown.Mode)
	assert.Equal(t, 24*time.Hour, inst.Cooldown.SeriesAfterGrab)
	assert.Equal(t, 72*time.Hour, inst.Cooldown.GUIDAfterFailedGrab)
	assert.Equal(t, 48*time.Hour, inst.Cooldown.GUIDAfterFailedImport)
	assert.Equal(t, 3, inst.Retry.MaxAttempts)
	assert.Equal(t, time.Second, inst.Retry.InitialBackoff)
	assert.Equal(t, 30*time.Second, inst.Retry.MaxBackoff)
	assert.Equal(t, 10, inst.Limits.MaxGrabsPerScan)
	assert.Equal(t, 5*time.Minute, inst.HealthCheck.RecheckAuth)
	assert.Equal(t, time.Minute, inst.HealthCheck.RecheckNetwork)
	assert.Equal(t, "auto", inst.Mode)
}

func TestApplyInstanceDefaults_PreservesExisting(t *testing.T) {
	inst := &InstanceSnapshot{
		Timeout:       20 * time.Second,
		SearchTimeout: 2 * time.Minute,
		Mode:          "manual",
	}
	ApplyInstanceDefaults(inst)

	assert.Equal(t, 20*time.Second, inst.Timeout)
	assert.Equal(t, 2*time.Minute, inst.SearchTimeout)
	assert.Equal(t, "manual", inst.Mode)
}

func TestSortInstances(t *testing.T) {
	instances := []InstanceSnapshot{
		{Name: "Zebra"},
		{Name: "Alpha"},
		{Name: "Beta"},
	}
	SortInstances(instances)

	assert.Equal(t, "Alpha", instances[0].Name)
	assert.Equal(t, "Beta", instances[1].Name)
	assert.Equal(t, "Zebra", instances[2].Name)
}

func ptrStr(s string) *string { return &s }

func TestInstanceSnapshot_UIURL_FallbackToURL(t *testing.T) {
	t.Parallel()
	s := InstanceSnapshot{URL: "http://sonarr:80"}
	assert.Equal(t, "http://sonarr:80", s.UIURL())
}

func TestInstanceSnapshot_UIURL_PrefersPublicURL(t *testing.T) {
	t.Parallel()
	s := InstanceSnapshot{
		URL:       "http://sonarr:80",
		PublicURL: ptrStr("https://s.arr.morbo.dev"),
	}
	assert.Equal(t, "https://s.arr.morbo.dev", s.UIURL())
}

func TestInstanceSnapshot_UIURL_EmptyPublicURLFallsBack(t *testing.T) {
	t.Parallel()
	empty := ""
	s := InstanceSnapshot{URL: "http://sonarr:80", PublicURL: &empty}
	assert.Equal(t, "http://sonarr:80", s.UIURL(),
		"empty *PublicURL is treated as unset, not as override")
}

func TestInstanceSnapshot_WebhookBaseURL_FallbackToDerived(t *testing.T) {
	t.Parallel()
	s := InstanceSnapshot{}
	assert.Equal(t, "https://app.example.com", s.WebhookBaseURL("https://app.example.com"))
}

func TestInstanceSnapshot_WebhookBaseURL_PrefersOverride(t *testing.T) {
	t.Parallel()
	s := InstanceSnapshot{
		WebhookURLOverride: ptrStr("http://seasonfill.servarr.svc:8080"),
	}
	assert.Equal(t, "http://seasonfill.servarr.svc:8080", s.WebhookBaseURL("https://app.example.com"))
}

func TestInstanceSnapshot_WebhookBaseURL_EmptyOverrideFallsBack(t *testing.T) {
	t.Parallel()
	empty := ""
	s := InstanceSnapshot{WebhookURLOverride: &empty}
	assert.Equal(t, "https://derived.example.com", s.WebhookBaseURL("https://derived.example.com"))
}
