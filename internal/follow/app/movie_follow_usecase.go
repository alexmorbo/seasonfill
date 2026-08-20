package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// ErrInvalidTMDBID — tmdb_id was not a positive integer. Handler → 400.
var ErrInvalidTMDBID = errors.New("follow: tmdb_id must be a positive integer")

// ErrMovieNotFound — no canon movies row for the posted tmdb_id. Handler →
// 404. The whole movie surface is TMDB-keyed (GET /movies/:tmdb_id), and the
// detail read creates the canon stub on view, so a follow on a card the user
// can actually see always resolves. A follow on a never-viewed tmdb id is a
// 404 by design — the mirror of the series ErrSeriesNotFound contract.
var ErrMovieNotFound = errors.New("follow: movie not found")

// FollowedMovieItem is one movie-watchlist card (read model for
// GET /follow/movies). Mirrors FollowedItem; MovieID is the canon surrogate
// (never surfaced by the movie API) and TMDBID is the key the FE navigates by.
type FollowedMovieItem struct {
	MovieID     domain.MovieID
	TMDBID      *domain.TMDBID
	Title       string
	PosterAsset *string
	Year        *int
	FollowedAt  time.Time
}

// MovieReader resolves a canon movie by TMDB id. Satisfied by
// enrichpersistence.MovieRepository.GetByTMDBID (ports.ErrNotFound on miss).
type MovieReader interface {
	GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (movie.Canon, error)
}

// MovieFollowStore is the followed_movies persistence port.
type MovieFollowStore interface {
	Follow(ctx context.Context, userID int64, movieID domain.MovieID) error
	Unfollow(ctx context.Context, userID int64, movieID domain.MovieID) error
	ListFollowed(ctx context.Context, userID int64, lang string) ([]FollowedMovieItem, error)
}

// MovieEnricher is the narrow movie enrollment port. Satisfied by
// moviedetail/app.MovieHotEnqueuer (the SAME holder the detail read uses).
// nil-OK — enqueue is skipped when the dispatcher is not wired.
type MovieEnricher interface {
	EnqueueMovieHot(movieID domain.MovieID)
}

// MovieFollowUseCase orchestrates movie follow/unfollow/list. Deliberately a
// separate type from FollowUseCase: the two verticals key on different ids
// (canon series.id vs TMDB movie id) and enroll through different enrichment
// lanes, so a shared parametric service would only move the branching inward.
type MovieFollowUseCase struct {
	movies   MovieReader
	store    MovieFollowStore
	enricher MovieEnricher // nil-OK
	log      *slog.Logger
}

// NewMovieFollowUseCase constructs the use case. movies + store required;
// enricher nil-OK; log=nil → slog.Default.
func NewMovieFollowUseCase(movies MovieReader, store MovieFollowStore, enricher MovieEnricher, log *slog.Logger) (*MovieFollowUseCase, error) {
	if movies == nil {
		return nil, errors.New("follow: movie reader required")
	}
	if store == nil {
		return nil, errors.New("follow: movie store required")
	}
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "follow")
	}
	return &MovieFollowUseCase{movies: movies, store: store, enricher: enricher, log: log}, nil
}

// Follow marks the movie behind tmdbID as followed (idempotent) and kicks the
// Hot enrichment lane so a stub card lifts to full immediately. 404 when no
// canon movie exists for the tmdb id.
func (u *MovieFollowUseCase) Follow(ctx context.Context, userID int64, tmdbID domain.TMDBID) error {
	if userID <= 0 {
		return ErrInvalidUser
	}
	if tmdbID <= 0 {
		return ErrInvalidTMDBID
	}
	canon, err := u.movies.GetByTMDBID(ctx, tmdbID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return ErrMovieNotFound
		}
		return fmt.Errorf("follow: load movie tmdb=%d: %w", int64(tmdbID), err)
	}
	if err := u.store.Follow(ctx, userID, canon.ID); err != nil {
		return fmt.Errorf("follow: persist u=%d m=%d: %w", userID, int64(canon.ID), err)
	}
	if u.enricher != nil {
		u.enricher.EnqueueMovieHot(canon.ID)
	}
	u.log.InfoContext(ctx, "movie_followed",
		slog.Int64("user_id", userID),
		slog.Int64("movie_id", int64(canon.ID)),
		slog.Int64("tmdb_id", int64(tmdbID)))
	return nil
}

// Unfollow clears the follow row (idempotent). An unknown tmdb id is a no-op
// success — unfollowing something that is not followed is already a 200 on the
// series side, and a 404 here would only surface as a dead FE toggle.
func (u *MovieFollowUseCase) Unfollow(ctx context.Context, userID int64, tmdbID domain.TMDBID) error {
	if userID <= 0 {
		return ErrInvalidUser
	}
	if tmdbID <= 0 {
		return ErrInvalidTMDBID
	}
	canon, err := u.movies.GetByTMDBID(ctx, tmdbID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("unfollow: load movie tmdb=%d: %w", int64(tmdbID), err)
	}
	if err := u.store.Unfollow(ctx, userID, canon.ID); err != nil {
		return fmt.Errorf("unfollow: u=%d m=%d: %w", userID, int64(canon.ID), err)
	}
	u.log.InfoContext(ctx, "movie_unfollowed",
		slog.Int64("user_id", userID),
		slog.Int64("movie_id", int64(canon.ID)),
		slog.Int64("tmdb_id", int64(tmdbID)))
	return nil
}

// ListFollowed returns the movie watchlist cards, newest first.
func (u *MovieFollowUseCase) ListFollowed(ctx context.Context, userID int64, lang string) ([]FollowedMovieItem, error) {
	if userID <= 0 {
		return nil, ErrInvalidUser
	}
	items, err := u.store.ListFollowed(ctx, userID, lang)
	if err != nil {
		return nil, fmt.Errorf("follow: list movies u=%d: %w", userID, err)
	}
	return items, nil
}
