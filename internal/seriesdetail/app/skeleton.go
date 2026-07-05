package seriesdetail

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// SkeletonDTO is the above-fold canon document returned by
// SkeletonComposer.Compose (PLAN §7.1). Every scalar is an A0 typed VO
// (Object Calisthenics §4.3) — a zero VO marshals to JSON null. Sonarr /
// qBit per-instance state is intentionally absent (§7.0 bounded-context):
// in_library_instances points the FE at the separate /library endpoint.
type SkeletonDTO struct {
	SeriesID domain.SeriesID    `json:"series_id"`
	Lang     values.LanguageTag `json:"lang"`

	Hero struct {
		Title            values.Title             `json:"title"`
		OriginalTitle    values.Title             `json:"original_title"`
		Tagline          values.Tagline           `json:"tagline,omitempty"`
		YearStart        values.Year              `json:"year_start"`
		YearEnd          *values.Year             `json:"year_end,omitempty"`
		RuntimeMinutes   values.Minutes           `json:"runtime_minutes"`
		ContentRating    values.ContentRating     `json:"content_rating,omitempty"`
		Genres           []GenreRef               `json:"genres"`
		TmdbRating       *values.Rating           `json:"tmdb_rating,omitempty"`
		ImdbRating       *values.Rating           `json:"imdb_rating,omitempty"`
		OmdbRating       *values.Rating           `json:"omdb_rating,omitempty"`
		NextEpisodeCanon *values.NextEpisodeCanon `json:"next_episode,omitempty"`
		PosterAsset      values.MediaHash         `json:"poster_asset"`
		BackdropAsset    values.MediaHash         `json:"backdrop_asset"`
		TrailerKey       *values.TrailerKey       `json:"trailer_key,omitempty"`
	} `json:"hero"`

	Sidebar struct {
		Status              values.SeriesStatus  `json:"status"`
		Networks            []NetworkRef         `json:"networks"`
		ProductionCompanies []CompanyRef         `json:"production_companies"`
		FirstAirDate        *time.Time           `json:"first_air_date,omitempty"`
		OriginCountries     []values.CountryCode `json:"origin_countries"`
		OriginalLanguage    values.LangCode      `json:"original_language"`
		Keywords            []KeywordRef         `json:"keywords"`
	} `json:"sidebar"`

	SeasonCount        int      `json:"season_count"`
	InLibraryInstances []string `json:"in_library_instances"`

	// ServedLanguage is the BCP-47 language the hero title (the section's
	// principal localized text) was actually served in (W15-9). Empty when
	// the title fell through to canon.OriginalTitle (no series_texts row).
	// When it differs from the requested Lang, computeDegraded appends the
	// "missing_lang" marker so the FE re-polls until en-US lands.
	ServedLanguage string `json:"served_language,omitempty"`

	// ExternalLinks is the IMDb / TMDB / TVDB / homepage footer row (C3c-1,
	// restored from the pre-B1b fat contract). Always present on the wire;
	// each inner field is nil when the canon carries no value. The FE footer
	// renders nothing when every field is nil.
	ExternalLinks ExternalLinks `json:"external_links"`

	Degraded []string  `json:"degraded,omitempty"`
	SyncedAt time.Time `json:"synced_at"`
}

// GenreRef / NetworkRef / CompanyRef / KeywordRef mirror PLAN §7.1 exactly.
// tmdb_id is a wire number (not an internal typed ID) and name is the
// localized display string — neither is covered by the bare-ID guard.
type GenreRef struct {
	TmdbID int    `json:"tmdb_id"`
	Name   string `json:"name"`
}

type NetworkRef struct {
	TmdbID    int    `json:"tmdb_id"`
	Name      string `json:"name"`
	LogoAsset string `json:"logo_asset,omitempty"`
}

type CompanyRef struct {
	TmdbID    int    `json:"tmdb_id"`
	Name      string `json:"name"`
	LogoAsset string `json:"logo_asset,omitempty"`
}

