package qbit

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"

	"github.com/alexmorbo/seasonfill/domain"
)

// TorrentInfo is the seasonfill-owned projection of qbt.Torrent. It
// exposes every field PRD §4.2 lists, plus the derived state group
// (see state.go). The autobrr type intentionally never crosses the
// package boundary: A-2's torrentsync use case and the eventual
// HTTP layer only see TorrentInfo.
//
// Hash is the normalised primary key — v1 infohash lowercase hex
// when non-empty, otherwise v2 (see NormaliseHash). InfohashV1 and
// InfohashV2 retain the raw qBit-reported values for diagnostics
// (e.g. detecting cross-seed v2-only torrents in logs); they are
// NOT used for joins.
//
// Timestamps are decoded from qBit's unix-seconds into time.Time UTC;
// zero qBit timestamps map to zero time.Time (consumers test with
// .IsZero()).
type TorrentInfo struct {
	Hash        string // normalised PK
	InfohashV1  string // raw, may be empty on v2-only torrents
	InfohashV2  string // raw, may be empty on v1-only torrents
	Name        string
	Category    string
	Tags        string // raw comma-separated qBit tag string
	Tracker     string // full first-tracker URL
	TrackerHost string // derived host (see ExtractTrackerHost)
	// SeasonNumber is the season parsed from Name by ParseSeason
	// (see season.go). Pointer-nullable: nil means "no SxxExx hit"
	// (pack torrent, malformed name) OR "matches reference multiple
	// distinct seasons" (multi-season compilation). Single-season
	// releases yield the season number. Set once at mapTorrent
	// time; consumers read it through Entry.Info.SeasonNumber.
	SeasonNumber *int
	SavePath     string
	ContentPath  string
	MagnetURI    string
	Private      bool

	StateRaw   string     // verbatim qBit state string
	StateGroup StateGroup // see state.go

	Size              int64
	TotalSize         int64
	Completed         int64
	Downloaded        int64
	Uploaded          int64
	DownloadedSession int64
	UploadedSession   int64
	AmountLeft        int64

	Progress      float64
	DlSpeed       int64
	UpSpeed       int64
	ETA           int64
	NumSeeds      int64
	NumComplete   int64
	NumLeechs     int64
	NumIncomplete int64
	Availability  float64

	Ratio        float64
	Popularity   float64
	TimeActive   time.Duration
	SeedingTime  time.Duration
	SeenComplete time.Time
	LastActivity time.Time
	AddedOn      time.Time
	CompletionOn time.Time

	TrackersCount int64
}

// Snapshot is the immutable view returned by SyncSession.Refresh.
// `Torrents` is keyed by TorrentInfo.Hash. `Removed` lists hashes
// that disappeared since the previous Refresh — empty on the first
// (rid=0) call. `Rid` is the cursor qBit returned; the next Refresh
// continues from it transparently.
type Snapshot struct {
	Rid      int64
	Torrents map[string]TorrentInfo
	Removed  []string
}

// SyncSession owns the merged qBit MainData cursor for a single qBit
// instance. It is NOT safe for concurrent use — the per-instance
// torrentsync loop (story 220) owns one session and calls Refresh
// from a single goroutine.
type SyncSession interface {
	// Refresh calls /sync/maindata with the session's current rid,
	// applies the delta to the cursor, and returns the post-merge
	// snapshot. The first call (rid=0) returns the full inventory
	// and an empty Removed slice; subsequent calls return only the
	// fields that changed plus removed hashes.
	Refresh(ctx context.Context) (Snapshot, error)

	// Rid returns the current cursor. Exposed for diagnostics
	// (metrics, log lines); the loop does not need to thread it.
	Rid() int64
}

type syncSession struct {
	inner *qbt.Client
	main  *qbt.MainData
}

// newSyncSession is the SyncSession constructor exposed through
// Client (see client.go). It performs login if needed and returns a
// session with rid=0 ready for the first Refresh.
func newSyncSession(ctx context.Context, c *client) (*syncSession, error) {
	if c.closed {
		return nil, errors.New("qbit client closed")
	}
	if err := c.Login(ctx); err != nil {
		return nil, fmt.Errorf("qbit sync session: %w", err)
	}
	return &syncSession{
		inner: c.inner,
		main:  &qbt.MainData{},
	}, nil
}

// Refresh implements SyncSession.
//
// Internally we lean on autobrr's MainData.Update which:
//  1. Calls /sync/maindata with rid from the cursor.
//  2. On full_update=true, replaces the cursor wholesale.
//  3. On full_update=false, merges only the fields present in the
//     raw JSON (preserving fields qBit did not touch).
//  4. Applies torrents_removed deletions.
//
// The Removed slice we return is computed from the keyset before/
// after the call rather than reaching into autobrr internals. This
// is O(N) but N is hundreds-of-torrents, runs every 30s — trivial.
func (s *syncSession) Refresh(ctx context.Context) (Snapshot, error) {
	before := make(map[string]struct{}, len(s.main.Torrents))
	for h := range s.main.Torrents {
		before[h] = struct{}{}
	}

	if err := s.main.Update(ctx, s.inner); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Snapshot{}, fmt.Errorf("qbit sync refresh: %w", ctxErr)
		}
		return Snapshot{}, fmt.Errorf("qbit sync refresh: %w", errors.Join(err, domain.ErrInstanceNetwork))
	}

	torrents := make(map[string]TorrentInfo, len(s.main.Torrents))
	for rawKey, t := range s.main.Torrents {
		info := mapTorrent(rawKey, t)
		torrents[info.Hash] = info
	}

	var removed []string
	for h := range before {
		if _, still := s.main.Torrents[h]; !still {
			// Hash here is the qBit map-key (the v1-or-fallback hash
			// that autobrr already lowercases). Normalise once more
			// for consistency with the Snapshot.Torrents keyspace.
			removed = append(removed, NormaliseHash(h, ""))
		}
	}

	return Snapshot{
		Rid:      s.main.Rid,
		Torrents: torrents,
		Removed:  removed,
	}, nil
}

