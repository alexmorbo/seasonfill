package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	grabdomain "github.com/alexmorbo/seasonfill/internal/grab/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// Action is the closed set of torrent mutations Q2 ships. Phase 2 adds
// delete/set-category; keep this frozen so the audit `action` column and
// the route table stay in lockstep.
type Action string

const (
	ActionPause   Action = "pause"
	ActionResume  Action = "resume"
	ActionRecheck Action = "recheck"
)

// Valid reports whether a is one of the three supported actions.
func (a Action) Valid() bool {
	switch a {
	case ActionPause, ActionResume, ActionRecheck:
		return true
	default:
		return false
	}
}

// Grabs is the usecase's view of the grab repository — the hash guard and
// actual-instance resolver in one lookup. Satisfied by
// *grabpersistence.GrabRepository (FindLatestSuccessByHash).
// ports.ErrNotFound (wrapped in GrabNotFoundError) bubbles for a foreign
// hash.
type Grabs interface {
	FindLatestSuccessByHash(ctx context.Context, hash string) (grabdomain.Record, error)
}

// SeriesMapRef is the torrent_series_map projection the Q5 fallback guard
// needs: the instance that owns the hash (SeriesID carried for logging /
// future use, cheap to select).
type SeriesMapRef struct {
	InstanceName shareddomain.InstanceName
	SeriesID     shareddomain.SonarrSeriesID
}

// SeriesMap is the usecase's fallback hash guard (ADR-0013 Q5). Torrents
// seasonfill only OBSERVES have no grab_records row, so the Grabs guard
// 404s every displayed torrent; the displayed set comes from
// torrent_series_map, and FindByHash resolves the owning instance from that
// bridge. ports.ErrNotFound (wrapped in GrabNotFoundError) signals a hash
// absent from the bridge. Satisfied by
// *torrentactionpersistence.SeriesMapRepository.
type SeriesMap interface {
	FindByHash(ctx context.Context, hash string) (SeriesMapRef, error)
}

// MovieMapRef is the torrent_movie_map projection the Q5 fallback guard
// needs: the instance that owns the hash (RadarrMovieID carried for
// logging / future use, cheap to select). Twin of SeriesMapRef.
type MovieMapRef struct {
	InstanceName  shareddomain.InstanceName
	RadarrMovieID shareddomain.RadarrMovieID
}

// MovieMap is the movie-side twin of SeriesMap (ADR-0023 B1.1).
// Movie torrents seasonfill only OBSERVES have no grab_records row, so
// the Grabs guard 404s them; the displayed set comes from
// torrent_movie_map, and FindByHash resolves the owning instance from
// that bridge. ports.ErrNotFound (wrapped in GrabNotFoundError) signals
// a hash absent from the bridge. Satisfied by
// *torrentactionpersistence.MovieMapRepository. Not yet consumed by the
// usecase — B1.6 folds it into the guard chain.
type MovieMap interface {
	FindByHash(ctx context.Context, hash string) (MovieMapRef, error)
}

// TorrentController is the narrow write surface the usecase drives against
// a single qBit instance. qbit.Client satisfies it structurally (it also
// exposes Login/Pause/Resume/Recheck/Close). The usecase owns the client
// lifecycle: it Logs in, acts, and Closes.
type TorrentController interface {
	Login(ctx context.Context) error
	Pause(ctx context.Context, hash string) error
	Resume(ctx context.Context, hash string) error
	Recheck(ctx context.Context, hash string) error
	Close() error
}

// QbitClientProvider builds a per-instance TorrentController. The
// production adapter (wiring/catalog.go) composes the regrab
// SettingsUseCase.Lookup (password-decrypting) with
// infraregrab.QbitClientFactoryFunc. A missing/disabled settings row
// bubbles ports.ErrNotFound (→ 404); a bad URL bubbles the config error.
type QbitClientProvider interface {
	ClientFor(ctx context.Context, instance shareddomain.InstanceName) (TorrentController, error)
}

// AuditRecord is the value the AuditWriter persists. Actor is "" for
// unauthenticated/bypass paths (the handler passes the auth.username
// context value verbatim, which is "api-key" or the session username).
type AuditRecord struct {
	InstanceName shareddomain.InstanceName
	Hash         string
	Action       Action
	Actor        string
	Result       string // "ok" | "error"
	CreatedAt    time.Time
}

// AuditWriter persists one torrent_action_audit row. Write failures are
// best-effort in the usecase — a successful mutation is never rolled back
// because its audit insert failed; the error is logged.
type AuditWriter interface {
	Write(ctx context.Context, rec AuditRecord) error
}

// Input is the usecase request. Instance is the path :name; the usecase
// cross-checks it against the grab record's InstanceName.
type Input struct {
	Instance shareddomain.InstanceName
	Hash     string
	Action   Action
	Actor    string
}

// UseCase implements the Q2 write flow. Construct via New.
type UseCase struct {
	grabs     Grabs
	seriesMap SeriesMap
	provider  QbitClientProvider
	audit     AuditWriter
	now       func() time.Time
	log       *slog.Logger
}

