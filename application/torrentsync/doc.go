// Package torrentsync implements Pillar A's application layer
// (PRD v4 §4.4–§4.7). It owns the per-instance qBit /sync/maindata
// polling loop, the in-memory torrent inventory, and the
// three-grain persist policy that lands rows in
// `qbit_torrents` + `qbit_torrent_events` without thrashing
// sqlite.
//
// Composition:
//   - Store: the in-memory inventory keyed by (instance, hash);
//     a secondary index (instance, seriesID) → []hash is built
//     here for story 221's reconciler.
//   - PersistPolicy: the diff-and-write logic (immediate state
//     change, batched counters, never live).
//   - UseCase: wires Store + PersistPolicy + qbit.SyncSession;
//     restart recovery; pending-set coalescence.
//   - Loop: the per-instance polling goroutine modelled 1:1 on
//     cmd/server/regrab_loop.go's instanceLoop. Atomic
//     interval, wake channel, failure degradation, drain on
//     shutdown.
//
// The package depends only on infrastructure/qbit (the
// SyncSession + TorrentInfo shapes from story 219) — no other
// infra import, no application-layer cycles.
package torrentsync
