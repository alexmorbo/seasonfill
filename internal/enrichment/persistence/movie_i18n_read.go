package persistence

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MovieI18nRow is the localized movie side-table read projection (Ф6-R-6a).
type MovieI18nRow struct {
	Title    *string
	Overview *string
	Tagline  *string
	Poster   *string
	Backdrop *string
}

// MovieI18nReadRepository reads one (movie_id, language) localized row. The
// movie_i18n table otherwise has only a writer (MovieI18nSeeder). Returns
// ports.ErrNotFound when absent so the detail usecase can fall back to canon
// fields / another language.
type MovieI18nReadRepository struct{ db *gorm.DB }

// NewMovieI18nReadRepository constructs the localized movie read repo.
func NewMovieI18nReadRepository(db *gorm.DB) *MovieI18nReadRepository {
	return &MovieI18nReadRepository{db: db}
}

// Get resolves the localized row for (movieID, lang) via the never-empty ladder:
// requested language → en-US → any-available (language ASC), restricted to rows
// carrying a NON-EMPTY poster_asset (S-E2/E3 invariant #2 — a canon-drop empty
// row must never shadow a poster-bearing localized row). ports.ErrNotFound when
// NO poster-bearing row exists in any language (the usecase then degrades to
// canon). Mirrors SeriesMediaTextsRepository.GetPosterAnyLang.
func (r *MovieI18nReadRepository) Get(ctx context.Context, movieID domain.MovieID, lang string) (MovieI18nRow, error) {
	if lang == "" {
		lang = fallbackLanguage
	}
	const q = "SELECT mi.title AS title, mi.overview AS overview, mi.tagline AS tagline, " +
		"mi.poster_asset AS poster_asset, mi.backdrop_asset AS backdrop_asset " +
		"FROM movie_i18n mi " +
		"WHERE mi.movie_id = ? AND mi.poster_asset IS NOT NULL AND mi.poster_asset <> '' " +
		"ORDER BY CASE WHEN mi.language = ? THEN 2 WHEN mi.language = ? THEN 1 ELSE 0 END DESC, mi.language ASC " +
		"LIMIT 1"
	var row struct {
		Title         *string `gorm:"column:title"`
		Overview      *string `gorm:"column:overview"`
		Tagline       *string `gorm:"column:tagline"`
		PosterAsset   *string `gorm:"column:poster_asset"`
		BackdropAsset *string `gorm:"column:backdrop_asset"`
	}
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(q, int64(movieID), lang, fallbackLanguage).Scan(&row).Error; err != nil {
		return MovieI18nRow{}, fmt.Errorf("get movie_i18n: %w", err)
	}
	// Raw+Scan yields no ErrRecordNotFound: a no-match leaves the struct zero.
	// The WHERE guarantees a matched row has a non-empty poster, so a nil
	// PosterAsset uniquely means "no poster-bearing row in any language".
	if row.PosterAsset == nil {
		return MovieI18nRow{}, ports.ErrNotFound
	}
	return MovieI18nRow{
		Title:    row.Title,
		Overview: row.Overview,
		Tagline:  row.Tagline,
		Poster:   row.PosterAsset,
		Backdrop: row.BackdropAsset,
	}, nil
}

// ListTitlesByTMDBIDsWithFallback maps tmdb_id → localized non-empty title via
// the never-empty ladder, for the movie library list (which carries tmdb ids,
// not canon PKs). JOINs movie_i18n → movies on movie_id. Ids with no localized
// row (or a blank ladder title) are absent (caller keeps the canon title).
func (r *MovieI18nReadRepository) ListTitlesByTMDBIDsWithFallback(
	ctx context.Context,
	tmdbIDs []int,
	lang string,
) (map[int]string, error) {
	if len(tmdbIDs) == 0 {
		return map[int]string{}, nil
	}
	if lang == "" {
		lang = fallbackLanguage
	}
	// One indexed sweep of every localized row for these movies, then the ladder
	// is resolved in Go (per tmdb id: requested → en-US → lowest language ASC).
	type joined struct {
		TMDBID   int     `gorm:"column:tmdb_id"`
		Language string  `gorm:"column:language"`
		Title    *string `gorm:"column:title"`
	}
	var rows []joined
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("movie_i18n mi").
		Select("m.tmdb_id AS tmdb_id, mi.language AS language, mi.title AS title").
		Joins("JOIN movies m ON m.id = mi.movie_id").
		Where("m.tmdb_id IN ?", tmdbIDs).
		Order("mi.language ASC").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list movie titles by tmdb ids (lang=%s): %w", lang, err)
	}
	// Rank per tmdb id: requested(2) > en-US(1) > any(0); language ASC tiebreak
	// is already applied by the ORDER BY, so the FIRST better-ranked non-empty
	// title wins.
	best := make(map[int]int, len(tmdbIDs)) // tmdb id -> chosen rank
	out := make(map[int]string, len(tmdbIDs))
	for _, r0 := range rows {
		if r0.Title == nil {
			continue
		}
		title := strings.TrimSpace(*r0.Title)
		if title == "" {
			continue
		}
		rank := 0
		switch r0.Language {
		case lang:
			rank = 2
		case fallbackLanguage:
			rank = 1
		}
		if cur, seen := best[r0.TMDBID]; !seen || rank > cur {
			best[r0.TMDBID] = rank
			out[r0.TMDBID] = title
		}
	}
	return out, nil
}
