// Package seriesdetail — see ports.go header.
//
// ratings_tmdb_refresher.go (W18-7a). The TMDB branch of the /ratings SWR endpoint
// has no rating-only counterpart to the OMDb worker, so this narrow refresher fetches
// /tv/{id} (vote_average / vote_count), owner-writes tmdb_rating/tmdb_votes +
// enrichment_tmdb_synced_at, and mirrors the OMDb worker's negative-cache discipline
// (read terminal → skip; record not-found terminal so a bogus tmdb_id is not
// re-hammered on every open). It does NOT re-tune the shared TMDB enrichment TTL
// (F-04) and does NOT run the full series worker (text/cast/seasons/media).
package seriesdetail

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	tmdb "github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// tmdbRatingTerminalAttempts mirrors enrichment/app terminalAttempts (=99,
// series_worker.go) — the constant is unexported there. A row at or above this
// is a hard terminal not-found; the refresher skips it (no re-fetch).
const tmdbRatingTerminalAttempts = 99

// RatingsTMDBClient is the narrow TMDB seam. *tmdb.Client (and the wiring
// TMDBClientHolder) satisfy it via GetTV.
type RatingsTMDBClient interface {
	GetTV(ctx context.Context, id int64, language string) (*tmdb.TVResponse, error)
}

// RatingsTMDBWriter is the narrow persistence seam for the TMDB rating branch.
// *persistence.SeriesRepository satisfies it.
type RatingsTMDBWriter interface {
	Get(ctx context.Context, id domain.SeriesID) (series.Canon, error)
	UpdateTMDBRatingColumns(ctx context.Context, id domain.SeriesID, rating *float64, votes *int, syncedAt time.Time) error
	MarkTMDBSynced(ctx context.Context, id domain.SeriesID, now time.Time) error
}

// RatingsErrorLedger is the narrow enrichment_errors seam (terminal-respect +
// not-found journal). *persistence.EnrichmentErrorsRepository satisfies it.
type RatingsErrorLedger interface {
	GetByEntitySource(ctx context.Context, entityType enrichment.EntityType, entityID int64, source enrichment.Source) (enrichment.EnrichmentError, error)
	RecordFailure(ctx context.Context, e enrichment.EnrichmentError) error
}

// RatingsTMDBRefresher fetches + owner-writes a series' tmdb_rating. It satisfies
// the usecase's TMDBRatingRefresher port (Refresh(ctx, seriesID) error).
type RatingsTMDBRefresher struct {
	client func() RatingsTMDBClient // getter — mirrors the holder pattern (nil ⇒ skip)
	writer RatingsTMDBWriter
	ledger RatingsErrorLedger
	logger *slog.Logger
	now    func() time.Time
}

