package persistence

import (
	"context"
	"fmt"
	"strings"
	"time"

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

// Get resolves the localized row for (movieID, lang) via a PER-FIELD ladder
// (requested language → en-US → any-available, language ASC). Candidates are
// restricted to rows carrying a NON-EMPTY poster_asset (S-E2/E3 invariant #2 — a
// canon-drop empty row must never shadow a poster-bearing localized row); among
// those, EACH field (title, overview, tagline, poster, backdrop) is resolved
// independently, so a poster-bearing ru-RU stub with an EMPTY overview no longer
// shadows the good en-US overview (the localized title still wins). ports.ErrNotFound
// when NO poster-bearing row exists in any language (the usecase then degrades to
// canon). Mirrors the per-column any-lang philosophy of
// SeriesMediaTextsRepository.GetPosterAnyLang/GetBackdropAnyLang.
func (r *MovieI18nReadRepository) Get(ctx context.Context, movieID domain.MovieID, lang string) (MovieI18nRow, error) {
	if lang == "" {
		lang = fallbackLanguage
	}
	const q = "SELECT mi.language AS language, mi.title AS title, mi.overview AS overview, " +
		"mi.tagline AS tagline, mi.poster_asset AS poster_asset, mi.backdrop_asset AS backdrop_asset " +
		"FROM movie_i18n mi " +
		"WHERE mi.movie_id = ? AND mi.poster_asset IS NOT NULL AND mi.poster_asset <> '' " +
		"ORDER BY mi.language ASC"
	var rows []struct {
		Language      string  `gorm:"column:language"`
		Title         *string `gorm:"column:title"`
		Overview      *string `gorm:"column:overview"`
		Tagline       *string `gorm:"column:tagline"`
		PosterAsset   *string `gorm:"column:poster_asset"`
		BackdropAsset *string `gorm:"column:backdrop_asset"`
	}
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(q, int64(movieID)).Scan(&rows).Error; err != nil {
		return MovieI18nRow{}, fmt.Errorf("get movie_i18n: %w", err)
	}
	// Every candidate row carries a non-empty poster (WHERE clause), so an empty
	// slice uniquely means "no poster-bearing row in any language".
	if len(rows) == 0 {
		return MovieI18nRow{}, ports.ErrNotFound
	}

	langRank := func(l string) int {
		switch l {
		case lang:
			return 2
		case fallbackLanguage:
			return 1
		default:
			return 0
		}
	}
	// Per-field best-rank resolution. Rows are ordered language ASC, so at equal
	// rank the first-seen (lowest language) wins — matching the old CASE…,language
	// ASC tiebreak. A non-empty value from a strictly higher-ranked row overwrites,
	// so each field falls through the ladder independently.
	var out MovieI18nRow
	titleRank, overviewRank, taglineRank, posterRank, backdropRank := -1, -1, -1, -1, -1
	pick := func(field **string, curRank *int, val *string, rank int) {
		if val == nil || *val == "" {
			return
		}
		if rank > *curRank {
			*curRank = rank
			*field = val
		}
	}
	for i := range rows {
		row := rows[i]
		rank := langRank(row.Language)
		pick(&out.Title, &titleRank, row.Title, rank)
		pick(&out.Overview, &overviewRank, row.Overview, rank)
		pick(&out.Tagline, &taglineRank, row.Tagline, rank)
		pick(&out.Poster, &posterRank, row.PosterAsset, rank)
		pick(&out.Backdrop, &backdropRank, row.BackdropAsset, rank)
	}
	return out, nil
}

// TitleLanguage resolves the BCP-47 language the localized TITLE resolves to via
// the requested → en-US → any (language ASC) ladder, over rows carrying a
// non-empty title. Returns "" (and nil error) when the movie has no titled
// localized row. Feeds the Ф2.1 movie cast served_language / missing_lang signal
// — the movie analog of the series cast hero-title language (W15-9).
func (r *MovieI18nReadRepository) TitleLanguage(ctx context.Context, movieID domain.MovieID, lang string) (string, error) {
	if lang == "" {
		lang = fallbackLanguage
	}
	const q = "SELECT mi.language AS language, mi.title AS title " +
		"FROM movie_i18n mi " +
		"WHERE mi.movie_id = ? AND mi.title IS NOT NULL AND mi.title <> '' " +
		"ORDER BY mi.language ASC"
	var rows []struct {
		Language string  `gorm:"column:language"`
		Title    *string `gorm:"column:title"`
	}
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(q, int64(movieID)).Scan(&rows).Error; err != nil {
		return "", fmt.Errorf("get movie_i18n title language: %w", err)
	}
	// requested(2) > en-US(1) > any(0); rows are language ASC so the first
	// best-ranked row wins the tiebreak.
	langRank := func(l string) int {
		switch l {
		case lang:
			return 2
		case fallbackLanguage:
			return 1
		default:
			return 0
		}
	}
	best, out := -1, ""
	for _, row := range rows {
		if row.Title == nil || *row.Title == "" {
			continue
		}
		if rank := langRank(row.Language); rank > best {
			best = rank
			out = row.Language
		}
	}
	return out, nil
}

// HasLocalizedTextGap reports whether the (movieID, lang) localized row has a
// text gap that is DUE for a re-hydration recheck (U-1b on-view heal). A gap is:
// a movie_i18n row for this (movie_id, language) EXISTS whose title is NULL/empty
// OR whose overview is NULL/empty, AND whose enriched_at is NULL or strictly older
// than recheckBefore.
//
//   - "row exists" gates out genuinely-untranslated movies (no row for this lang):
//     TMDB never returned a translation, so there is nothing to heal — returning
//     false keeps the freshener from firing HandleForced forever (anti-storm).
//   - "title/overview empty" targets the U-1 empty-title bug (and a stray empty
//     overview): the value the read ladder would otherwise fall back across langs for.
//   - "enriched_at NULL or < recheckBefore" bounds the pathological "TMDB has an
//     overview but no title" case to one recheck per recheckBefore window: after a
//     HandleForced, UpsertEnriched stamps enriched_at = now, so the gap is suppressed
//     until now + window even when the title stays empty.
//
// Pure read, dialect-portable (EXISTS + IS NULL/” + timestamp compare, ? binds) so
// it runs identically on Postgres (prod) and the SQLite test lane. Errors surface to
// the caller, which fails CLOSED (treats as no-gap) — the background picker is the
// backstop for a transient read error, and an on-request path must not fire a 5s TMDB
// hydrate on a flaky read.
func (r *MovieI18nReadRepository) HasLocalizedTextGap(
	ctx context.Context,
	movieID domain.MovieID,
	lang string,
	recheckBefore time.Time,
) (bool, error) {
	if movieID == 0 || lang == "" {
		return false, nil
	}
	const q = "SELECT 1 FROM movie_i18n mi " +
		"WHERE mi.movie_id = ? AND mi.language = ? " +
		"AND (mi.enriched_at IS NULL OR mi.enriched_at < ?) " +
		"AND ((mi.title IS NULL OR mi.title = '') OR (mi.overview IS NULL OR mi.overview = '')) " +
		"LIMIT 1"
	var hit *int
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(q, int64(movieID), lang, recheckBefore.UTC()).Scan(&hit).Error; err != nil {
		return false, fmt.Errorf("movie_i18n text gap (movie=%d lang=%s): %w", int64(movieID), lang, err)
	}
	return hit != nil, nil
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

// MovieRecsCoverage returns (covered, total) localized rec-title coverage for a
// MOVIE — the ADR-0022 S4 mirror of SeriesTextsRepository.RecommendationsCoverage.
// total = distinct recommended_movie_id in movie_recommendations for movieID;
// covered = those with a movie_i18n row (language == language AND title IS NOT NULL).
// Returns (0,0,nil) when the parent has no recommendations rows (cold-boot / never-
// enriched-recs movie) — the recsPlugin then reads total==0 as "no_recs" (fresh).
// No fallback ladder: the plugin asks "is THIS lang present", not "anything from the
// fallback chain" (mirror of the series coverage semantics).
func (r *MovieI18nReadRepository) MovieRecsCoverage(
	ctx context.Context,
	movieID domain.MovieID,
	language string,
) (covered, total int, err error) {
	var totalCnt int64
	if e := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("movie_recommendations").
		Where("movie_id = ?", movieID).
		Distinct("recommended_movie_id").
		Count(&totalCnt).Error; e != nil {
		return 0, 0, fmt.Errorf("count movie_recommendations: %w", e)
	}
	if totalCnt == 0 {
		return 0, 0, nil
	}
	var coveredCnt int64
	if e := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("movie_recommendations AS mr").
		Joins("JOIN movie_i18n mi ON mi.movie_id = mr.recommended_movie_id AND mi.language = ? AND mi.title IS NOT NULL", language).
		Where("mr.movie_id = ?", movieID).
		Distinct("mr.recommended_movie_id").
		Count(&coveredCnt).Error; e != nil {
		return 0, 0, fmt.Errorf("count movie_i18n for recommendations: %w", e)
	}
	return int(coveredCnt), int(totalCnt), nil
}
