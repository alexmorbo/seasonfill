// Package dto — torrents endpoint DTO (Story 222 / PRD v4 §9
// row 2). The shape is the locked-down frontend contract for the
// series-detail torrents tab. Every field below documents its
// zero-value semantics — nil vs empty vs absent — because the
// frontend polls the route every 3 seconds and the JSON is the
// only contract between the two halves of the feature.
package dto

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeriesTorrentsResponse — the document returned by
// GET /api/v1/instances/:name/series/:id/torrents. The shape
// merges live + persisted state per PRD §9 row 2: each torrent
// row carries the full inventory column set, with `live=false`
// flagging rows pulled from the DB fallback (e.g. qBit
// unreachable, torrent deleted, fresh pod restart before
// Hydrate completes).
//
// `synced_at` is the wall-clock at which the response was
// composed (server-side now()); the frontend feeds it into the
// "synced Xs ago" microcopy. `sync_age_seconds` is the same
// value pre-computed for clients that prefer not to redo the
// math.
type SeriesTorrentsResponse struct {
	// Instance is the Sonarr instance the request hit.
	Instance domain.InstanceName `json:"instance" example:"alpha"`
	// SonarrSeriesID echoes the URL parameter for clients that
	// disambiguate cross-instance state.
	SonarrSeriesID domain.SonarrSeriesID `json:"sonarr_series_id" example:"123"`
	// SeriesID is the resolved canonical series.id, echoed for
	// parity with the composite series-detail document.
	SeriesID domain.SeriesID `json:"series_id" example:"42"`

	// Torrents is the merged list of live + DB-fallback rows,
	// sorted by added_on DESC with synced_at DESC as tiebreaker.
	// Always present (empty slice when no torrents map to the
	// series — UI treats empty as "no torrents yet").
	Torrents []TorrentRow `json:"torrents"`

	// LiveCount is the number of rows where live=true. UI uses
	// it to decide whether the "stale fallback" banner shows.
	LiveCount int `json:"live_count" example:"3"`
	// TotalCount is len(Torrents). Echoed so naive clients
	// can show the row count without iterating.
	TotalCount int `json:"total_count" example:"4"`

	// SyncedAt is the server-side wall-clock at which the
	// response was composed. The frontend feeds it into the
	// "synced Xs ago" microcopy.
	SyncedAt time.Time `json:"synced_at"`
	// SyncAgeSeconds is `now - synced_at` rounded to seconds at
	// the moment of composition. Always 0 in steady state;
	// non-zero only in tests that pin the clock.
	SyncAgeSeconds int `json:"sync_age_seconds" example:"0"`
}

// TorrentRow — one row of the torrents tab. Field grouping
// follows the qBit columns the UI renders (design brief §4 +
// PRD §4.2):
//
//   - identity  (hash, name, category, tags, tracker_host)
//   - volume    (size, total_size, downloaded, uploaded)
//   - live      (dl_speed, up_speed, eta, num_seeds, num_leechs,
//     progress) — ZERO on rows where live=false
//   - seeding   (ratio, popularity, time_active_s, seeding_time_s,
//     last_activity)
//   - timestamps (added_on, completion_on)
//   - state     (state_raw, state_group)
//
// `live` discriminates rows: true when the row was pulled from
// the in-memory store (latest Refresh), false when pulled from
// the qbit_torrents persistence table. UI greys out the live
// cells on `live=false` rows.
type TorrentRow struct {
	// Hash is the normalised v1 infohash (lowercase hex). PK
	// for the row in the store and in qbit_torrents.
	Hash domain.QbitHash `json:"hash" example:"abcdef1234567890abcdef1234567890abcdef12"`
	// Name is the qBit-reported torrent name (release filename
	// in most cases).
	Name string `json:"name"`
	// Category, Tags, TrackerHost, SavePath, ContentPath —
	// optional descriptors carried verbatim from qBit / DB.
	Category    *string `json:"category,omitempty"`
	Tags        *string `json:"tags,omitempty"`
	TrackerHost *string `json:"tracker_host,omitempty" example:"tracker.example.com"`
	// SeasonNumber is the season parsed from the torrent name
	// by infrastructure/qbit.ParseSeason. Nil/absent when no
	// SxxExx pattern matched (pack torrents, multi-season
	// compilations, malformed names). Single-season releases
	// surface the integer for the UI's "S05" chip.
	SeasonNumber *int    `json:"season_number,omitempty" example:"5"`
	SavePath     *string `json:"save_path,omitempty"`
	ContentPath  *string `json:"content_path,omitempty"`
	// Provenance is the movie bridge's record of HOW the download came to
	// exist: `radarr_search` (Radarr searched an indexer and grabbed) or
	// `manual_import` (the file was dropped in and Radarr imported it).
	// MOVIE ROWS ONLY — torrent_series_map has no provenance column, so this
	// field is absent from every /series/:id/torrents payload (omitempty).
	Provenance *string `json:"provenance,omitempty" example:"radarr_search"`

	// StateRaw is the verbatim qBit state token (22 values —
	// see PRD §4.3); UI tooltip uses this.
	StateRaw string `json:"state_raw" example:"uploading"`
	// StateGroup is the 8-bucket projection — UI chip colour.
	// One of: downloading, seeding, stalled, queued, paused,
	// checking, error, unknown.
	StateGroup string `json:"state_group" example:"seeding"`

	// Health is the derived actionability bucket (ADR-0013 Q3′):
	// one of ok | stalled | error, projected from state_group alone.
	// `stalled` = залипло (no progress), `error` = qBit
	// error/missingFiles, everything else → `ok`. A future Q3″
	// follow-up will add `unregistered`.
	Health string `json:"health" example:"ok"`

	// SizeBytes / TotalSize / Downloaded / Uploaded — volume
	// counters. SizeBytes is the SELECTED file subset, TotalSize
	// the full archive (qBit semantics).
	SizeBytes  int64 `json:"size_bytes" example:"4294967296"`
	TotalSize  int64 `json:"total_size_bytes" example:"4294967296"`
	Downloaded int64 `json:"downloaded_bytes" example:"4294967296"`
	Uploaded   int64 `json:"uploaded_bytes" example:"8589934592"`

	// Live cells — ZERO when Live=false (PRD §4.6: live
	// telemetry is NOT persisted, so the DB fallback rows
	// cannot carry it).
	DLSpeed   int64   `json:"dl_speed_bps" example:"0"`
	UPSpeed   int64   `json:"up_speed_bps" example:"4194304"`
	ETA       int64   `json:"eta_seconds" example:"0"`
	NumSeeds  int64   `json:"num_seeds" example:"12"`
	NumLeechs int64   `json:"num_leechs" example:"3"`
	Progress  float64 `json:"progress" example:"1"`

	// Ratio / Popularity / TimeActive / SeedingTime /
	// LastActivity — seeding counters; persisted columns. Are
	// non-zero on both live and DB-fallback rows.
	Ratio        float64    `json:"ratio" example:"2.5"`
	Popularity   float64    `json:"popularity" example:"1.2"`
	TimeActiveS  int64      `json:"time_active_seconds" example:"7200"`
	SeedingTimeS int64      `json:"seeding_time_seconds" example:"3600"`
	LastActivity *time.Time `json:"last_activity,omitempty"`

	// AddedOn / CompletionOn — lifecycle timestamps. AddedOn
	// drives the default sort.
	AddedOn      *time.Time `json:"added_on,omitempty"`
	CompletionOn *time.Time `json:"completion_on,omitempty"`

	// Live discriminator. true → pulled from the in-memory
	// store on the latest Refresh; false → pulled from the
	// qbit_torrents persistence table because the hash is not
	// (currently) in the store.
	Live bool `json:"live" example:"true"`
	// Present mirrors the persistence column. true → row is
	// still in qBit OR was just synced; false → row was marked
	// absent on a previous tick (DB-only deleted-but-known).
	Present bool `json:"present" example:"true"`
	// SyncedAt is the wall-clock of the Refresh that produced
	// this row (for live=true) or the last persisted update
	// (for live=false). Echoed per-row so clients that show
	// per-row freshness do not need to derive it.
	SyncedAt time.Time `json:"synced_at"`
}

