package mediastore

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/alexmorbo/seasonfill/internal/observability"
)

// Fixed op labels — a closed set, embedded verbatim in metric names.
const (
	opGet    = "get"
	opPut    = "put"
	opStat   = "stat"
	opDelete = "delete"
	opList   = "list"
)

// meteredStore wraps a Store and emits per-operation metrics (request
// outcome, latency, HTTP status, bytes, in-flight) for every call while
// delegating unchanged to the inner backend. It is transparent to all
// callers — the returned readers, infos, and errors are passed through
// untouched.
type meteredStore struct {
	inner Store
}

// NewMeteredStore decorates inner with metric instrumentation. Used by
// New to wrap the s3 and fs backends; the null store is left unwrapped
// (its ErrNotSupported no-ops carry no operational signal).
func NewMeteredStore(inner Store) Store {
	return &meteredStore{inner: inner}
}

func (m *meteredStore) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	observability.IncS3Inflight(opGet)
	defer observability.DecS3Inflight(opGet)
	start := time.Now()
	rc, info, err := m.inner.Get(ctx, key)
	var n int64
	if err == nil {
		n = info.Size
	}
	recordS3(opGet, start, err, n)
	return rc, info, err
}

func (m *meteredStore) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	observability.IncS3Inflight(opPut)
	defer observability.DecS3Inflight(opPut)
	start := time.Now()
	err := m.inner.Put(ctx, key, r, size, contentType)
	var n int64
	if err == nil {
		n = size
	}
	recordS3(opPut, start, err, n)
	return err
}

func (m *meteredStore) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	observability.IncS3Inflight(opStat)
	defer observability.DecS3Inflight(opStat)
	start := time.Now()
	info, err := m.inner.Stat(ctx, key)
	recordS3(opStat, start, err, 0)
	return info, err
}

func (m *meteredStore) Delete(ctx context.Context, key string) error {
	observability.IncS3Inflight(opDelete)
	defer observability.DecS3Inflight(opDelete)
	start := time.Now()
	err := m.inner.Delete(ctx, key)
	recordS3(opDelete, start, err, 0)
	return err
}

func (m *meteredStore) List(ctx context.Context, prefix string, fn func(ObjectInfo) error) error {
	observability.IncS3Inflight(opList)
	defer observability.DecS3Inflight(opList)
	start := time.Now()
	err := m.inner.List(ctx, prefix, fn)
	recordS3(opList, start, err, 0)
	return err
}

// recordS3 emits the after-call metrics for one op: latency, request
// outcome, HTTP response code, and (get/put only, when > 0) bytes.
func recordS3(op string, start time.Time, err error, nBytes int64) {
	observability.ObserveS3Duration(op, time.Since(start).Seconds())
	observability.IncS3Request(op, classifyOutcome(err))
	observability.IncS3ResponseCode(op, statusFromErr(err))
	if nBytes > 0 && (op == opGet || op == opPut) {
		observability.AddS3Bytes(op, nBytes)
	}
}

// classifyOutcome maps an inner-store error to a fixed outcome label.
func classifyOutcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		return "timeout"
	default:
		return "error"
	}
}

// statusFromErr extracts the HTTP status from a minio.ErrorResponse in
// the error chain. nil → 200; a chain with no HTTP status (timeout /
// transport / sentinel-wrapped not-found) → 0.
func statusFromErr(err error) int {
	if err == nil {
		return 200
	}
	var me minio.ErrorResponse
	if errors.As(err, &me) {
		return me.StatusCode
	}
	return 0
}
