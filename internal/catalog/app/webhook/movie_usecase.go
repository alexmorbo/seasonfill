package webhook

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/torrentsync"
	domainwebhook "github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// MovieUseCase processes a Radarr webhook MovieEvent end-to-end: it drives the
// SAME movie_states + movies-canon cache writes the radarr-sync loop drives, via
// the SHARED scan.BuildRadarrMovieCache + scan.PersistRadarrMovieCache helpers
// (F-21), and — on Grab — bridges the qBit info-hash into torrent_movie_map
// (ADR-0023 B1.2). Mirror of the Sonarr webhook UseCase's handleSeriesAdd /
// handleSeriesDelete / handleGrabbed. Errors on the upsert path are WARN-logged
// and swallowed — Radarr retries on non-2xx and the next sync self-heals
// (D-2.5 sidecar rule).
type MovieUseCase struct {
	movies      scan.MovieCanonUpserter // enrichment MovieRepository (COALESCE Upsert)
	states      scan.MovieStateUpserter // THIN UpsertStub adapter (stat-preserving)
	softDeleter movieStateSoftDeleter
	// torrentMovieMap is the ADR-0023 B1.1 write port over torrent_movie_map.
	// Nil-OK: when unwired the B1.2 grab bridge is silently disabled and the
	// B1.3 queue/history reconciler remains the only writer.
	torrentMovieMap torrentsync.MovieMapRepo
	// tx scopes the bridge write. Nil-OK: a nil transactor runs the work
	// closure directly (mirror of UseCase.handleGrabbed's tx-or-direct split).
	tx     ports.Transactor
	logger *slog.Logger
	now    func() time.Time
}

// movieStateSoftDeleter is the narrow soft-delete surface for MovieDelete.
type movieStateSoftDeleter interface {
	SoftDelete(ctx context.Context, instanceName domain.InstanceName, radarrMovieID int) error
}

// MovieDeps groups constructor params.
type MovieDeps struct {
	Movies      scan.MovieCanonUpserter
	States      scan.MovieStateUpserter // pass the THIN UpsertStub adapter
	SoftDeleter movieStateSoftDeleter
	Logger      *slog.Logger
	// TorrentMovieMap is the ADR-0023 B1.2 bridge port. Nil-OK: a nil repo
	// disables the grab→torrent_movie_map write silently (same nil-OK contract
	// as Deps.TorrentSeriesMap / SeriesSyncer / EpisodeStates on the series
	// side), so minimal/test wirings need not supply it.
	TorrentMovieMap torrentsync.MovieMapRepo
	// Tx wraps the bridge write in one transaction. Nil-OK: the write then
	// runs directly on the ambient ctx.
	Tx ports.Transactor
}

func NewMovieUseCase(d MovieDeps) *MovieUseCase {
	lg := d.Logger
	if lg == nil {
		lg = sharedports.DomainLogger(slog.Default(), "webhook")
	}
	return &MovieUseCase{
		movies:          d.Movies,
		states:          d.States,
		softDeleter:     d.SoftDeleter,
		torrentMovieMap: d.TorrentMovieMap,
		tx:              d.Tx,
		logger:          lg,
		now:             func() time.Time { return time.Now().UTC() },
	}
}

// WithClock — tests only.
func (u *MovieUseCase) WithClock(f func() time.Time) *MovieUseCase { u.now = f; return u }

// Process consumes a domain MovieEvent. Returns nil on Unsupported events and
// (idempotent) already-deleted rows; non-nil only on real downstream failures
// so the drainer can retry (mirror of Process's transient-vs-terminal split).
func (u *MovieUseCase) Process(ctx context.Context, evt domainwebhook.MovieEvent) error {
	switch evt.Type {
	case domainwebhook.MovieEventTypeUnsupported:
		u.logger.DebugContext(ctx, "radarr_webhook_event_no_op",
			slog.String("instance", string(evt.InstanceName)),
			slog.String("raw_event_type", evt.RawEventType))
		return nil
	case domainwebhook.MovieEventTypeGrabbed:
		return u.handleMovieGrabbed(ctx, evt)
	case domainwebhook.MovieEventTypeDeleted:
		return u.handleDelete(ctx, evt)
	case domainwebhook.MovieEventTypeUpsert:
		return u.handleUpsert(ctx, evt)
	default:
		u.logger.WarnContext(ctx, "radarr_webhook_event_unknown_type",
			slog.String("instance", string(evt.InstanceName)))
		return nil
	}
}

