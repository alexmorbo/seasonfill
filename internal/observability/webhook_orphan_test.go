package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIncWebhookOrphan_RegistersPerPathAndEventType(t *testing.T) {
	IncWebhookOrphan("status", "grabbed")
	IncWebhookOrphan("status", "grabbed")
	IncWebhookOrphan("grab", "downloaded")

	body := writeAndRead(t)
	assert.Contains(t, body, `seasonfill_webhook_orphan_total{path="status",event_type="grabbed"} 2`)
	assert.Contains(t, body, `seasonfill_webhook_orphan_total{path="grab",event_type="downloaded"} 1`)
}

func TestMetricWebhookOrphan_ConstShape(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "seasonfill_webhook_orphan_total", MetricWebhookOrphan)
}
