package grab

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	domaingrab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedDomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ReparseGrabs is the narrow GrabRepository surface ReparseUseCase
// consumes. Lets tests stub against the two methods that matter
// (ListUnparsedSince + UpdateParsed) without implementing the entire
// 18-method GrabRepository interface.
type ReparseGrabs interface {
	ListUnparsedSince(ctx context.Context, since time.Time, limit int) ([]domaingrab.Record, error)
	UpdateParsed(ctx context.Context, id uuid.UUID, parsed *domaingrab.Parsed, parsedAt time.Time) error
}

// ReparseSonarr is the narrow SonarrClient surface ReparseUseCase
// consumes — only ParseRelease.
type ReparseSonarr interface {
	ParseRelease(ctx context.Context, title string) (ports.ParseResult, error)
}

// ReparseUseCase replays Sonarr /api/v3/parse against every grab_records
// row that landed in pre-ParseOnGrab eras (parsed_at IS NULL) and
// persists the result. Powered by the `seasonfill reparse` CLI;
// idempotent — a row whose parsed_at is already populated is skipped
// silently. Terminal-status rows (imported / import_failed / grab_failed)
// short-circuit because re-parsing them costs Sonarr calls and gains
// nothing the UI can act on.
//
// Failure-isolated by row: a single Sonarr 4xx/5xx WARNs and the loop
// continues so an indexer flap can't strand the entire backlog.
type ReparseUseCase struct {
	grabs  ReparseGrabs
	sonarr ReparseSonarr
	logger *slog.Logger
	now    func() time.Time
}

// NewReparseUseCase wires the use case with its three dependencies. The
// logger is required — pass slog.Default() when no domain logger is in
// scope (e.g. CLI commands that haven't constructed the wiring graph).
//
// Accepts a full ports.SonarrClient for callers that already have one
// in hand (CLI wiring) but stores it under the narrow ReparseSonarr
// interface so the dependency graph stays explicit.
func NewReparseUseCase(grabs ReparseGrabs, sonarrClient ReparseSonarr,
	logger *slog.Logger,
) *ReparseUseCase {
	if logger == nil {
		logger = slog.Default()
	}
	return &ReparseUseCase{
		grabs:  grabs,
		sonarr: sonarrClient,
		logger: logger,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// WithClock swaps the time source — tests-only.
func (u *ReparseUseCase) WithClock(now func() time.Time) *ReparseUseCase {
	if now != nil {
		u.now = now
	}
	return u
}

// ReplayInstance walks every grab_records row for instance where
// parsed_at IS NULL AND status NOT IN ('imported','grab_failed',
// 'cancelled') and calls Sonarr /api/v3/parse for each. Persists via
// UpdateParsed.
//
// Returns the count of rows whose Parsed actually landed. A Sonarr
// per-row failure is logged at WARN and the row stays parsed_at=NULL so
// the next manual reparse run resumes from there.
//
// The 1000-row cap matches grab_records page size; the CLI invokes
// ReplayInstance once per instance per run, so a backlog larger than
// 1000 needs an explicit repeat invocation. Operator-driven cadence is
// fine — reparse is never automatic.
func (u *ReparseUseCase) ReplayInstance(ctx context.Context, instance sharedDomain.InstanceName) (int, error) {
	rows, err := u.grabs.ListUnparsedSince(ctx, time.Unix(0, 0), 1000)
	if err != nil {
		return 0, fmt.Errorf("list unparsed: %w", err)
	}
	processed := 0
	for _, row := range rows {
		if row.InstanceName != instance {
			continue
		}
		// IsTerminal covers imported / import_failed / grab_failed —
		// re-parsing terminal rows costs Sonarr round-trips for no UI
		// signal. The grab domain model has no StatusCancelled (D-2)
		// so the terminal set fully characterises "done".
		if row.Status.IsTerminal() {
			continue
		}
		title := row.ReleaseTitle
		if title == "" {
			continue
		}
		pr, err := u.sonarr.ParseRelease(ctx, title)
		if err != nil {
			u.logger.WarnContext(ctx, "reparse_sonarr_parse_failed",
				slog.String("instance", string(instance)),
				slog.String("grab_id", row.ID.String()),
				slog.String("release_title", title),
				slog.String("error", err.Error()))
			continue
		}
		extras := sonarr.ExtractExtras(title)
		merged := sonarr.MergeParse(sonarr.ParseResult{
			Quality:      pr.Quality,
			Source:       pr.Source,
			Resolution:   pr.Resolution,
			Languages:    pr.Languages,
			ReleaseGroup: pr.ReleaseGroup,
		}, extras)
		var payload *domaingrab.Parsed
		if !merged.IsZero() {
			payload = &merged
		}
		if err := u.grabs.UpdateParsed(ctx, row.ID, payload, u.now()); err != nil {
			return processed, fmt.Errorf("update parsed for %s: %w", row.ID, err)
		}
		processed++
	}
	return processed, nil
}
