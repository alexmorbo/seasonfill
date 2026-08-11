package persistence

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
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

// Get resolves the localized row for (movieID, lang). ports.ErrNotFound on miss.
func (r *MovieI18nReadRepository) Get(ctx context.Context, movieID domain.MovieID, lang string) (MovieI18nRow, error) {
	var m database.MovieI18nModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("movie_id = ? AND language = ?", movieID, lang).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return MovieI18nRow{}, ports.ErrNotFound
		}
		return MovieI18nRow{}, fmt.Errorf("get movie_i18n: %w", err)
	}
	return MovieI18nRow{Title: m.Title, Overview: m.Overview, Tagline: m.Tagline, Poster: m.PosterAsset, Backdrop: m.BackdropAsset}, nil
}
