package cachewatch

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/VictoriaMetrics/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSizer_Contract tabulates the rules around the sizer callback.
// PRD §6.7 explicitly forbids a default — every caller MUST own its
// byte estimate.
func TestSizer_Contract(t *testing.T) {
	t.Run("nil sizer panics with explanatory message", func(t *testing.T) {
		defer func() {
			r := recover()
			require.NotNil(t, r, "New must panic when sizer is nil")
			msg, ok := r.(string)
			require.True(t, ok, "panic value must be a string")
			assert.Contains(t, msg, "sizer must be non-nil")
			assert.Contains(t, msg, "PRD §6.7")
		}()
		_ = New[string, string](uniqueName(t), 10, 0, nil)
	})

	t.Run("sizer return flows into cache_bytes_estimated", func(t *testing.T) {
		name := uniqueName(t)
		// Fixed-size sizer: every entry costs exactly 1000 bytes.
		c := New[string, string](name, 100, 0, func(k, v string) int { return 1000 })
		t.Cleanup(func() { _ = c.Close() })

		for i := range 5 {
			c.Add(strconv.Itoa(i), "ignored")
		}

		buf := &bytes.Buffer{}
		metrics.WritePrometheus(buf, true)
		body := buf.String()

		assert.Contains(t, body, `cache_bytes_estimated{cache="`+name+`"} 5000`)
	})

	t.Run("sizer return participates in Remove accounting", func(t *testing.T) {
		name := uniqueName(t)
		c := New[string, string](name, 100, 0, func(k, v string) int { return 100 })
		t.Cleanup(func() { _ = c.Close() })

		c.Add("a", "x")
		c.Add("b", "y")
		c.Remove("a")

		buf := &bytes.Buffer{}
		metrics.WritePrometheus(buf, true)
		body := buf.String()

		assert.Contains(t, body, `cache_bytes_estimated{cache="`+name+`"} 100`)
	})
}