// TorrentActionResponse is the 200 body for POST
// /instances/:name/torrents/:hash/{pause,resume,recheck} (ADR-0013 Q2).
type TorrentActionResponse struct {
	Status string `json:"status"` // always "ok" on 200
	Action string `json:"action"` // pause | resume | recheck
	Hash   string `json:"hash"`   // normalised 40-hex
}

// MovieTorrentsResponse — the document returned by
// GET /api/v1/movies/:tmdb_id/torrents (ADR-0023 B1.4). Movie twin of
// SeriesTorrentsResponse with the series identity swapped for the movie one:
// `sonarr_series_id`/`series_id` → `radarr_movie_id`/`movie_id`. Rows carry
// the SAME TorrentRow shape, so the frontend reuses one table component; the
// movie-only `provenance` field appears per row and `season_number` never
// does (movies have no seasons).
//
// `instance` is the RADARR instance resolved from the canonical movie — the
// lex-first instance whose movie_states row is active. Torrent actions must
// be POSTed to that same instance
// (POST /instances/{instance}/torrents/{hash}/{pause,resume,recheck}).
type MovieTorrentsResponse struct {
	// Instance is the Radarr instance the torrents were resolved under.
	Instance domain.InstanceName `json:"instance" example:"radarr-main"`
	// RadarrMovieID is the INSTANCE-LOCAL Radarr movie id — the key
	// torrent_movie_map is written against.
	RadarrMovieID int `json:"radarr_movie_id" example:"123"`
	// MovieID is the resolved canonical movies.id, echoed for parity with
	// SeriesTorrentsResponse.SeriesID.
	MovieID domain.MovieID `json:"movie_id" example:"42"`
	// TMDBID echoes the URL parameter so a client holding only the response
	// can round-trip back to the movie detail document.
	TMDBID int `json:"tmdb_id" example:"550"`

	// Torrents is the merged list of live + DB-fallback rows, sorted by
	// added_on DESC with synced_at DESC as tiebreaker. Always present
	// (empty slice when nothing maps to the movie).
	Torrents []TorrentRow `json:"torrents"`

	// LiveCount is the number of rows where live=true.
	LiveCount int `json:"live_count" example:"1"`
	// TotalCount is len(Torrents).
	TotalCount int `json:"total_count" example:"2"`

	// SyncedAt is the server-side wall-clock at which the response was
	// composed.
	SyncedAt time.Time `json:"synced_at"`
	// SyncAgeSeconds is `now - synced_at` at composition time. Always 0 in
	// steady state; non-zero only in tests that pin the clock.
	SyncAgeSeconds int `json:"sync_age_seconds" example:"0"`
}
