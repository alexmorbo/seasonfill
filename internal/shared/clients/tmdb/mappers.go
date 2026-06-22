package tmdb

import (
	"strconv"
	"strings"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MediaTypeTV, MediaTypeTVEpisode and MediaTypeMovie are the
// media_type discriminator values used in person_credits (B-3 + D-7).
// Kept here because they are the only place TMDB media-type strings
// leak into the mapper layer.
//
// MediaTypeTV: series-level credit row (aggregate_credits cast/crew),
// tmdb_media_id=<series tmdb_id>. Written by SeriesWorker step 7 + by
// PersonWorker for /person/{id}/tv_credits.
//
// MediaTypeTVEpisode: D-7 (468b) episode-level credit row (per-episode
// guest_stars + crew), tmdb_media_id=<episode tmdb_id>. Written by
// SeriesWorker step 7b. Replaces the dropped legacy `episode_people`
// table (which carried episode_id as the FK); the polymorphic
// person_credits surface uses the episode's TMDB id as the join key
// instead, mirroring how the series-level row keys by tmdb_media_id.
//
// MediaTypeMovie: movie credit row (PersonWorker only).
const (
	MediaTypeTV        = "tv"
	MediaTypeTVEpisode = "tv_episode"
	MediaTypeMovie     = "movie"
)

// MapTVToCanon flattens a TVResponse into a series.Canon row.
// Hydration is HydrationFull (the call fetched the full append).
// Sonarr-owned fields stay nil (Year — see PRD §5.4: year is
// Sonarr's source of truth even though TMDB has it). TVDB id IS
// populated when present on external_ids (orphan-resolution path
// reads it back).
//
// Time fields are parsed lenient: an empty string yields nil
// rather than a year-zero time.Time.
func MapTVToCanon(tv *TVResponse) series.Canon {
	if tv == nil {
		return series.Canon{}
	}
	c := series.Canon{
		TMDBID:           new(domain.TMDBID(tv.ID)),
		Hydration:        series.HydrationFull,
		Title:            tv.Name,
		OriginalTitle:    nonEmptyPtr(tv.OriginalName),
		Status:           nonEmptyPtr(tv.Status),
		FirstAirDate:     parseDate(tv.FirstAirDate),
		LastAirDate:      parseDate(tv.LastAirDate),
		Homepage:         nonEmptyPtr(tv.Homepage),
		OriginalLanguage: nonEmptyPtr(tv.OriginalLanguage),
		Popularity:       nonZeroFloatPtr(tv.Popularity),
		InProduction:     tv.InProduction,
		PosterAsset:      nonEmptyPtr(tv.PosterPath),
		BackdropAsset:    nonEmptyPtr(tv.BackdropPath),
		TMDBRating:       nonZeroFloatPtr(tv.VoteAverage),
		TMDBVotes:        nonZeroIntPtr(tv.VoteCount),
	}
	if len(tv.EpisodeRunTime) > 0 && tv.EpisodeRunTime[0] > 0 {
		c.RuntimeMinutes = new(tv.EpisodeRunTime[0])
	}
	if len(tv.OriginCountry) > 0 {
		c.OriginCountry = new(tv.OriginCountry[0])
		// OriginCountries holds the full TMDB array — used by the
		// right-rail "Страны" row. Singular OriginCountry stays the
		// first element for compat. Copy defensively to avoid aliasing
		// the TVResponse slice.
		c.OriginCountries = append([]string(nil), tv.OriginCountry...)
	}
	if tv.NextEpisodeToAir != nil {
		c.NextAirDate = parseDate(tv.NextEpisodeToAir.AirDate)
	}
	if tv.ExternalIDs != nil {
		if id := NormaliseIMDBID(tv.ExternalIDs.IMDBID); id != "" {
			v := domain.IMDBID(id)
			c.IMDBID = &v
		}
		if tv.ExternalIDs.TVDBID != nil {
			v := *tv.ExternalIDs.TVDBID
			c.TVDBID = &v
		}
	}
	return c
}

// MapTVToSeasons turns the season summary slice into CanonSeason
// rows. SeriesID is NOT set here — the caller wires it after the
// series row is upserted (it knows the freshly-assigned ID; the
// mapper does not).
func MapTVToSeasons(tv *TVResponse) []series.CanonSeason {
	if tv == nil {
		return nil
	}
	out := make([]series.CanonSeason, 0, len(tv.Seasons))
	for _, s := range tv.Seasons {
		out = append(out, series.CanonSeason{
			SeasonNumber: s.SeasonNumber,
			TMDBSeasonID: new(int(s.ID)),
			Name:         nonEmptyPtr(s.Name),
			Overview:     nonEmptyPtr(s.Overview),
			AirDate:      parseDate(s.AirDate),
			EpisodeCount: nonZeroIntPtr(s.EpisodeCount),
			PosterAsset:  nonEmptyPtr(s.PosterPath),
		})
	}
	return out
}

// MapTVToCredits flattens aggregate_credits into:
//   - []SeriesCredit (cast + crew rows)
//   - []Person stubs (one per unique TMDB person id, hydration=stub)
//
// PersonID on each SeriesCredit row is 0 — the caller resolves the
// real person_id by upserting Person stubs first (using tmdb_id as
// the natural key) and then writing SeriesCredit rows with the
// freshly-resolved ids. CharacterName / Department / Job carry the
// canonical (first role / first job) values; the C-2 worker is
// responsible for the aggregate_credits round-trip per PRD §5.5.
//
// IMPORTANT: SeriesID on SeriesCredit is also 0 — same reason as
// PersonID. The caller wires both before persisting.
func MapTVToCredits(tv *TVResponse) ([]people.SeriesCredit, []people.Person) {
	if tv == nil || tv.AggregateCredits == nil {
		return nil, nil
	}
	creds := make([]people.SeriesCredit, 0, len(tv.AggregateCredits.Cast)+len(tv.AggregateCredits.Crew))
	stubs := make([]people.Person, 0, len(tv.AggregateCredits.Cast)+len(tv.AggregateCredits.Crew))
	seen := make(map[int64]struct{}, len(tv.AggregateCredits.Cast))

	for _, cast := range tv.AggregateCredits.Cast {
		if _, ok := seen[cast.ID]; !ok {
			seen[cast.ID] = struct{}{}
			stubs = append(stubs, personStubFromCast(cast))
		}
		// Pick the first (canonical) role.
		creditID, character, episodeCount := "", "", cast.TotalEpisodeCount
		if len(cast.Roles) > 0 {
			creditID = cast.Roles[0].CreditID
			character = cast.Roles[0].Character
		}
		creds = append(creds, people.SeriesCredit{
			Kind:          people.SeriesCreditCast,
			TMDBCreditID:  creditID,
			CharacterName: nonEmptyPtr(character),
			CreditOrder:   new(cast.Order),
			EpisodeCount:  nonZeroIntPtr(episodeCount),
			// SeriesID + PersonID resolved by caller (C-2).
		})
	}

	for _, crew := range tv.AggregateCredits.Crew {
		if _, ok := seen[crew.ID]; !ok {
			seen[crew.ID] = struct{}{}
			stubs = append(stubs, personStubFromCrew(crew))
		}
		// One row per Job, not per crew member — keeps the natural
		// key (series_id, tmdb_credit_id) unique.
		for _, job := range crew.Jobs {
			creds = append(creds, people.SeriesCredit{
				Kind:         people.SeriesCreditCrew,
				TMDBCreditID: job.CreditID,
				Department:   nonEmptyPtr(crew.Department),
				Job:          nonEmptyPtr(job.Job),
				EpisodeCount: nonZeroIntPtr(job.EpisodeCount),
			})
		}
	}
	return creds, stubs
}

func personStubFromCast(c TVCastMember) people.Person {
	return people.Person{
		TMDBID:             new(domain.TMDBID(c.ID)),
		Hydration:          people.HydrationStub,
		Name:               c.Name,
		OriginalName:       nonEmptyPtr(c.OriginalName),
		Gender:             c.Gender,
		KnownForDepartment: nonEmptyPtr(c.KnownForDepartment),
		Popularity:         nonZeroFloatPtr(c.Popularity),
		ProfileAsset:       nonEmptyPtr(c.ProfilePath),
	}
}

func personStubFromCrew(c TVCrewMember) people.Person {
	return people.Person{
		TMDBID:             new(domain.TMDBID(c.ID)),
		Hydration:          people.HydrationStub,
		Name:               c.Name,
		OriginalName:       nonEmptyPtr(c.OriginalName),
		Gender:             c.Gender,
		KnownForDepartment: nonEmptyPtr(c.KnownForDepartment),
		Popularity:         nonZeroFloatPtr(c.Popularity),
		ProfileAsset:       nonEmptyPtr(c.ProfilePath),
	}
}

// MapTVToTaxonomy returns four parallel slices of taxonomy entities.
// Genre.Name / Keyword.Name come straight from the API response —
// the mapper does NOT split them into i18n rows; that's a worker
// concern (write the entity, then write the (entity_id, language)
// row to genres_i18n / keywords_i18n).
func MapTVToTaxonomy(tv *TVResponse) (genres []taxonomy.Genre, keywords []taxonomy.Keyword, networks []taxonomy.Network, companies []taxonomy.ProductionCompany) {
	if tv == nil {
		return nil, nil, nil, nil
	}
	for _, g := range tv.Genres {
		genres = append(genres, taxonomy.Genre{
			TMDBID: new(domain.TMDBID(g.ID)),
			Name:   g.Name,
		})
	}
	if tv.Keywords != nil {
		for _, k := range tv.Keywords.Results {
			keywords = append(keywords, taxonomy.Keyword{
				TMDBID: new(domain.TMDBID(k.ID)),
				Name:   k.Name,
			})
		}
	}
	for _, n := range tv.Networks {
		networks = append(networks, taxonomy.Network{
			TMDBID:        new(domain.TMDBID(n.ID)),
			Name:          n.Name,
			LogoAsset:     nonEmptyPtr(n.LogoPath),
			OriginCountry: nonEmptyPtr(n.OriginCountry),
		})
	}
	for _, c := range tv.ProductionCompanies {
		companies = append(companies, taxonomy.ProductionCompany{
			TMDBID:        new(domain.TMDBID(c.ID)),
			Name:          c.Name,
			LogoAsset:     nonEmptyPtr(c.LogoPath),
			OriginCountry: nonEmptyPtr(c.OriginCountry),
		})
	}
	return genres, keywords, networks, companies
}

// MapTVToContentRatings returns the per-country rating slice.
// Empty payload → nil slice. SeriesID is the caller's job.
func MapTVToContentRatings(tv *TVResponse) []MappedContentRating {
	if tv == nil || tv.ContentRatings == nil {
		return nil
	}
	out := make([]MappedContentRating, 0, len(tv.ContentRatings.Results))
	for _, r := range tv.ContentRatings.Results {
		out = append(out, MappedContentRating{
			Country: r.ISO31661,
			Rating:  r.Rating,
		})
	}
	return out
}

// MapTVToVideos returns the per-video slice. Date parsing is
// lenient — an unparseable published_at yields a nil pointer.
func MapTVToVideos(tv *TVResponse) []MappedVideo {
	if tv == nil || tv.Videos == nil {
		return nil
	}
	out := make([]MappedVideo, 0, len(tv.Videos.Results))
	for _, v := range tv.Videos.Results {
		out = append(out, MappedVideo{
			TMDBID:      v.ID,
			Language:    v.ISO6391,
			Country:     v.ISO31661,
			Name:        v.Name,
			Key:         v.Key,
			Site:        v.Site,
			Type:        v.Type,
			Official:    v.Official,
			Size:        v.Size,
			PublishedAt: parseRFC3339(v.PublishedAt),
		})
	}
	return out
}

// MapTVToExternalIDs returns one MappedExternalID per non-empty
// provider on the external_ids embed. NormaliseIMDBID is applied
// to the imdb_id leg.
func MapTVToExternalIDs(tv *TVResponse) []MappedExternalID {
	if tv == nil || tv.ExternalIDs == nil {
		return nil
	}
	out := make([]MappedExternalID, 0, 6)
	if id := NormaliseIMDBID(tv.ExternalIDs.IMDBID); id != "" {
		out = append(out, MappedExternalID{Provider: "imdb", ProviderID: id})
	}
	if tv.ExternalIDs.TVDBID != nil {
		out = append(out, MappedExternalID{Provider: "tvdb", ProviderID: strconv.Itoa(int(*tv.ExternalIDs.TVDBID))})
	}
	if tv.ExternalIDs.WikidataID != "" {
		out = append(out, MappedExternalID{Provider: "wikidata", ProviderID: tv.ExternalIDs.WikidataID})
	}
	if tv.ExternalIDs.FacebookID != "" {
		out = append(out, MappedExternalID{Provider: "facebook", ProviderID: tv.ExternalIDs.FacebookID})
	}
	if tv.ExternalIDs.InstagramID != "" {
		out = append(out, MappedExternalID{Provider: "instagram", ProviderID: tv.ExternalIDs.InstagramID})
	}
	if tv.ExternalIDs.TwitterID != "" {
		out = append(out, MappedExternalID{Provider: "twitter", ProviderID: tv.ExternalIDs.TwitterID})
	}
	return out
}

// MapTVToRecommendations returns one stub Canon per recommendation.
// Hydration=HydrationStub — only tmdb_id, title, year, poster,
// rating, first_air_date are filled. The C-2 worker upserts these
// (skipping if a full row already exists for the same tmdb_id) and
// writes the join row to series_recommendations.
func MapTVToRecommendations(tv *TVResponse) []series.Canon {
	if tv == nil || tv.Recommendations == nil {
		return nil
	}
	out := make([]series.Canon, 0, len(tv.Recommendations.Results))
	for _, r := range tv.Recommendations.Results {
		c := series.Canon{
			TMDBID:       new(domain.TMDBID(r.ID)),
			Hydration:    series.HydrationStub,
			Title:        r.Name,
			PosterAsset:  nonEmptyPtr(r.PosterPath),
			TMDBRating:   nonZeroFloatPtr(r.VoteAverage),
			TMDBVotes:    nonZeroIntPtr(r.VoteCount),
			FirstAirDate: parseDate(r.FirstAirDate),
		}
		if t := parseDate(r.FirstAirDate); t != nil {
			y := t.Year()
			c.Year = &y
		}
		out = append(out, c)
	}
	return out
}

// MapSeasonToEpisodes turns the season payload into CanonEpisode
// rows. seriesID and seasonID are passed in — the mapper does not
// know them. Only TMDB-owned fields per PRD §5.4 are filled;
// Sonarr-owned fields (SonarrEpisodeID, AbsoluteNumber) stay nil.
func MapSeasonToEpisodes(season *SeasonResponse, seriesID domain.SeriesID, seasonID int64) []series.CanonEpisode {
	if season == nil {
		return nil
	}
	out := make([]series.CanonEpisode, 0, len(season.Episodes))
	for _, e := range season.Episodes {
		ep := series.CanonEpisode{
			SeriesID:      seriesID,
			SeasonID:      new(seasonID),
			SeasonNumber:  e.SeasonNumber,
			EpisodeNumber: e.EpisodeNumber,
			TMDBEpisodeID: new(int(e.ID)),
			AirDate:       parseDate(e.AirDate),
			FinaleType:    nonEmptyPtr(e.EpisodeType),
			TMDBRating:    nonZeroFloatPtr(e.VoteAverage),
			TMDBVotes:     nonZeroIntPtr(e.VoteCount),
			StillAsset:    nonEmptyPtr(e.StillPath),
		}
		if e.Runtime != nil && *e.Runtime > 0 {
			ep.RuntimeMinutes = e.Runtime
		}
		out = append(out, ep)
	}
	return out
}

// MapSeasonToCredits returns EpisodeCredit rows — guest stars + crew
// — for every episode in the season payload. EpisodeID is 0 (the
// caller resolves it after upserting episodes); PersonID is 0
// (same person-stub resolve as MapTVToCredits).
func MapSeasonToCredits(season *SeasonResponse) []people.EpisodeCredit {
	if season == nil {
		return nil
	}
	out := make([]people.EpisodeCredit, 0)
	for _, e := range season.Episodes {
		for _, g := range e.GuestStars {
			out = append(out, people.EpisodeCredit{
				Kind:          people.EpisodeCreditGuestStar,
				TMDBCreditID:  g.CreditID,
				CharacterName: nonEmptyPtr(g.Character),
				CreditOrder:   new(g.Order),
			})
		}
		for _, c := range e.Crew {
			out = append(out, people.EpisodeCredit{
				Kind:         people.EpisodeCreditCrew,
				TMDBCreditID: c.CreditID,
				Department:   nonEmptyPtr(c.Department),
				Job:          nonEmptyPtr(c.Job),
			})
		}
	}
	return out
}

// MapPersonToDomain returns:
//   - people.Person — full hydration row, biography text in Biography.
//   - []people.PersonCredit — both tv_credits and movie_credits.
//
// person_credits are cross-references: the row carries tmdb_media_id
// and media_type ∈ {tv, movie}; we do NOT create series stubs for
// non-library TV titles here (B-3 schema decision). The caller
// (C-3 worker) writes them straight to the person_credits table.
//
// PersonID on each PersonCredit is 0 — caller wires after upserting
// the Person row.
func MapPersonToDomain(p *PersonResponse) (people.Person, []people.PersonCredit) {
	if p == nil {
		return people.Person{}, nil
	}
	person := people.Person{
		TMDBID:             new(domain.TMDBID(p.ID)),
		Hydration:          people.HydrationFull,
		Name:               p.Name,
		OriginalName:       nonEmptyPtr(p.OriginalName),
		Gender:             p.Gender,
		Birthday:           parseDate(p.Birthday),
		Deathday:           parseDate(p.Deathday),
		PlaceOfBirth:       nonEmptyPtr(p.PlaceOfBirth),
		KnownForDepartment: nonEmptyPtr(p.KnownForDepartment),
		Popularity:         nonZeroFloatPtr(p.Popularity),
		ProfileAsset:       nonEmptyPtr(p.ProfilePath),
		Biography:          p.Biography,
	}
	if id := NormaliseIMDBID(p.IMDBID); id != "" {
		person.IMDBID = new(id)
	}
	if p.ExternalIDs != nil {
		if id := NormaliseIMDBID(p.ExternalIDs.IMDBID); id != "" && person.IMDBID == nil {
			person.IMDBID = new(id)
		}
	}

	var credits []people.PersonCredit
	if p.TVCredits != nil {
		for _, c := range p.TVCredits.Cast {
			credits = append(credits, personCreditFromTV(c, people.SeriesCreditCast))
		}
		for _, c := range p.TVCredits.Crew {
			credits = append(credits, personCreditFromTV(c, people.SeriesCreditCrew))
		}
	}
	if p.MovieCredits != nil {
		for _, c := range p.MovieCredits.Cast {
			credits = append(credits, personCreditFromMovie(c, people.SeriesCreditCast))
		}
		for _, c := range p.MovieCredits.Crew {
			credits = append(credits, personCreditFromMovie(c, people.SeriesCreditCrew))
		}
	}
	return person, credits
}

func personCreditFromTV(c PersonTVCredit, kind people.SeriesCreditKind) people.PersonCredit {
	return people.PersonCredit{
		MediaType:     MediaTypeTV,
		TMDBMediaID:   c.ID,
		TMDBCreditID:  c.CreditID,
		Kind:          kind,
		Title:         c.Name,
		OriginalTitle: nonEmptyPtr(c.OriginalName),
		CharacterName: nonEmptyPtr(c.Character),
		Department:    nonEmptyPtr(c.Department),
		Job:           nonEmptyPtr(c.Job),
		EpisodeCount:  nonZeroIntPtr(c.EpisodeCount),
		ReleaseDate:   parseDate(c.FirstAirDate),
		PosterAsset:   nonEmptyPtr(c.PosterPath),
		TMDBRating:    nonZeroFloatPtr(c.VoteAverage),
		TMDBVotes:     nonZeroIntPtr(c.VoteCount),
	}
}

func personCreditFromMovie(c PersonMovieCredit, kind people.SeriesCreditKind) people.PersonCredit {
	return people.PersonCredit{
		MediaType:     MediaTypeMovie,
		TMDBMediaID:   c.ID,
		TMDBCreditID:  c.CreditID,
		Kind:          kind,
		Title:         c.Title,
		OriginalTitle: nonEmptyPtr(c.OriginalTitle),
		CharacterName: nonEmptyPtr(c.Character),
		Department:    nonEmptyPtr(c.Department),
		Job:           nonEmptyPtr(c.Job),
		ReleaseDate:   parseDate(c.ReleaseDate),
		PosterAsset:   nonEmptyPtr(c.PosterPath),
		TMDBRating:    nonZeroFloatPtr(c.VoteAverage),
		TMDBVotes:     nonZeroIntPtr(c.VoteCount),
	}
}

// MapFindResponseToTMDBID picks the first tv_results[*].id. Returns
// (0, false) on empty — caller treats as "no tvdb→tmdb mapping",
// the worker records an enrichment_errors row with attempts=terminalAttempts.
func MapFindResponseToTMDBID(f *FindResponse) (int64, bool) {
	if f == nil || len(f.TVResults) == 0 {
		return 0, false
	}
	return f.TVResults[0].ID, true
}

// NormaliseIMDBID prefixes "tt" when input is purely numeric.
// Empty input returns empty. PRD §13 risk 6.
func NormaliseIMDBID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "tt") {
		return raw
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			// Non-numeric and missing tt prefix — pass through;
			// caller decides whether to accept (PRD §13 risk 6 only
			// covers tt-vs-raw-digits, not arbitrary garbage).
			return raw
		}
	}
	return "tt" + raw
}

// ---- helpers (kept package-private) -----------------------------

// parseDate accepts TMDB's YYYY-MM-DD form and returns *time.Time
// in UTC (date-only times are unambiguous). Empty / unparseable
// input yields nil.
func parseDate(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil
	}
	return &t
}

// parseRFC3339 accepts the videos.published_at form
// ("2008-01-16T00:00:00.000Z"). Lenient — unparseable yields nil.
func parseRFC3339(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return &t
		}
	}
	return nil
}

func nonEmptyPtr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
func nonZeroIntPtr(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}
func nonZeroFloatPtr(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}