func (s *syncSession) Rid() int64 { return s.main.Rid }

// mapTorrent projects a single qbt.Torrent into seasonfill's
// TorrentInfo, applying hash normalisation and state grouping.
func mapTorrent(mapKey string, t qbt.Torrent) TorrentInfo {
	// NormaliseHash is the single point of truth: v1 wins when
	// non-empty; otherwise we fall through to t.Hash (which autobrr
	// sets to mapKey post-decode) and finally the mapKey itself.
	fallback := t.Hash
	if fallback == "" {
		fallback = mapKey
	}
	hash := NormaliseHash(t.InfohashV1, fallback)
	return TorrentInfo{
		Hash:         hash,
		InfohashV1:   strings.ToLower(t.InfohashV1),
		InfohashV2:   strings.ToLower(t.InfohashV2),
		Name:         t.Name,
		Category:     t.Category,
		Tags:         t.Tags,
		Tracker:      t.Tracker,
		TrackerHost:  ExtractTrackerHost(t.Tracker),
		SeasonNumber: ParseSeason(t.Name),
		SavePath:     t.SavePath,
		ContentPath:  t.ContentPath,
		MagnetURI:    t.MagnetURI,
		Private:      t.Private,

		StateRaw:   string(t.State),
		StateGroup: stateGroup(string(t.State)),

		Size:              t.Size,
		TotalSize:         t.TotalSize,
		Completed:         t.Completed,
		Downloaded:        t.Downloaded,
		Uploaded:          t.Uploaded,
		DownloadedSession: t.DownloadedSession,
		UploadedSession:   t.UploadedSession,
		AmountLeft:        t.AmountLeft,

		Progress:      t.Progress,
		DlSpeed:       t.DlSpeed,
		UpSpeed:       t.UpSpeed,
		ETA:           t.ETA,
		NumSeeds:      t.NumSeeds,
		NumComplete:   t.NumComplete,
		NumLeechs:     t.NumLeechs,
		NumIncomplete: t.NumIncomplete,
		Availability:  t.Availability,

		Ratio:        t.Ratio,
		Popularity:   t.Popularity,
		TimeActive:   time.Duration(t.TimeActive) * time.Second,
		SeedingTime:  time.Duration(t.SeedingTime) * time.Second,
		SeenComplete: unixOrZero(t.SeenComplete),
		LastActivity: unixOrZero(t.LastActivity),
		AddedOn:      unixOrZero(t.AddedOn),
		CompletionOn: unixOrZero(t.CompletionOn),

		TrackersCount: t.TrackersCount,
	}
}

// unixOrZero converts qBit's int64 unix-seconds into time.Time UTC.
// qBit reports 0 (and occasionally -1) for "never"; both map to a
// zero time.Time so consumers can test with .IsZero().
func unixOrZero(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

// ExtractTrackerHost returns the lowercase host portion of a tracker
// announce URL, stripping scheme, port, path, and query. qBit reports
// the *currently-elected* tracker URL in `tracker`; empty when no
// tracker is contacted (e.g. metaDL phase or DHT-only torrents) —
// returns empty string in that case.
//
// Robustness: url.Parse accepts almost anything; if Hostname() is
// empty we fall back to splitting on '/' to handle malformed entries
// the wild qBit corpus occasionally produces.
func ExtractTrackerHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err == nil {
		if h := u.Hostname(); h != "" {
			return strings.ToLower(h)
		}
	}
	// Fallback: strip scheme + everything from the first '/' after host.
	s := raw
	if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
	}
	if idx := strings.IndexAny(s, "/?"); idx >= 0 {
		s = s[:idx]
	}
	if idx := strings.LastIndex(s, ":"); idx >= 0 {
		s = s[:idx]
	}
	return strings.ToLower(s)
}

// NormaliseHash returns the seasonfill canonical torrent hash:
// lowercase hex, preferring v1 when non-empty, falling back to the
// supplied fallback (typically the qBit `hash` field or the
// /sync/maindata map key — autobrr v1.16.0 puts the v1 hash in the
// map key when present, otherwise v2). Lowercases unconditionally.
//
// Background: qBit 5.x with libtorrent 2.x supports BitTorrent v2
// (BEP 52). Cross-seed v2-only torrents legitimately report
// infohash_v1="" and a v2 sha-256 hex in `hash`. PRD §13 risk 6
// requires that the seasonfill PK consistently picks v1 when
// available — otherwise the same release shows up under two PKs
// across instance lifecycles.
//
// The function MUST be the single point of truth for hash
// normalisation: handlers, repositories, the watchdog detector, the
// reconciler — all go through here.
func NormaliseHash(v1, fallback string) string {
	if h := strings.TrimSpace(v1); h != "" {
		return strings.ToLower(h)
	}
	return strings.ToLower(strings.TrimSpace(fallback))
}
