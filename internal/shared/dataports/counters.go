package dataports

import (
	"context"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// CounterBucket is one bucket row. BucketStart is UTC; granularity is
// 1h for the 24h window and 1d for 7d/30d. Repositories return buckets
// in ascending time order and zero-fill gaps so the SPA renders a
// fixed number of bars without client-side gap-filling.
type CounterBucket struct {
	BucketStart time.Time
	Grabs       int
	Imports     int
	Fails       int
}

// CounterWindow names the supported aggregation windows.
type CounterWindow string

const (
	CounterWindow24h CounterWindow = "24h"
	CounterWindow7d  CounterWindow = "7d"
	CounterWindow30d CounterWindow = "30d"
)

// CounterRepository surfaces aggregations over grab_records. Impls
// isolate SQL dialect differences.
type CounterRepository interface {
	// BucketCounters returns the per-bucket roll-up for one instance.
	// `now` is the exclusive upper bound. The slice has exactly N
	// entries (24 / 7 / 30) — empty buckets are zero-filled.
	BucketCounters(ctx context.Context, instance domain.InstanceName, window CounterWindow, now time.Time) ([]CounterBucket, error)

	// AvgGrabsLast7Days returns SUM/7 over the 7 days BEFORE `now`'s
	// UTC day. Empty days count as zero; divisor is always 7.
	AvgGrabsLast7Days(ctx context.Context, instance domain.InstanceName, now time.Time) (float64, error)
}
