package icsfeed

import (
	"context"
	"fmt"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/calendar"
)

const (
	windowBackMonths = 1 // small back-window keeps recently-aired visible
	windowFwdMonths  = 3
)

// CalendarBuilder is the reused S2 calendar port. Production: *calendar.UseCase.
type CalendarBuilder interface {
	Build(ctx context.Context, q calendar.Query) (calendar.Report, error)
}

// EpochRepository reads and bumps the app_config.ics_epoch revocation
// generation. Production: *persistence.RuntimeConfigRepository.
type EpochRepository interface {
	GetICSEpoch(ctx context.Context) (int64, error)
	BumpICSEpoch(ctx context.Context) (int64, error)
}

// Minted carries a freshly signed token + the scope it encodes.
type Minted struct {
	Token string
	Scope string
}

// UseCase orchestrates the ICS feed: mint/revoke the revocation epoch and
// render the feed for a verified token. It owns its own wall clock (window
// boundaries) independent of the calendar usecase's clock.
type UseCase struct {
	cal   CalendarBuilder
	epoch EpochRepository
	key   []byte
	clock func() time.Time
}

// NewUseCase wires the ICS feed usecase (clock defaults to time.Now().UTC).
func NewUseCase(cal CalendarBuilder, epoch EpochRepository, key []byte) *UseCase {
	return &UseCase{cal: cal, epoch: epoch, key: key, clock: func() time.Time { return time.Now().UTC() }}
}

// WithClock swaps the clock for deterministic tests.
func (uc *UseCase) WithClock(clock func() time.Time) *UseCase {
	uc.clock = clock
	return uc
}

// Mint signs a token at the CURRENT ics_epoch for the given scope.
func (uc *UseCase) Mint(ctx context.Context, scope string) (Minted, error) {
	ep, err := uc.epoch.GetICSEpoch(ctx)
	if err != nil {
		return Minted{}, fmt.Errorf("ics mint: read epoch: %w", err)
	}
	tok, err := SignToken(uc.key, scope, ep)
	if err != nil {
		return Minted{}, fmt.Errorf("ics mint: sign: %w", err)
	}
	return Minted{Token: tok, Scope: normalizeScope(scope)}, nil
}

// Revoke bumps ics_epoch, invalidating every previously minted token, and
// returns the new epoch.
func (uc *UseCase) Revoke(ctx context.Context) (int64, error) {
	ep, err := uc.epoch.BumpICSEpoch(ctx)
	if err != nil {
		return 0, fmt.Errorf("ics revoke: %w", err)
	}
	return ep, nil
}

// Render verifies the token (signature + purpose), checks its epoch against
// the live ics_epoch, runs the reused calendar query over the token's scope
// and an upcoming-focused window, and renders the iCalendar body. Returns
// ErrRevoked for ANY token rejection; wrapped errors for DB/render faults.
func (uc *UseCase) Render(ctx context.Context, token string) (string, error) {
	p, err := VerifyToken(uc.key, token)
	if err != nil {
		return "", ErrRevoked
	}
	cur, err := uc.epoch.GetICSEpoch(ctx)
	if err != nil {
		return "", fmt.Errorf("ics render: read epoch: %w", err)
	}
	if p.Epoch != cur {
		return "", ErrRevoked
	}
	now := uc.clock()
	rep, err := uc.cal.Build(ctx, calendar.Query{
		From:  now.AddDate(0, -windowBackMonths, 0),
		To:    now.AddDate(0, windowFwdMonths, 0),
		Scope: p.Scope,
	})
	if err != nil {
		return "", fmt.Errorf("ics render: build: %w", err)
	}
	return Render(rep), nil
}