// NewRatingsTMDBRefresher validates required deps.
func NewRatingsTMDBRefresher(
	client func() RatingsTMDBClient,
	writer RatingsTMDBWriter,
	ledger RatingsErrorLedger,
	logger *slog.Logger,
	now func() time.Time,
) (*RatingsTMDBRefresher, error) {
	if client == nil {
		return nil, errors.New("ratings tmdb refresher: client getter required")
	}
	if writer == nil || ledger == nil {
		return nil, errors.New("ratings tmdb refresher: writer + ledger required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &RatingsTMDBRefresher{client: client, writer: writer, ledger: ledger, logger: logger, now: now}, nil
}

// Refresh fetches /tv/{id}, owner-writes tmdb_rating/tmdb_votes + synced_at, and
// respects the enrichment_errors terminal state. Returns nil on EVERY terminal
// outcome (no tmdb_id, terminal-negative, client unavailable, upstream error) — the
// caller re-reads the canon to see whether a value landed. Never returns a value.
func (r *RatingsTMDBRefresher) Refresh(ctx context.Context, seriesID domain.SeriesID) error {
	canon, err := r.writer.Get(ctx, seriesID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return nil
		}
		return err
	}
	if canon.TMDBID == nil || int64(*canon.TMDBID) == 0 {
		return nil // no id — usecase already maps this to unavailable; defensive
	}
	tmdbID := int64(*canon.TMDBID)

	// Negative-cache: mirror omdb_worker — skip a hard-terminal row so a
	// bogus/deleted tmdb_id is not re-hit on every open.
	if row, gerr := r.ledger.GetByEntitySource(ctx,
		enrichment.EntityTypeSeries, int64(seriesID), enrichment.SourceTMDBSeries); gerr == nil {
		if row.Attempts >= tmdbRatingTerminalAttempts {
			r.logger.DebugContext(ctx, "ratings.tmdb.terminal_skip",
				slog.Int64("series_id", int64(seriesID)))
			return nil
		}
	} else if !errors.Is(gerr, ports.ErrNotFound) {
		r.logger.WarnContext(ctx, "ratings.tmdb.ledger_read_failed",
			slog.Int64("series_id", int64(seriesID)), slog.String("error", gerr.Error()))
	}

	client := r.client()
	if client == nil {
		r.logger.DebugContext(ctx, "ratings.tmdb.client_nil", slog.Int64("series_id", int64(seriesID)))
		return nil
	}

	tv, err := client.GetTV(ctx, tmdbID, "en-US")
	if err != nil {
		var apiErr *tmdb.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			// Terminal not-found — journal so we stop re-hitting it (mirrors
			// omdb_worker not_found → attempts=99).
			r.recordTerminal(ctx, seriesID, err)
			r.logger.InfoContext(ctx, "ratings.tmdb.not_found", slog.Int64("series_id", int64(seriesID)))
			return nil
		}
		// Transient (429/5xx/timeout) — the client already self-backs-off; do NOT
		// journal (the full series worker owns TMDB error backoff). Reload will show
		// the prior value/empty; the source stays revalidating/pending.
		r.logger.WarnContext(ctx, "ratings.tmdb.fetch_failed",
			slog.Int64("series_id", int64(seriesID)), slog.String("error", err.Error()))
		return nil
	}

	rating, votes := extractTMDBRating(tv)
	now := r.now()
	if rating != nil {
		if werr := r.writer.UpdateTMDBRatingColumns(ctx, seriesID, rating, votes, now); werr != nil {
			return werr
		}
	} else {
		// Genuine no-rating from TMDB — stamp synced_at only (do NOT null an existing
		// value). MarkTMDBSynced is the shipped stamp writer.
		if werr := r.writer.MarkTMDBSynced(ctx, seriesID, now); werr != nil {
			return werr
		}
	}
	r.logger.InfoContext(ctx, "ratings.tmdb.ok",
		slog.Int64("series_id", int64(seriesID)),
		slog.Bool("had_rating", rating != nil))
	return nil
}

func (r *RatingsTMDBRefresher) recordTerminal(ctx context.Context, seriesID domain.SeriesID, cause error) {
	now := r.now()
	rec := enrichment.EnrichmentError{
		EntityType: enrichment.EntityTypeSeries,
		EntityID:   int64(seriesID),
		Source:     enrichment.SourceTMDBSeries,
		LastError:  cause.Error(),
		Attempts:   tmdbRatingTerminalAttempts,
		LastSeenAt: now,
	}
	if err := r.ledger.RecordFailure(ctx, rec); err != nil {
		r.logger.WarnContext(ctx, "ratings.tmdb.record_terminal_failed",
			slog.Int64("series_id", int64(seriesID)), slog.String("error", err.Error()))
	}
}

// extractTMDBRating reads vote_average / vote_count from the /tv response, applying
// the SAME nonZero guard the enrichment mapper uses (MapTVToCanon): a 0 rating /
// 0 votes maps to nil (no rating), NOT a stored zero.
func extractTMDBRating(tv *tmdb.TVResponse) (*float64, *int) {
	if tv == nil {
		return nil, nil
	}
	var rating *float64
	if tv.VoteAverage > 0 {
		v := tv.VoteAverage
		rating = &v
	}
	var votes *int
	if tv.VoteCount > 0 {
		v := tv.VoteCount
		votes = &v
	}
	return rating, votes
}
