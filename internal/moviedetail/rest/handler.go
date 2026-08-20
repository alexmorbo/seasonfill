package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// Handler serves GET /api/v1/movies/:tmdb_id.
type Handler struct {
	uc       *mdapp.UseCase
	resolver *media.Resolver // nil-OK: raw TMDB paths flow through unchanged
	log      *slog.Logger
}

// NewHandler constructs the movie-detail REST handler. resolver rewrites raw
// poster/backdrop/collection-poster paths to sha256 media hashes (nil-OK → raw
// paths flow through, pre-M-FIX-1 behavior).
func NewHandler(uc *mdapp.UseCase, resolver *media.Resolver, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{uc: uc, resolver: resolver, log: log}
}

// Get handles GET /api/v1/movies/:tmdb_id.
//
// @Summary     Movie detail aggregate
// @Description Returns the movie detail keyed by TMDB id: canon + localized
// @Description title/overview + franchise collection + per-instance Radarr
// @Description library membership. All data is local (no live TMDB). 404 when
// @Description no canon row exists for the tmdb id.
// @Tags        movies
// @Produce     json
// @Param       tmdb_id path      int    true  "TMDB movie id"
// @Param       lang    query     string false "BCP-47 language tag"
// @Success     200     {object}  dto.MovieDetailResponse
// @Failure     400     {object}  dto.ErrorResponse
// @Failure     401     {object}  dto.ErrorResponse
// @Failure     404     {object}  dto.ErrorResponse
// @Failure     500     {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /movies/{tmdb_id} [get]
func (h *Handler) Get(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("tmdb_id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid tmdb id", Code: "BAD_REQUEST"})
		return
	}
	lang := strings.TrimSpace(c.Query("lang"))
	d, err := h.uc.Get(c.Request.Context(), domain.TMDBID(id), lang)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "movie_not_found", Code: "MOVIE_NOT_FOUND"})
			return
		}
		h.log.ErrorContext(c.Request.Context(), "movie_detail_failed", slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "movie detail unavailable"})
		return
	}
	c.JSON(http.StatusOK, h.toMovieDetailResponse(c.Request.Context(), d))
}

// heroResolveBudget caps the first-fold hero poster + backdrop synchronous
// media resolve, mirroring the series skeleton hero (seriesdetail/app/
// skeleton.go:189). Async Resolve returns nil on a cold miss → placeholder SVG
// → a blank hero on first open; ResolveSync blocks (within this budget) so the
// hero paints the real poster/backdrop on the first fold.
const heroResolveBudget = 1500 * time.Millisecond

func (h *Handler) toMovieDetailResponse(ctx context.Context, d mdapp.Detail) dto.MovieDetailResponse {
	poster := d.Poster
	backdrop := d.Backdrop
	if h.resolver != nil {
		syncCtx, cancel := context.WithTimeout(ctx, heroResolveBudget)
		if hash := h.resolver.ResolveSync(syncCtx, d.Poster, "w342", "poster_w342"); hash != nil {
			poster = hash
		}
		if hash := h.resolver.ResolveSync(syncCtx, d.Backdrop, "w1280", "backdrop_w1280"); hash != nil {
			backdrop = hash
		}
		cancel()
	}
	out := dto.MovieDetailResponse{
		Title:            d.Title,
		Overview:         d.Overview,
		Tagline:          d.Tagline,
		Year:             d.Canon.Year,
		Status:           d.Canon.Status,
		Runtime:          d.Canon.RuntimeMinutes,
		Poster:           poster,
		Backdrop:         backdrop,
		Released:         d.Canon.ReleaseDate,
		Digital:          d.Canon.DigitalReleaseDate,
		Physical:         d.Canon.PhysicalReleaseDate,
		TMDBRating:       d.Canon.TMDBRating,
		IMDBRating:       d.Canon.IMDBRating,
		OriginalLanguage: d.Canon.OriginalLanguage,
		OriginalTitle:    d.Canon.OriginalTitle,
		Homepage:         d.Canon.Homepage,
		Budget:           d.Canon.Budget,
		Revenue:          d.Canon.Revenue,
		SyncedAt:         d.Canon.EnrichmentTMDBSyncedAt,
		Degraded:         d.Degraded,
	}
	if d.Canon.TMDBID != nil {
		out.TMDBID = int(*d.Canon.TMDBID)
	}
	if d.Canon.IMDBID != nil {
		s := string(*d.Canon.IMDBID)
		out.IMDBID = &s
	}
	if d.Collection != nil {
		colPoster := d.Collection.PosterAsset
		if h.resolver != nil {
			if hash := h.resolver.Resolve(ctx, d.Collection.PosterAsset, "w342", "poster_w342"); hash != nil {
				colPoster = hash
			}
		}
		out.Collection = &dto.MovieDetailCollection{
			TMDBCollectionID: d.Collection.TMDBCollectionID,
			Name:             d.Collection.Name,
			Poster:           colPoster,
			RadarrMonitored:  d.Collection.RadarrMonitored,
		}
	}
	for _, m := range d.Library {
		out.Library = append(out.Library, dto.MovieDetailLibrary{
			InstanceName:  m.InstanceName,
			RadarrMovieID: m.RadarrMovieID,
			Monitored:     m.Monitored,
			HasFile:       m.HasFile,
			Availability:  m.Availability,
			SizeOnDisk:    m.SizeOnDisk,
			Quality:       m.Quality,
			Resolution:    m.Resolution,
			VideoCodec:    m.VideoCodec,
			AudioCodec:    m.AudioCodec,
		})
	}
	for _, g := range d.Genres {
		out.Genres = append(out.Genres, dto.TaxonomyChip{ID: g.ID, Name: g.Name, Language: g.Language})
	}
	for _, k := range d.Keywords {
		out.Keywords = append(out.Keywords, dto.TaxonomyChip{ID: k.ID, Name: k.Name, Language: k.Language})
	}
	if len(d.Canon.OriginCountries) > 0 {
		out.Countries = append(out.Countries, d.Canon.OriginCountries...)
		c0 := d.Canon.OriginCountries[0]
		out.Country = &c0
	}
	if len(d.Companies) > 0 {
		studio := d.Companies[0].Name
		out.Studio = &studio
		for _, co := range d.Companies {
			logo := co.LogoAsset
			if h.resolver != nil {
				if hash := h.resolver.Resolve(ctx, co.LogoAsset, "w185", "company_logo_w185"); hash != nil {
					logo = hash
				}
			}
			row := dto.MovieDetailCompany{Name: co.Name, LogoAsset: logo, OriginCountry: co.OriginCountry}
			if co.TMDBID != nil {
				id := int(*co.TMDBID)
				row.TMDBID = &id
			}
			out.Companies = append(out.Companies, row)
		}
	}
	if t := toMovieTrailer(d.Trailer); t != nil {
		out.Trailer = t
	}
	return out
}

// toMovieTrailer projects the stored best-trailer row to the wire dto.Trailer.
// Returns nil when there is no trailer or no playable key (site/key nil) so the
// field stays omitempty — the FE hides a trailer with no key by design.
func toMovieTrailer(v *enrichpersistence.MovieVideo) *dto.Trailer {
	if v == nil || v.Key == nil || *v.Key == "" {
		return nil
	}
	site := ""
	if v.Site != nil {
		site = *v.Site
	}
	return &dto.Trailer{Site: site, Key: *v.Key, Name: v.Name, PublishedAt: v.PublishedAt}
}
