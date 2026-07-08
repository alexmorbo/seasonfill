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

// Story 1099 — bounded S3 in-flight caps. Read (Get/Stat) and write (Put)
// have independent ceilings so a downloader Put flood can never starve
// interactive serve Gets. A cap <=0 passed to NewMeteredStore normalizes to
// these defaults so a zero-value Config (and the tests that build the
// decorator directly) never deadlock on a nil/zero-capacity channel.
const (
	defaultReadInflight  = 24
	defaultWriteInflight = 12
	// readAcquireBudget bounds how long a Get/Stat waits for a read slot
	// before degrading. It sits inside the handler's serve-Get budget, so a
	// serve-budget expiry (getCtx.Done) preempts it.
	readAcquireBudget = 500 * time.Millisecond
)

// errReadInflightTimeout is returned by Get/Stat when the read-inflight
// semaphore cannot be acquired within readAcquireBudget. It is deliberately
// NEITHER ErrNotFound NOR ErrNotSupported (and does not wrap them) so the
// serve handler (Story 1099 Fix B) degrades to the SVG placeholder rather
// than treating it as a lost object (which would trigger a refetch).
var errReadInflightTimeout = errors.New("mediastore: read in-flight limit acquire timeout")

// meteredStore wraps a Store and emits per-operation metrics (request
// outcome, latency, HTTP status, bytes, in-flight) for every call while
// delegating unchanged to the inner backend. Story 1099 adds two buffered
// semaphores (readSem, writeSem) that bound concurrent Get/Stat and Put
// against the shared minio client. It is transparent to all callers — the
// returned readers, infos, and errors are passed through untouched.
type meteredStore struct {
	inner    Store
	readSem  chan struct{}
	writeSem chan struct{}
}

// NewMeteredStore decorates inner with metric instrumentation and bounded
// in-flight semaphores. readCap/writeCap <=0 normalize to the package
// defaults. Used by New to wrap the s3 and fs backends; the null store is
// left unwrapped (its ErrNotSupported no-ops carry no operational signal).
func NewMeteredStore(inner Store, readCap, writeCap int) Store {
	if readCap <= 0 {
		readCap = defaultReadInflight
	}
	if writeCap <= 0 {
		writeCap = defaultWriteInflight
	}
	return &meteredStore{
		inner:    inner,
		readSem:  make(chan struct{}, readCap),
		writeSem: make(chan struct{}, writeCap),
	}
}

// acquireRead takes a read slot with a bounded wait. ctx cancellation (the
// serve budget or a client abort) preempts the wait and is returned verbatim;
// readAcquireBudget expiry returns errReadInflightTimeout and records the
// acquire-timeout metric. Returns nil only when a slot was acquired — the
// caller MUST releaseRead on that (and only that) path.
func (m *meteredStore) acquireRead(ctx context.Context, op string) error {
	select {
	case m.readSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(readAcquireBudget):
		observability.IncS3AcquireTimeout(op)
		return errReadInflightTimeout
	}
}

func (m *meteredStore) releaseRead() { <-m.readSem }

func (m *meteredStore) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	if err := m.acquireRead(ctx, opGet); err != nil {
		return nil, ObjectInfo{}, err
	}
	defer m.releaseRead()
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
	// Write slot: block until free, but honor ctx so a shutting-down
	// downloader never hangs forever on a saturated semaphore.
	select {
	case m.writeSem <- struct{}{}:
		defer func() { <-m.writeSem }()
	case <-ctx.Done():
		return ctx.Err()
	}
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
	if err := m.acquireRead(ctx, opStat); err != nil {
		return ObjectInfo{}, err
	}
	defer m.releaseRead()
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