// handleUpsert normalises the event to ports.RadarrMovie, then drives the SHARED
// F-21 helpers — the exact same cache write the sync loop performs.
func (u *MovieUseCase) handleUpsert(ctx context.Context, evt domainwebhook.MovieEvent) error {
	if evt.RadarrMovieID == 0 {
		u.logger.DebugContext(ctx, "radarr_webhook_upsert_missing_id",
			slog.String("instance", string(evt.InstanceName)))
		return nil
	}
	// Normalise onto the SAME shape the GET /movie sync produces (F-21).
	m := ports.RadarrMovie{
		RadarrMovieID:       evt.RadarrMovieID,
		Title:               evt.Title,
		TitleSlug:           evt.TitleSlug,
		Year:                evt.Year,
		TMDBID:              evt.TMDBID,
		IMDBID:              evt.IMDBID,
		Monitored:           evt.Monitored,
		HasFile:             evt.HasFile,
		MinimumAvailability: evt.MinimumAvailability,
		// SizeOnDiskBytes intentionally 0: the webhook uses the THIN
		// UpsertStub writer which omits size, preserving the sync-written value.
	}
	cache := scan.BuildRadarrMovieCache(evt.InstanceName, m, u.now())
	if _, err := scan.PersistRadarrMovieCache(ctx, u.movies, u.states, cache); err != nil {
		u.logger.WarnContext(ctx, "radarr_webhook_upsert_failed",
			slog.String("instance", string(evt.InstanceName)),
			slog.Int("radarr_movie_id", evt.RadarrMovieID),
			slog.String("error", err.Error()))
		return nil // best-effort sidecar; next sync self-heals
	}
	u.logger.InfoContext(ctx, "radarr_webhook_movie_cached",
		slog.String("instance", string(evt.InstanceName)),
		slog.Int("radarr_movie_id", evt.RadarrMovieID),
		slog.String("raw_event_type", evt.RawEventType))
	return nil
}

