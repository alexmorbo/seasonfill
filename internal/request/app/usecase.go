// Package app is the request-workflow application layer (Ф8-U-2, ADR-0020 §D2).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	reqdomain "github.com/alexmorbo/seasonfill/internal/request/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// ErrRequestNotFound — no request row for the id. Handler → 404.
var ErrRequestNotFound = errors.New("request: not found")

// RequestRepository persists the requests table.
type RequestRepository interface {
	// InsertPending inserts a pending request, or returns the existing pending
	// row's id when one already matches (user_id, media_type, tmdb_id). existed
	// reports which happened.
	InsertPending(ctx context.Context, r reqdomain.Request) (id int64, existed bool, err error)
	Get(ctx context.Context, id int64) (reqdomain.Request, error)
	ListByUser(ctx context.Context, userID uint) ([]reqdomain.Request, error)
	ListAll(ctx context.Context) ([]reqdomain.Request, error)
	// SetStatus updates status + approver_id (+ updated_at). Runs on the
	// tx-scoped DB when a Transactor opened one.
	SetStatus(ctx context.Context, id int64, status string, approverID uint) error
}

// SeriesAdder replays a queued tv add. Satisfied by a wiring adapter over
// *discovery/app.AddToSonarrUseCase.
type SeriesAdder interface {
	AddTV(ctx context.Context, spec reqdomain.AddSpec) error
}

// MovieAdder replays a queued movie add. Satisfied by a wiring adapter over
// *discovery/app.AddToRadarrUseCase.
type MovieAdder interface {
	AddMovie(ctx context.Context, spec reqdomain.AddSpec) error
}

// Transactor makes the status-flip + outbox emit atomic.
type Transactor interface {
	Transaction(ctx context.Context, fn func(ctx context.Context) error) error
}

// UseCase orchestrates queue / list / approve / deny.
type UseCase struct {
	repo   RequestRepository
	sonarr SeriesAdder         // nil-OK (tv approvals disabled if absent)
	radarr MovieAdder          // nil-OK (movie approvals disabled if absent)
	outbox ports.OutboxEmitter // nil-OK (no notification emit)
	tx     Transactor          // nil-OK (falls back to non-atomic)
	log    *slog.Logger
}

// NewUseCase builds the request use case. repo required; the rest nil-OK.
func NewUseCase(repo RequestRepository, sonarr SeriesAdder, radarr MovieAdder, outbox ports.OutboxEmitter, tx Transactor, log *slog.Logger) *UseCase {
	if repo == nil {
		panic("request.NewUseCase: repo required")
	}
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "request")
	}
	return &UseCase{repo: repo, sonarr: sonarr, radarr: radarr, outbox: outbox, tx: tx, log: log}
}

// Queue implements discovery/app.RequestQueue. Idempotent.
func (u *UseCase) Queue(ctx context.Context, userID uint, spec reqdomain.AddSpec) (int64, error) {
	r := reqdomain.Request{
		UserID:    userID,
		MediaType: spec.MediaType,
		TMDBID:    spec.ExternalID,
		Seasons:   spec.Seasons,
		Spec:      spec,
		Status:    reqdomain.StatusPending,
	}
	id, existed, err := u.repo.InsertPending(ctx, r)
	if err != nil {
		return 0, fmt.Errorf("queue request: %w", err)
	}
	u.log.InfoContext(ctx, "request_queued",
		slog.Uint64("user_id", uint64(userID)),
		slog.String("media_type", spec.MediaType),
		slog.Int64("external_id", spec.ExternalID),
		slog.Int64("request_id", id),
		slog.Bool("existed", existed))
	return id, nil
}

// List returns requests scoped to the caller: all rows for a manager/admin,
// own rows otherwise.
func (u *UseCase) List(ctx context.Context, caller admin.User) ([]reqdomain.Request, error) {
	if caller.Role == admin.RoleAdmin || caller.ManageRequests {
		return u.repo.ListAll(ctx)
	}
	return u.repo.ListByUser(ctx, caller.ID)
}

// Approve replays the stored add (OUTSIDE the tx — it is a network call), then
// atomically flips status=approved + emits request.approved. Idempotent: an
// already-approved request is a no-op. Requires manage_requests (enforced by
// the route middleware).
func (u *UseCase) Approve(ctx context.Context, id int64, approver admin.User) (reqdomain.Request, error) {
	r, err := u.repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return reqdomain.Request{}, ErrRequestNotFound
		}
		return reqdomain.Request{}, fmt.Errorf("approve: load request %d: %w", id, err)
	}
	if r.Status == reqdomain.StatusApproved {
		return r, nil // idempotent no-op
	}

	// Replay the add (network) before the DB transition.
	switch r.MediaType {
	case reqdomain.MediaTypeTV:
		if u.sonarr == nil {
			return reqdomain.Request{}, errors.New("approve: sonarr adder not wired")
		}
		if aerr := u.sonarr.AddTV(ctx, r.Spec); aerr != nil {
			return reqdomain.Request{}, fmt.Errorf("approve: replay tv add: %w", aerr)
		}
	case reqdomain.MediaTypeMovie:
		if u.radarr == nil {
			return reqdomain.Request{}, errors.New("approve: radarr adder not wired")
		}
		if aerr := u.radarr.AddMovie(ctx, r.Spec); aerr != nil {
			return reqdomain.Request{}, fmt.Errorf("approve: replay movie add: %w", aerr)
		}
	default:
		return reqdomain.Request{}, fmt.Errorf("approve: unknown media_type %q", r.MediaType)
	}

	if err := u.transition(ctx, id, reqdomain.StatusApproved, approver.ID, r, "request.approved"); err != nil {
		return reqdomain.Request{}, err
	}
	r.Status = reqdomain.StatusApproved
	r.ApproverID = &approver.ID
	u.log.InfoContext(ctx, "request_approved", slog.Int64("request_id", id), slog.Uint64("approver_id", uint64(approver.ID)))
	return r, nil
}

// Deny sets status=denied + emits request.denied. Idempotent.
func (u *UseCase) Deny(ctx context.Context, id int64, approver admin.User) (reqdomain.Request, error) {
	r, err := u.repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return reqdomain.Request{}, ErrRequestNotFound
		}
		return reqdomain.Request{}, fmt.Errorf("deny: load request %d: %w", id, err)
	}
	if r.Status == reqdomain.StatusDenied {
		return r, nil
	}
	if err := u.transition(ctx, id, reqdomain.StatusDenied, approver.ID, r, "request.denied"); err != nil {
		return reqdomain.Request{}, err
	}
	r.Status = reqdomain.StatusDenied
	r.ApproverID = &approver.ID
	u.log.InfoContext(ctx, "request_denied", slog.Int64("request_id", id), slog.Uint64("approver_id", uint64(approver.ID)))
	return r, nil
}

// transition flips status + emits the notification atomically.
func (u *UseCase) transition(ctx context.Context, id int64, status string, approverID uint, r reqdomain.Request, eventType string) error {
	work := func(txCtx context.Context) error {
		if err := u.repo.SetStatus(txCtx, id, status, approverID); err != nil {
			return fmt.Errorf("set status: %w", err)
		}
		if u.outbox != nil {
			payload := requestEventPayload(r, status)
			if err := u.outbox.Insert(txCtx, ports.OutboxRow{EventType: eventType, Payload: payload}); err != nil {
				return fmt.Errorf("emit %s: %w", eventType, err)
			}
		}
		return nil
	}
	if u.tx != nil {
		return u.tx.Transaction(ctx, work)
	}
	return work(ctx)
}
