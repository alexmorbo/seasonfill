package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Story W19-M — S3 media-store metric helpers. Same registry-inspection
// smoke style as the rest of metrics_test.go: call each helper, then
// assert the Prometheus export carries the expected name + labels.

func TestIncS3Request_RegistersAndIncrements(t *testing.T) {
	IncS3Request("get", "ok")
	IncS3Request("get", "ok")
	IncS3Request("put", "error")
	IncS3Request("stat", "not_found")
	IncS3Request("list", "timeout")
	body := writeAndRead(t)
	assert.Contains(t, body, `seasonfill_s3_requests_total{op="get",outcome="ok"} 2`)
	assert.Contains(t, body, `seasonfill_s3_requests_total{op="put",outcome="error"} 1`)
	assert.Contains(t, body, `seasonfill_s3_requests_total{op="stat",outcome="not_found"} 1`)
	assert.Contains(t, body, `seasonfill_s3_requests_total{op="list",outcome="timeout"} 1`)
}

func TestObserveS3Duration_RegistersHistogram(t *testing.T) {
	ObserveS3Duration("get", 0.010)
	ObserveS3Duration("get", 0.250)
	body := writeAndRead(t)
	assert.Contains(t, body, `seasonfill_s3_request_duration_seconds`)
	assert.Contains(t, body, `op="get"`)
}

func TestIncS3ResponseCode_RegistersAndIncrements(t *testing.T) {
	IncS3ResponseCode("get", 200)
	IncS3ResponseCode("stat", 404)
	IncS3ResponseCode("put", 0)
	body := writeAndRead(t)
	assert.Contains(t, body, `seasonfill_s3_response_code_total{op="get",code="200"} 1`)
	assert.Contains(t, body, `seasonfill_s3_response_code_total{op="stat",code="404"} 1`)
	assert.Contains(t, body, `seasonfill_s3_response_code_total{op="put",code="0"} 1`)
}

func TestAddS3Bytes_RegistersAndIncrements(t *testing.T) {
	AddS3Bytes("get", 1024)
	AddS3Bytes("put", 512)
	body := writeAndRead(t)
	assert.Contains(t, body, `seasonfill_s3_bytes_total{op="get"} 1024`)
	assert.Contains(t, body, `seasonfill_s3_bytes_total{op="put"} 512`)
}

func TestIncDecS3Inflight_RegistersAndTracks(t *testing.T) {
	IncS3Inflight("get")
	IncS3Inflight("get")
	DecS3Inflight("get")
	body := writeAndRead(t)
	assert.Contains(t, body, `seasonfill_s3_inflight{op="get"} 1`)
}

func TestS3MetricConstants_NotEmpty(t *testing.T) {
	t.Parallel()
	for _, c := range []string{
		MetricS3RequestsTotal,
		MetricS3RequestDuration,
		MetricS3ResponseCodeTotal,
		MetricS3BytesTotal,
		MetricS3Inflight,
	} {
		require.NotEmpty(t, c)
		assert.Contains(t, c, "seasonfill_s3_")
	}
}
