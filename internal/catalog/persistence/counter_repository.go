package persistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// CounterRepository aggregates grab_records into time buckets.
// SQL dialect divergence is contained here.
type CounterRepository struct {
	db *gorm.DB
}

func NewCounterRepository(db *gorm.DB) *CounterRepository {
	return &CounterRepository{db: db}
}

type bucketRow struct {
	Bucket  string
	Grabs   int
	Imports int
	Fails   int
}

// BucketCounters returns N buckets (24 / 7 / 30) zero-filled in
// ascending time order. One SQL round-trip; rows folded into a
// fixed-size slice keyed by bucket position.
func (r *CounterRepository) BucketCounters(
	ctx context.Context, instance domain.InstanceName, window ports.CounterWindow, now time.Time,
) ([]ports.CounterBucket, error) {
	plan, err := planForWindow(window, now)
	if err != nil {
		return nil, err
	}

	dialect := r.db.Name()
	bucketExpr, err := bucketExpression(dialect, plan.granularity)
	if err != nil {
		return nil, err
	}

	failList := failStatusStrings()

	sqlText := fmt.Sprintf(`
		SELECT %s AS bucket,
			COUNT(*) AS grabs,
			SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS imports,
			SUM(CASE WHEN status IN (?, ?) THEN 1 ELSE 0 END) AS fails
		FROM grab_records
		WHERE instance_name = ?
			AND created_at >= ?
			AND created_at <  ?
		GROUP BY bucket
	`, bucketExpr)

	args := []any{
		string(grab.StatusImported),
		failList[0], failList[1],
		instance,
		plan.start, plan.end,
	}

	var rows []bucketRow
	if err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlText, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("counter bucket query: %w", err)
	}

	return zeroFill(plan, rows)
}

// AvgGrabsLast7Days COUNTs grabs over the 7 days BEFORE today and
// divides by 7. Excludes today so the Dashboard compares today's
// running total to a stable historical baseline.
func (r *CounterRepository) AvgGrabsLast7Days(
	ctx context.Context, instance domain.InstanceName, now time.Time,
) (float64, error) {
	end := startOfUTCDay(now)
	start := end.Add(-7 * 24 * time.Hour)

	var total int64
	if err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Table("grab_records").
		Where("instance_name = ? AND created_at >= ? AND created_at < ?",
			instance, start, end).
		Count(&total).Error; err != nil {
		return 0, fmt.Errorf("counter avg7d query: %w", err)
	}
	return float64(total) / 7.0, nil
}

// bucketPlan captures the time range and granularity for one window.
type bucketPlan struct {
	start, end  time.Time
	granularity time.Duration // time.Hour or 24*time.Hour
	count       int
}

func planForWindow(window ports.CounterWindow, now time.Time) (bucketPlan, error) {
	now = now.UTC()
	switch window {
	case ports.CounterWindow24h:
		end := now.Truncate(time.Hour).Add(time.Hour)
		return bucketPlan{
			start:       end.Add(-24 * time.Hour),
			end:         end,
			granularity: time.Hour,
			count:       24,
		}, nil
	case ports.CounterWindow7d:
		end := startOfUTCDay(now).Add(24 * time.Hour)
		return bucketPlan{
			start:       end.Add(-7 * 24 * time.Hour),
			end:         end,
			granularity: 24 * time.Hour,
			count:       7,
		}, nil
	case ports.CounterWindow30d:
		end := startOfUTCDay(now).Add(24 * time.Hour)
		return bucketPlan{
			start:       end.Add(-30 * 24 * time.Hour),
			end:         end,
			granularity: 24 * time.Hour,
			count:       30,
		}, nil
	default:
		return bucketPlan{}, fmt.Errorf("invalid counter window: %q", window)
	}
}

func startOfUTCDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// bucketExpression returns SQL that truncates created_at to the
// granularity. Both dialects emit the same canonical key string so
// zeroFill can match raw rows by exact string equality.
func bucketExpression(dialect string, granularity time.Duration) (string, error) {
	switch dialect {
	case "sqlite":
		if granularity == time.Hour {
			return "strftime('%Y-%m-%dT%H:00:00Z', created_at)", nil
		}
		return "strftime('%Y-%m-%d', created_at)", nil
	case "postgres":
		if granularity == time.Hour {
			// to_char emits the same canonical hour string SQLite does.
			return "to_char(date_trunc('hour', created_at), 'YYYY-MM-DD\"T\"HH24:00:00\"Z\"')", nil
		}
		return "to_char(date_trunc('day', created_at), 'YYYY-MM-DD')", nil
	default:
		return "", fmt.Errorf("counter repository: unsupported dialect %q", dialect)
	}
}

// failStatusStrings returns [import_failed, grab_failed] in a fixed
// order so the SQL placeholder count stays stable. If domain adds a
// third fail status the SQL builder and this helper must change in
// lockstep — status_groups_test.go catches drift.
func failStatusStrings() [2]string {
	fs := grab.FailStatuses()
	out := [2]string{}
	for i := 0; i < 2 && i < len(fs); i++ {
		out[i] = string(fs[i])
	}
	return out
}

// zeroFill expands raw rows into a fixed-size slice, gap-filling any
// missing bucket. The key format MUST match bucketExpression's output.
func zeroFill(plan bucketPlan, rows []bucketRow) ([]ports.CounterBucket, error) {
	byKey := make(map[string]bucketRow, len(rows))
	for _, r := range rows {
		byKey[r.Bucket] = r
	}
	out := make([]ports.CounterBucket, 0, plan.count)
	for i := 0; i < plan.count; i++ {
		start := plan.start.Add(time.Duration(i) * plan.granularity)
		key := formatBucketKey(start, plan.granularity)
		if row, ok := byKey[key]; ok {
			out = append(out, ports.CounterBucket{
				BucketStart: start,
				Grabs:       row.Grabs,
				Imports:     row.Imports,
				Fails:       row.Fails,
			})
			continue
		}
		out = append(out, ports.CounterBucket{BucketStart: start})
	}
	return out, nil
}

func formatBucketKey(t time.Time, granularity time.Duration) string {
	if granularity == time.Hour {
		return t.UTC().Format("2006-01-02T15:00:00Z")
	}
	return t.UTC().Format("2006-01-02")
}

// Ensure interface compliance at compile time.
var _ ports.CounterRepository = (*CounterRepository)(nil)
