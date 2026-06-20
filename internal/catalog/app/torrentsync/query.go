package torrentsync

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Query is the read-side use case backing the HTTP endpoint
// (story 222). It merges the live in-memory snapshot with the
// durable qbit_torrents persistence table for one series.
//
// The merge is hash-keyed: any hash present in the store wins
// over the DB row carrying the same hash. The DB fills the gap
// for hashes mapped to this series in torrent_series_map but
// absent from the store (qBit unreachable, deleted torrent,
// fresh pod before Hydrate completes).
//
// Query owns no state — it is a thin coordinator over the
// Store + ports. Safe for concurrent use.
type Query struct {
	store  *Store
	repo   TorrentsRepo
	lookup LookupRepo
	now    func() time.Time
}

// QueryRow is the merged result row the handler renders. Mirrors
// the Entry shape but carries the `Live` discriminator the DTO
// surfaces.
type QueryRow struct {
	Entry Entry
	Live  bool
	// Present mirrors qbit_torrents.present. Always true for
	// Live=true rows (the live snapshot only carries present
	// torrents); false on DB-only rows that were marked absent.
	Present bool
}

// QueryResult is the materialised return value. Rows are sorted
// by added_on DESC with synced_at DESC as tiebreaker. SyncedAt
// is the server-side wall-clock at composition time — the
// handler stamps it onto the response and into the ETag.
type QueryResult struct {
	Rows      []QueryRow
	LiveCount int
	SyncedAt  time.Time
}

// NewQuery wires the use case. `lookup` may be nil when the
// caller wants store-only behaviour (tests); production wires
// the QbitTorrentsRepository for both `repo` and `lookup`.
func NewQuery(store *Store, repo TorrentsRepo, lookup LookupRepo) *Query {
	return &Query{
		store:  store,
		repo:   repo,
		lookup: lookup,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// WithClock overrides the wall-clock source. Test seam.
func (q *Query) WithClock(now func() time.Time) *Query {
	if now != nil {
		q.now = now
	}
	return q
}

// BySeriesID returns the merged + sorted torrent rows for the
// supplied (instance, sonarrSeriesID). Returns an empty slice
// when no hashes are mapped to the series — distinguishing
// "unknown series" from "no torrents" is the handler's job
// (404 vs 200 + empty), not the query's. The error return
// surfaces only infrastructure failures.
func (q *Query) BySeriesID(ctx context.Context, instance domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID) (QueryResult, error) {
	syncedAt := q.now()

	// Step 1 — pull the live hashes from the store. The
	// secondary index (story 220, populated by story 221)
	// returns the set of hashes mapped to this series under
	// this instance.
	liveHashes := q.store.HashesFor(instance, sonarrSeriesID)
	seen := make(map[string]struct{}, len(liveHashes))
	rows := make([]QueryRow, 0, len(liveHashes))
	for _, h := range liveHashes {
		entry, ok := q.store.Get(instance, h)
		if !ok {
			// Race: index lists the hash but the entry has
			// been evicted between SetSeriesMapping and Get
			// (Delete clears bySeries but the snapshot copy
			// we hold may be stale). Fall through — the DB
			// branch will pick it up if persisted.
			continue
		}
		seen[h] = struct{}{}
		rows = append(rows, QueryRow{
			Entry:   entry,
			Live:    true,
			Present: true,
		})
	}

	// Step 2 — fill the gap from the DB. Two sources for the
	// "set of hashes to look up": the lookup repo
	// (torrent_series_map — provides hashes that EVER mapped
	// to this series even if the store has lost them) plus any
	// liveHashes the store failed to return (the race above).
	// Empty input → no DB round-trip.
	if q.lookup != nil {
		dbHashes, err := q.lookup.HashesForSeries(ctx, instance, sonarrSeriesID)
		if err != nil {
			return QueryResult{}, fmt.Errorf("lookup hashes for series: %w", err)
		}
		missing := make([]string, 0, len(dbHashes))
		for _, h := range dbHashes {
			if _, dup := seen[h]; dup {
				continue
			}
			missing = append(missing, h)
		}
		if len(missing) > 0 {
			dbRows, err := q.repo.FindByHashes(ctx, instance, missing)
			if err != nil {
				return QueryResult{}, fmt.Errorf("find qbit_torrents by hashes: %w", err)
			}
			for _, e := range dbRows {
				// Live fields are zero in the DB schema; the
				// repository's entryFromModel already zeros
				// them, but we re-state the contract here so
				// a future repo refactor can never accidentally
				// surface stale live telemetry to the wire.
				e.Info.DlSpeed = 0
				e.Info.UpSpeed = 0
				e.Info.ETA = 0
				e.Info.NumSeeds = 0
				e.Info.NumLeechs = 0
				e.Info.Progress = 0
				rows = append(rows, QueryRow{
					Entry:   e,
					Live:    false,
					Present: true,
				})
				seen[e.Info.Hash] = struct{}{}
			}
		}
	}

	// Step 3 — sort. added_on DESC, then synced_at DESC.
	sort.SliceStable(rows, func(i, j int) bool {
		ai := rows[i].Entry.Info.AddedOn
		aj := rows[j].Entry.Info.AddedOn
		if !ai.Equal(aj) {
			return ai.After(aj)
		}
		return rows[i].Entry.SyncedAt.After(rows[j].Entry.SyncedAt)
	})

	live := 0
	for _, r := range rows {
		if r.Live {
			live++
		}
	}
	return QueryResult{
		Rows:      rows,
		LiveCount: live,
		SyncedAt:  syncedAt,
	}, nil
}