type KeywordRef struct {
	TmdbID int    `json:"tmdb_id"`
	Name   string `json:"name"`
}

// ExternalLinks is the IMDb / TMDB / TVDB / homepage footer row (C3c-1).
// Restored from the pre-B1b dto.ExternalLinks contract byte-for-byte. Each
// field is an optional pointer — nil when the canon has no value. The typed
// domain IDs marshal to plain string / number (no custom MarshalJSON), so no
// .swaggo override is required.
type ExternalLinks struct {
	IMDBID   *domain.IMDBID `json:"imdb_id,omitempty" example:"tt0903747"`
	TMDBID   *domain.TMDBID `json:"tmdb_id,omitempty" example:"1396"`
	TVDBID   *domain.TVDBID `json:"tvdb_id,omitempty" example:"81189"`
	Homepage *string        `json:"homepage,omitempty"`
}

// NextEpisodeRef is the single next-aired canon episode projection read by
// the skeleton hero. Season/episode are 1-based wire numbers.
type NextEpisodeRef struct {
	SeasonNumber  int
	EpisodeNumber int
	Title         string
	AirDate       time.Time
}

// NextEpisodePort returns ONLY the soonest future-aired canon episode for a
// series (title localized via the language-fallback chain). This is the one
// new port B1a introduces; its concrete repository impl is delivered in B1b.
// ok=false means the series has no future-dated episode (ended / no schedule).
type NextEpisodePort interface {
	NextAired(ctx context.Context, seriesID domain.SeriesID, language string) (NextEpisodeRef, bool, error)
}

// SkeletonDeps groups the canon-only dependencies. No episode_states,
// season_stats, Sonarr client, or qBit client (§7.0). NextEpisode and
// Freshener are nil-OK.
type SkeletonDeps struct {
	Series            SeriesPort
	SeriesTexts       SeriesTextsPort
	SeriesMediaTexts  SeriesMediaTextsPort // Story 584b — nil-OK, canon fallback
	Genres            GenresPort
	Keywords          KeywordsPort
	Networks          NetworksPort
	Companies         CompaniesPort
	ContentRatings    ContentRatingsPort
	Videos            VideosPort
	Seasons           SeasonsPort
	SeriesCacheLookup SeriesCacheLookupPort
	NextEpisode       NextEpisodePort
	Freshener         SeriesFreshener
	MediaResolver     *media.Resolver
	Logger            *slog.Logger
	Now               func() time.Time
}

// SkeletonComposer builds the above-fold canon document. Testable in
// isolation — every dependency is a narrow port or nil-OK seam.
type SkeletonComposer struct {
	d SkeletonDeps
}

