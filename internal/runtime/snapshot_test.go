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
