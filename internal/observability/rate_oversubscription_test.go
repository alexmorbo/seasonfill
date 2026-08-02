package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/runtime"
)

func inst(rpm int) runtime.InstanceSnapshot {
	return runtime.InstanceSnapshot{RateLimit: runtime.RateLimitSnapshot{RPM: rpm}}
}

// gaugeValue scrapes MetricSonarrRateOversubscribed from the Prometheus
// exposition and returns its trailing value token ("" if absent).
func gaugeValue(t *testing.T) string {
	t.Helper()
	buf := &bytes.Buffer{}
	WritePrometheus(buf)
	line := findMetricLine(buf.String(), MetricSonarrRateOversubscribed)
	require.NotEmptyf(t, line, "gauge %q missing from /metrics:\n%s",
		MetricSonarrRateOversubscribed, buf.String())
	return strings.TrimSpace(strings.TrimPrefix(line, MetricSonarrRateOversubscribed))
}

func TestCheckRateOversubscription(t *testing.T) {
	cases := []struct {
		name      string
		globalRPM int
		instances []runtime.InstanceSnapshot
		wantWarn  bool
		wantGauge string
	}{
		{
			name:      "finite_global_below_sum_warns",
			globalRPM: 30,
			instances: []runtime.InstanceSnapshot{inst(20), inst(20)}, // Σ=40 > 30
			wantWarn:  true,
			wantGauge: "1",
		},
		{
			name:      "unlimited_global_zero_never_warns",
			globalRPM: 0,
			instances: []runtime.InstanceSnapshot{inst(500), inst(500)}, // Σ=1000, but gated
			wantWarn:  false,
			wantGauge: "0",
		},
		{
			name:      "negative_global_never_warns",
			globalRPM: -1,
			instances: []runtime.InstanceSnapshot{inst(50)},
			wantWarn:  false,
			wantGauge: "0",
		},
		{
			name:      "global_equal_sum_does_not_warn",
			globalRPM: 40,
			instances: []runtime.InstanceSnapshot{inst(20), inst(20)}, // Σ=40, 40<40 false
			wantWarn:  false,
			wantGauge: "0",
		},
		{
			name:      "global_above_sum_does_not_warn",
			globalRPM: 100,
			instances: []runtime.InstanceSnapshot{inst(20), inst(20)}, // Σ=40
			wantWarn:  false,
			wantGauge: "0",
		},
		{
			name:      "no_instances_does_not_warn",
			globalRPM: 30,
			instances: nil, // Σ=0
			wantWarn:  false,
			wantGauge: "0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logBuf,
				&slog.HandlerOptions{Level: slog.LevelDebug}))

			CheckRateOversubscription(context.Background(), logger,
				tc.globalRPM, tc.instances)

			assert.Equal(t, tc.wantGauge, gaugeValue(t), "gauge value")

			warned := strings.Contains(logBuf.String(), "sonarr.rate_limit.oversubscribed")
			assert.Equalf(t, tc.wantWarn, warned,
				"WARN presence mismatch; log=%s", logBuf.String())
			if tc.wantWarn {
				assert.Contains(t, logBuf.String(), `"global_rpm":30`)
				assert.Contains(t, logBuf.String(), `"sum_instance_rpm":40`)
			}
		})
	}
}

// TestCheckRateOversubscription_NilLogger — gate + WARN path must not panic on a
// nil logger (defensive; the setter still updates the gauge).
func TestCheckRateOversubscription_NilLogger(t *testing.T) {
	require.NotPanics(t, func() {
		CheckRateOversubscription(context.Background(), nil, 30,
			[]runtime.InstanceSnapshot{inst(50)}) // would warn, but nil logger
	})
	assert.Equal(t, "1", gaugeValue(t))
}