// NewSkeletonComposer applies the package defaults (Logger, Now, nop
// resolver) identical to NewComposer / NewCastComposer.
func NewSkeletonComposer(d SkeletonDeps) *SkeletonComposer {
	if d.Logger == nil {
		d.Logger = sharedports.DomainLogger(slog.Default(), "composer")
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.MediaResolver == nil {
		d.MediaResolver = media.NewNopResolver()
	}
	return &SkeletonComposer{d: d}
}

// posterResolveBudget caps the per-asset first-fold media resolve, matching
// Composer.Get (composer.go:1040).
const posterResolveBudget = 1500 * time.Millisecond

// Compose runs the 3-branch skeleton read. lang is a BCP-47 LanguageTag
// ("ru-RU") — passed verbatim to repos, EnsureFreshScope, and title VOs
// (no server-side normalization, operator directive §4.1).
func (sc *SkeletonComposer) Compose(ctx context.Context, seriesID domain.SeriesID, lang values.LanguageTag) (SkeletonDTO, error) {
	langTag := lang
	langStr := lang.Value()

	var freshen FreshenResult
	if sc.d.Freshener != nil {
		// SectionSkeleton stays ModeSync — the ONLY section the HTTP
		// response blocks on (SyncTimeout-bounded). The skeleton verdict
		// alone drives the degraded[] projection below; the response budget
		// is unchanged from the pre-W17-2 skeleton-only shape.
		freshen, _ = sc.d.Freshener.EnsureFreshScope(
			ctx, seriesID, langStr,
			[]freshener.Section{freshener.SectionSkeleton},
			nil,   // seasonNumbers — skeleton renders no season episodes
			false, // force — TTL respected
			ModeSync,
		)
		// W17-2 — bring the library detail-view freshen scope to parity with
		// the canonical/tmdb_fallback route (tmdb_fallback_usecase.go). Prior
		// to this, only SectionSkeleton was freshened on a library open, so a
		// library series' Overview/Cast/Media were never fetched: A4 per-lang
		// art (eager-hash), S-B all-langs text, and the
		// enrichment_media/text/cast_synced_at stamps stayed absent forever
		// (all 120 library series had enrichment_media_synced_at NULL). Dispatch
		// the three heavy sections in ModeAsync (detached goroutines, 180s
		// budget — the same async-carryover path a ModeSync timeout re-uses) so
		// the response NEVER waits on the TMDB fetches. force=false keeps the
		// Probe as the gate: a fresh section (TTL not elapsed / singleflight
		// in-flight) dispatches nothing, so re-opening a warm page does not
		// re-hit TMDB. Fire-and-forget: the async FreshenResult/error is
		// intentionally discarded (the sync skeleton call owns degraded[]).
		_, _ = sc.d.Freshener.EnsureFreshScope(
			ctx, seriesID, langStr,
			[]freshener.Section{
				freshener.SectionOverview,
				freshener.SectionCast,
				freshener.SectionMedia,
			},
			nil,   // seasonNumbers — hero sections, no season episodes
			false, // force — TTL respected (Probe gates re-runs)
			ModeAsync,
		)
	}

	canon, err := sc.d.Series.Get(ctx, seriesID)
	if err != nil {
		return SkeletonDTO{}, fmt.Errorf("skeleton canon load: %w", err)
	}

	dto := SkeletonDTO{SeriesID: seriesID, Lang: lang}
	dto.SyncedAt = canon.UpdatedAt
	dto.ExternalLinks = buildExternalLinks(canon)

	g, gctx := errgroup.WithContext(ctx)

	// Branch a — hero.
	g.Go(func() error {
		return sc.buildHero(gctx, &dto, canon, seriesID, langStr, langTag)
	})

	// Branch b — sidebar.
	g.Go(func() error {
		return sc.buildSidebar(gctx, &dto, canon, seriesID, langStr)
	})

	// Branch c — season_count + in_library_instances.
	g.Go(func() error {
		return sc.buildCounts(gctx, &dto, seriesID)
	})

	if gerr := g.Wait(); gerr != nil {
		return SkeletonDTO{}, gerr
	}

	dto.Degraded = sc.computeDegraded(canon, freshen, dto.ServedLanguage, langStr)
	return dto, nil
}

func (sc *SkeletonComposer) buildHero(ctx context.Context, dto *SkeletonDTO, canon series.Canon, seriesID domain.SeriesID, langStr string, langTag values.LanguageTag) error {
	// S-E2 — title/tagline resolved from series_texts (requested lang →
	// en-US → any-lang via GetWithFallback). Canon series.title is no
	// longer a fallback tier (dark-launch Variant A; S-E1 guarantees an
	// en-US row). W15-2 — when series_texts yields nothing, original_title
	// is the terminal title tier before the zero VO: it was deliberately
	// retained in canon (Variant A) precisely to serve here. A total miss
	// (no text row AND no original_title) leaves the Title a zero VO → JSON
	// null, which the FE renders as a placeholder.
	var display string
	text, terr := sc.d.SeriesTexts.GetWithFallback(ctx, seriesID, langStr)
	if terr == nil {
		if text.Title != nil && *text.Title != "" {
			display = *text.Title
			// W15-9 — the served row's language is the principal-title signal.
			// Only meaningful when the title was actually USED from this row
			// (non-empty; an empty-string title falls through to original_title
			// below, so it must NOT emit a fallback signal — parity with
			// cast.go). Written on the DTO here in the hero errgroup branch,
			// which exclusively owns hero fields — the sidebar/counts branches
			// never touch ServedLanguage and Compose reads it only after
			// g.Wait(), so this is race-safe (identical ownership to
			// dto.Hero.Title).
			dto.ServedLanguage = text.Language
		}
		if text.Tagline != nil {
			dto.Hero.Tagline = buildTagline(*text.Tagline, langTag)
		}
	}
	if display == "" && canon.OriginalTitle != nil && *canon.OriginalTitle != "" {
		display = *canon.OriginalTitle
	}
	dto.Hero.Title = buildTitle(display, langTag)
	if canon.OriginalTitle != nil && *canon.OriginalTitle != "" {
		// IN-11: request langTag as lang carrier (no origin-lang expansion).
		dto.Hero.OriginalTitle = buildTitle(*canon.OriginalTitle, langTag)
	}

	dto.Hero.YearStart = yearStart(canon)
	dto.Hero.YearEnd = yearEnd(canon)
	dto.Hero.RuntimeMinutes = minutesOrZero(canon.RuntimeMinutes)

	dto.Hero.TmdbRating = buildRating(canon.TMDBRating, canon.TMDBVotes)
	dto.Hero.ImdbRating = buildRating(canon.IMDBRating, canon.IMDBVotes)
	// OmdbRating: canon carries no numeric OMDb rating (IN-8) — stays nil.

	// Content rating (locale-picked, guard against non-enum TMDB values).
	ratings, crErr := sc.d.ContentRatings.ListBySeries(ctx, seriesID)
	if crErr == nil {
		if picked := pickContentRating(ratings, langStr); picked != nil {
			dto.Hero.ContentRating = contentRatingOrZero(picked.Rating)
		}
	}

	// Genres (localized).
	genreIDs, gErr := sc.d.Genres.ListBySeries(ctx, seriesID)
	if gErr != nil {
		return fmt.Errorf("skeleton genres: %w", gErr)
	}
	if len(genreIDs) > 0 {
		genres, err := sc.d.Genres.ListByIDsWithFallback(ctx, genreIDs, langStr)
		if err != nil {
			return fmt.Errorf("skeleton genres i18n: %w", err)
		}
		byID := make(map[int64]taxonomy.Genre, len(genres))
		for _, gg := range genres {
			byID[gg.ID] = gg
		}
		for _, id := range genreIDs {
			if gg, ok := byID[id]; ok {
				dto.Hero.Genres = append(dto.Hero.Genres, GenreRef{TmdbID: tmdbIntOf(gg.TMDBID), Name: gg.Name})
			}
		}
	}

	// Trailer key (lang-aware: requested lang → original → en → any).
	if videos, verr := sc.d.Videos.ListBySeriesAndType(ctx, seriesID, "Trailer"); verr == nil {
		dto.Hero.TrailerKey = pickTrailerForLang(videos, langStr, strOrEmpty(canon.OriginalLanguage))
	}

	// Next episode (nil-OK port).
	if sc.d.NextEpisode != nil {
		if ref, ok, nerr := sc.d.NextEpisode.NextAired(ctx, seriesID, langStr); nerr == nil && ok {
			if title := buildTitle(ref.Title, langTag); !title.IsZero() {
				days := int(ref.AirDate.Sub(sc.d.Now()).Hours() / 24)
				if ne, cerr := values.NewNextEpisodeCanon(ref.SeasonNumber, ref.EpisodeNumber, title, ref.AirDate, days); cerr == nil {
					dto.Hero.NextEpisodeCanon = &ne
				}
			}
		}
	}

	// Poster / backdrop first-fold sync resolve. Story 584b — read the
	// per-language series_media_texts raw path (requested lang → en-US via
	// the repo). S-E3a — canon series.poster_asset / backdrop_asset removed;
	// series_media_texts is the ONLY source (a cold/never-enriched series
	// with no per-lang row renders a monogram). Resolve sizes + budget
	// unchanged.
	var posterPath, backdropPath *string
	if sc.d.SeriesMediaTexts != nil {
		if mt, err := sc.d.SeriesMediaTexts.GetWithFallback(ctx, seriesID, langStr); err == nil {
			if mt.PosterAsset != nil && *mt.PosterAsset != "" {
				posterPath = mt.PosterAsset
			}
			if mt.BackdropAsset != nil && *mt.BackdropAsset != "" {
				backdropPath = mt.BackdropAsset
			}
		}
	}

	syncCtx, cancel := context.WithTimeout(ctx, posterResolveBudget)
	dto.Hero.PosterAsset = mediaHashOrZero(sc.d.MediaResolver.ResolveSync(syncCtx, posterPath, "w342", "poster_w342"))
	dto.Hero.BackdropAsset = mediaHashOrZero(sc.d.MediaResolver.ResolveSync(syncCtx, backdropPath, "w1280", "backdrop_w1280"))
	cancel()

	return nil
}

func (sc *SkeletonComposer) buildSidebar(ctx context.Context, dto *SkeletonDTO, canon series.Canon, seriesID domain.SeriesID, langStr string) error {
	if canon.Status != nil {
		dto.Sidebar.Status = seriesStatusOrZero(*canon.Status)
	}
	dto.Sidebar.FirstAirDate = canon.FirstAirDate
	dto.Sidebar.OriginalLanguage = langCodeOrZero(canon.OriginalLanguage)

	for _, cc := range canon.OriginCountries {
		if code, err := values.NewCountryCode(cc); err == nil {
			dto.Sidebar.OriginCountries = append(dto.Sidebar.OriginCountries, code)
		}
	}

	// Networks.
	netIDs, nErr := sc.d.Networks.ListBySeries(ctx, seriesID)
	if nErr != nil {
		return fmt.Errorf("skeleton networks: %w", nErr)
	}
	if len(netIDs) > 0 {
		nets, err := sc.d.Networks.ListByIDs(ctx, netIDs)
		if err != nil {
			return fmt.Errorf("skeleton networks by ids: %w", err)
		}
		for _, n := range nets {
			dto.Sidebar.Networks = append(dto.Sidebar.Networks, NetworkRef{
				TmdbID:    tmdbIntOf(n.TMDBID),
				Name:      n.Name,
				LogoAsset: strOrEmpty(sc.d.MediaResolver.Resolve(ctx, n.LogoAsset, "w185", "network_logo_w185")),
			})
		}
	}

	// Companies (non-fatal — reserved surface).
	if coIDs, cErr := sc.d.Companies.ListBySeries(ctx, seriesID); cErr == nil && len(coIDs) > 0 {
		if cos, err := sc.d.Companies.ListByIDs(ctx, coIDs); err == nil {
			for _, c := range cos {
				dto.Sidebar.ProductionCompanies = append(dto.Sidebar.ProductionCompanies, CompanyRef{
					TmdbID:    tmdbIntOf(c.TMDBID),
					Name:      c.Name,
					LogoAsset: strOrEmpty(sc.d.MediaResolver.Resolve(ctx, c.LogoAsset, "w185", "company_logo_w185")),
				})
			}
		}
	}

	// Keywords (localized, embedded per §7.1.5).
	kwIDs, kErr := sc.d.Keywords.ListBySeries(ctx, seriesID)
	if kErr != nil {
		return fmt.Errorf("skeleton keywords: %w", kErr)
	}
	if len(kwIDs) > 0 {
		kws, err := sc.d.Keywords.ListByIDsWithFallback(ctx, kwIDs, langStr)
		if err != nil {
			return fmt.Errorf("skeleton keywords i18n: %w", err)
		}
		byID := make(map[int64]taxonomy.Keyword, len(kws))
		for _, k := range kws {
			byID[k.ID] = k
		}
		for _, id := range kwIDs {
			if k, ok := byID[id]; ok {
				dto.Sidebar.Keywords = append(dto.Sidebar.Keywords, KeywordRef{TmdbID: tmdbIntOf(k.TMDBID), Name: k.Name})
			}
		}
	}

	return nil
}

func (sc *SkeletonComposer) buildCounts(ctx context.Context, dto *SkeletonDTO, seriesID domain.SeriesID) error {
	seasons, err := sc.d.Seasons.ListBySeries(ctx, seriesID)
	if err != nil {
		return fmt.Errorf("skeleton seasons count: %w", err)
	}
	dto.SeasonCount = len(seasons)

	caches, cErr := sc.d.SeriesCacheLookup.ListBySeriesID(ctx, seriesID)
	if cErr != nil {
		return fmt.Errorf("skeleton in_library lookup: %w", cErr)
	}
	seen := make(map[string]struct{}, len(caches))
	names := make([]string, 0, len(caches))
	for _, c := range caches {
		name := string(c.InstanceName)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	dto.InLibraryInstances = names // non-nil empty slice → JSON []
	return nil
}

func (sc *SkeletonComposer) computeDegraded(canon series.Canon, freshen FreshenResult, served, requested string) []string {
	var out []string
	if canon.Hydration != series.HydrationFull {
		out = append(out, "tmdb_series")
	}
	if freshen.Degraded {
		out = append(out, "freshener")
	}
	out = AppendMissingLang(out, served, requested)
	return out
}

// --- helpers ---

func buildTitle(value string, tag values.LanguageTag) values.Title {
	t, err := values.NewTitle(value, tag)
	if err != nil {
		return values.Title{}
	}
	return t
}

func buildTagline(value string, tag values.LanguageTag) values.Tagline {
	t, err := values.NewTagline(value, tag)
	if err != nil {
		return values.Tagline{}
	}
	return t
}

func buildRating(score *float64, votes *int) *values.Rating {
	if score == nil {
		return nil
	}
	sc, err := values.NewScore(*score)
	if err != nil {
		return nil
	}
	v := 0
	if votes != nil {
		v = *votes
	}
	vc, err := values.NewVoteCount(v)
	if err != nil {
		return nil
	}
	r, err := values.NewRating(sc, vc)
	if err != nil {
		return nil
	}
	return &r
}

func yearOrZero(y *int) values.Year {
	if y == nil {
		return values.Year{}
	}
	yr, err := values.NewYear(*y)
	if err != nil {
		return values.Year{}
	}
	return yr
}

// yearStart returns the first-air year, falling back to first_air_date's year
// when canon.Year is nil. Heals TMDB-only rows whose year column was never
// derived (pure display derive — writes nothing). Mirrors yearEnd.
func yearStart(canon series.Canon) values.Year {
	y := canon.Year
	if y == nil && canon.FirstAirDate != nil {
		yy := canon.FirstAirDate.Year()
		y = &yy
	}
	return yearOrZero(y)
}

// yearEnd returns the last-air year only when the show has ended; ongoing
// shows return nil so the UI renders "2026—".
func yearEnd(canon series.Canon) *values.Year {
	if canon.LastAirDate == nil {
		return nil
	}
	if canon.InProduction {
		return nil
	}
	yr, err := values.NewYear(canon.LastAirDate.Year())
	if err != nil {
		return nil
	}
	return &yr
}

func minutesOrZero(m *int) values.Minutes {
	if m == nil {
		return values.Minutes{}
	}
	mn, err := values.NewMinutes(*m)
	if err != nil {
		return values.Minutes{}
	}
	return mn
}

func contentRatingOrZero(s string) values.ContentRating {
	cr, err := values.NewContentRating(s)
	if err != nil {
		return values.ContentRating{}
	}
	return cr
}

func seriesStatusOrZero(s string) values.SeriesStatus {
	st, err := values.NewSeriesStatus(s)
	if err != nil {
		return values.SeriesStatus{}
	}
	return st
}

func langCodeOrZero(s *string) values.LangCode {
	if s == nil {
		return values.LangCode{}
	}
	lc, err := values.NewLangCode(*s)
	if err != nil {
		return values.LangCode{}
	}
	return lc
}

func mediaHashOrZero(hash *string) values.MediaHash {
	if hash == nil {
		return values.MediaHash{}
	}
	mh, err := values.NewMediaHash(*hash)
	if err != nil {
		return values.MediaHash{}
	}
	return mh
}

// pickTrailerForLang selects the trailer key to surface for the requested
// language. Videos are tried in a language-priority chain (requested lang →
// original language → English → catch-all); the first tier yielding a valid key
// wins. Tier 4 is a regression guard: a series whose only trailer is in some
// other language must still surface a trailer rather than hide it — the pre-i18n
// pick showed the best official trailer regardless of language, so dropping to
// nil here would be a regression.
func pickTrailerForLang(videos []enrichpersistence.Video, lang, originalLanguage string) *values.TrailerKey {
	for _, want := range []string{primarySubtag(lang), primarySubtag(originalLanguage), "en"} {
		if want == "" {
			continue
		}
		if tk := pickBestInLang(videos, want); tk != nil {
			return tk
		}
	}
	// Tier 4 catch-all: any remaining video (empty want matches all languages,
	// including a nil Language).
	return pickBestInLang(videos, "")
}

// pickBestInLang returns the best trailer key among videos whose primary
// language subtag equals want. want=="" matches any video (catch-all tier).
// Within the group an official YouTube "Trailer" is preferred; ties break on
// PublishedAt desc (nil published sorts last). Nil Site/Key rows are skipped.
func pickBestInLang(videos []enrichpersistence.Video, want string) *values.TrailerKey {
	var bestKey *string
	var bestPublished *time.Time
	var bestPreferred bool
	for i := range videos {
		v := videos[i]
		if v.Site == nil || v.Key == nil {
			continue
		}
		if want != "" && (v.Language == nil || primarySubtag(*v.Language) != want) {
			continue
		}
		preferred := v.Official && v.Type != nil && *v.Type == "Trailer" && *v.Site == "YouTube"
		if bestKey == nil || betterTrailer(preferred, v.PublishedAt, bestPreferred, bestPublished) {
			bestKey = v.Key
			bestPublished = v.PublishedAt
			bestPreferred = preferred
		}
	}
	if bestKey == nil {
		return nil
	}
	tk, err := values.NewTrailerKey(*bestKey)
	if err != nil {
		return nil
	}
	return &tk
}

func betterTrailer(candPref bool, candPub *time.Time, curPref bool, curPub *time.Time) bool {
	if candPref != curPref {
		return candPref
	}
	if candPub == nil {
		return false
	}
	if curPub == nil {
		return true
	}
	return candPub.After(*curPub)
}

func tmdbIntOf(id *domain.TMDBID) int {
	if id == nil {
		return 0
	}
	return int(*id)
}

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// buildExternalLinks projects the four external footer ids off the canon.
// Copies each pointee into a fresh local so the DTO never aliases the canon
// value. All-nil canon → all-nil object (FE footer renders nothing). C3c-1.
func buildExternalLinks(canon series.Canon) ExternalLinks {
	var out ExternalLinks
	if canon.IMDBID != nil {
		v := *canon.IMDBID
		out.IMDBID = &v
	}
	if canon.TMDBID != nil {
		v := *canon.TMDBID
		out.TMDBID = &v
	}
	if canon.TVDBID != nil {
		v := *canon.TVDBID
		out.TVDBID = &v
	}
	if canon.Homepage != nil && *canon.Homepage != "" {
		v := *canon.Homepage
		out.Homepage = &v
	}
	return out
}