// New wires the usecase. log defaults to slog.Default when nil; now
// defaults to time.Now().UTC (overridable in tests).
func New(grabs Grabs, seriesMap SeriesMap, provider QbitClientProvider, audit AuditWriter, log *slog.Logger) *UseCase {
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "qbit")
	}
	return &UseCase{
		grabs:     grabs,
		seriesMap: seriesMap,
		provider:  provider,
		audit:     audit,
		now:       func() time.Time { return time.Now().UTC() },
		log:       log,
	}
}

// Do runs guard → resolve actual instance → dial → mutate → audit.
//
// Error contract (mapped by the handler):
//   - invalid action              -> fmt error (handler 400 pre-check makes this unreachable)
//   - foreign hash                -> ports.ErrNotFound (via GrabNotFoundError) -> 404
//   - path :name != grab instance -> ports.ErrNotFound (via GrabNotFoundError) -> 404
//   - qBit unreachable            -> ErrInstanceNetwork -> 502
//   - success (incl. idempotent)  -> nil -> 200
func (u *UseCase) Do(ctx context.Context, in Input) error {
	if !in.Action.Valid() {
		return fmt.Errorf("torrentaction: invalid action %q", in.Action)
	}
	hash := strings.ToLower(strings.TrimSpace(in.Hash))

	// Guard + actual-instance resolution. UNION (ADR-0013 Q5): a hash is
	// legitimate if it produced a grab_records row (seasonfill grabbed it)
	// OR it lives in torrent_series_map (seasonfill only observed a
	// Sonarr-driven download). The displayed torrents come from
	// torrent_series_map, so the map fallback is what keeps the action
	// buttons from 404ing on ~100% of the UI.
	var actual shareddomain.InstanceName
	rec, err := u.grabs.FindLatestSuccessByHash(ctx, hash)
	switch {
	case err == nil:
		actual = rec.InstanceName
	case errors.Is(err, ports.ErrNotFound):
		// Grab miss — fall back to the bridge table.
		ref, mapErr := u.seriesMap.FindByHash(ctx, hash)
		if mapErr != nil {
			// Bridge miss returns the same GrabNotFoundError + ErrNotFound
			// shape -> the current 404; a real DB error propagates -> 500.
			return mapErr
		}
		actual = ref.InstanceName
	default:
		// A real grab-repo error (not not-found): propagate, do NOT fall
		// through to the bridge.
		return err
	}
	if actual != in.Instance {
		u.log.WarnContext(ctx, "torrent_action_instance_mismatch",
			slog.String("path_instance", string(in.Instance)),
			slog.String("actual_instance", string(actual)),
			slog.String("hash", hash))
		return errors.Join(
			&sharedErrors.GrabNotFoundError{ID: "hash:" + hash},
			ports.ErrNotFound,
		)
	}

	client, err := u.provider.ClientFor(ctx, actual)
	if err != nil {
		return fmt.Errorf("torrentaction: build client for %q: %w", actual, err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Login(ctx); err != nil {
		u.writeAudit(ctx, in, actual, hash, "error")
		return fmt.Errorf("torrentaction: qbit login %q: %w", actual, err)
	}

	actErr := u.invoke(ctx, client, in.Action, hash)
	result := "ok"
	if actErr != nil {
		result = "error"
	}
	u.writeAudit(ctx, in, actual, hash, result)
	if actErr != nil {
		return fmt.Errorf("torrentaction: %s %s on %q: %w", in.Action, hash, actual, actErr)
	}

	u.log.InfoContext(ctx, "torrent_action_ok",
		slog.String("instance", string(actual)),
		slog.String("hash", hash),
		slog.String("action", string(in.Action)),
		slog.String("actor", in.Actor))
	return nil
}

func (u *UseCase) invoke(ctx context.Context, c TorrentController, a Action, hash string) error {
	switch a {
	case ActionPause:
		return c.Pause(ctx, hash)
	case ActionResume:
		return c.Resume(ctx, hash)
	case ActionRecheck:
		return c.Recheck(ctx, hash)
	default:
		return fmt.Errorf("torrentaction: unreachable action %q", a)
	}
}

// writeAudit persists one row best-effort. A failed audit insert never
// fails the action — it is logged and swallowed. context.WithoutCancel
// lets the row land even when the request ctx was cancelled mid-flight
// (e.g. the action succeeded but the client hung up).
func (u *UseCase) writeAudit(ctx context.Context, in Input, actual shareddomain.InstanceName, hash, result string) {
	rec := AuditRecord{
		InstanceName: actual,
		Hash:         hash,
		Action:       in.Action,
		Actor:        in.Actor,
		Result:       result,
		CreatedAt:    u.now(),
	}
	if err := u.audit.Write(context.WithoutCancel(ctx), rec); err != nil {
		u.log.ErrorContext(ctx, "torrent_action_audit_write_failed",
			slog.String("instance", string(actual)),
			slog.String("hash", hash),
			slog.String("action", string(in.Action)),
			slog.String("error", err.Error()))
	}
}