// handleMovieGrabbed captures the qBit info-hash from a Radarr OnGrab webhook
// into torrent_movie_map (ADR-0023 B1.2). A grab that came through Radarr's own
// search is definitionally provenance=radarr_search, and the webhook is the
// most authoritative source we have — the repo's first-source-wins ON CONFLICT
// then stops the later B1.3 queue/history reconciler from downgrading the row.
//
// Two deliberate differences from the Sonarr twin (UseCase.handleGrabbed):
//
//  1. There is no grab_records row to match against on the movie side, so the
//     event fields are the only input — no orphan branch, no hash idempotency
//     read (first-source-wins lives in the repo's ON CONFLICT instead).
//  2. It does NOT fall through to handleUpsert. A Radarr Grab payload always
//     reports hasFile=false, and the THIN UpsertStub writer includes has_file
//     in its conflict-update set, so routing an upgrade-grab of an already
//     imported movie through the cache path would flip has_file true→false
//     until the next Download/scan. Membership stays on MovieAdded, on-disk
//     state on Download/MovieFileImported.
//
// Fail-soft throughout: unwired repo, missing movie id, unparseable downloadId
// and DB errors ALL return nil. A 500 here would make Radarr retry a webhook we
// can never map, and the B1.3 reconciler backfills the row from the queue or
// history anyway.
func (u *MovieUseCase) handleMovieGrabbed(ctx context.Context, evt domainwebhook.MovieEvent) error {
	if u.torrentMovieMap == nil {
		u.logger.DebugContext(ctx, "radarr_webhook_grab_map_not_wired",
			slog.String("instance", string(evt.InstanceName)),
			slog.String("raw_event_type", evt.RawEventType))
		return nil
	}
	if evt.RadarrMovieID == 0 {
		u.logger.DebugContext(ctx, "radarr_webhook_grab_missing_id",
			slog.String("instance", string(evt.InstanceName)),
			slog.String("raw_event_type", evt.RawEventType))
		return nil
	}
	parsed := grab.ParseTorrentHash(evt.DownloadID)
	if parsed == nil {
		// Non-qBit download client, empty downloadId, or a malformed hash.
		// Normal and silent — never an error (R-5).
		u.logger.DebugContext(ctx, "radarr_webhook_grab_no_hash",
			slog.String("instance", string(evt.InstanceName)),
			slog.Int("radarr_movie_id", evt.RadarrMovieID),
			slog.String("download_id", evt.DownloadID),
			slog.String("raw_event_type", evt.RawEventType))
		return nil
	}

	// ParseTorrentHash already normalises via domain.NewQbitHash; the explicit
	// ToLower mirrors handleGrabbed and keeps the invariant local to the write.
	hash := strings.ToLower(string(*parsed))
	row := torrentsync.MovieMapRow{
		Instance:      evt.InstanceName,
		Hash:          hash,
		RadarrMovieID: domain.RadarrMovieID(evt.RadarrMovieID),
		Source:        torrentsync.MovieMapSourceWebhook,
		Provenance:    torrentsync.MovieProvenanceRadarrSearch,
		CreatedAt:     u.now(),
	}
	work := func(txCtx context.Context) error {
		if err := u.torrentMovieMap.UpsertTx(txCtx, row); err != nil {
			return fmt.Errorf("upsert torrent_movie_map (webhook): %w", err)
		}
		return nil
	}
	var txErr error
	if u.tx != nil {
		txErr = u.tx.Transaction(ctx, work)
	} else {
		txErr = work(ctx)
	}
	if txErr != nil {
		u.logger.WarnContext(ctx, "radarr_webhook_grab_map_failed",
			slog.String("instance", string(evt.InstanceName)),
			slog.Int("radarr_movie_id", evt.RadarrMovieID),
			slog.String("hash", hash),
			slog.String("error", txErr.Error()))
		return nil // best-effort bridge; B1.3 reconciler backfills
	}
	u.logger.InfoContext(ctx, "radarr_webhook_grab_mapped",
		slog.String("instance", string(evt.InstanceName)),
		slog.Int("radarr_movie_id", evt.RadarrMovieID),
		slog.String("hash", hash),
		slog.String("source", string(torrentsync.MovieMapSourceWebhook)),
		slog.String("provenance", string(torrentsync.MovieProvenanceRadarrSearch)),
		slog.String("raw_event_type", evt.RawEventType))
	return nil
}

// handleDelete soft-deletes the movie_states row (mirror handleSeriesDelete).
func (u *MovieUseCase) handleDelete(ctx context.Context, evt domainwebhook.MovieEvent) error {
	if u.softDeleter == nil || evt.RadarrMovieID == 0 {
		return nil
	}
	if err := u.softDeleter.SoftDelete(ctx, evt.InstanceName, evt.RadarrMovieID); err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return nil // idempotent re-delivery
		}
		u.logger.WarnContext(ctx, "radarr_webhook_delete_failed",
			slog.String("instance", string(evt.InstanceName)),
			slog.Int("radarr_movie_id", evt.RadarrMovieID),
			slog.String("error", err.Error()))
		return nil
	}
	u.logger.InfoContext(ctx, "radarr_webhook_movie_deleted",
		slog.String("instance", string(evt.InstanceName)),
		slog.Int("radarr_movie_id", evt.RadarrMovieID))
	return nil
}
