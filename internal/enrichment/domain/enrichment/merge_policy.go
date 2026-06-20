package enrichment

import "time"

// SeriesPatch is the typed write-bundle a sync worker produces
// after parsing one upstream payload (TMDB /tv/{id} response,
// Sonarr /series/{id} response, OMDb /?i=... response). Each
// field is a pointer: nil means "this source did not provide
// the field"; a non-nil pointer means "this source asserts this
// value, apply per §5.4 priority".
//
// Source precedence is encoded in the Merge* functions, not in
// the patch struct: a worker is free to set every field; the
// merge function decides whether the source has authority for
// that field.
type SeriesPatch struct {
	TMDBID           *int
	TVDBID           *int
	IMDBID           *string
	Title            *string
	OriginalTitle    *string
	Status           *string
	FirstAirDate     *time.Time
	LastAirDate      *time.Time
	NextAirDate      *time.Time
	Year             *int
	RuntimeMinutes   *int
	Homepage         *string
	OriginalLanguage *string
	OriginCountry    *string
	OriginCountries  []string
	Popularity       *float64
	InProduction     *bool
	PosterAsset      *string
	BackdropAsset    *string
	TMDBRating       *float64
	TMDBVotes        *int
	IMDBRating       *float64
	IMDBVotes        *int
	OMDBRated        *string
	OMDBAwards       *string
}

// SeriesCanon is a domain/enrichment-local mirror of the fields
// MergeSeries needs to read/write. Kept as a local struct (NOT
// `series.Canon`) so domain/enrichment has zero cross-domain
// imports — staying a pure-Go module that any layer can depend
// on without dragging the rest of the canon types along.
//
// The mapping from `series.Canon` ↔ `SeriesCanon` lives in the
// repository adapter (C-2 / C-4 use it). Field shape matches
// `series.Canon` 1:1 so the adapter is a struct-literal copy.
type SeriesCanon struct {
	Hydration        HydrationLevel
	TMDBID           *int
	TVDBID           *int
	IMDBID           *string
	Title            string
	OriginalTitle    *string
	Status           *string
	FirstAirDate     *time.Time
	LastAirDate      *time.Time
	NextAirDate      *time.Time
	Year             *int
	RuntimeMinutes   *int
	Homepage         *string
	OriginalLanguage *string
	OriginCountry    *string
	OriginCountries  []string
	Popularity       *float64
	InProduction     bool
	PosterAsset      *string
	BackdropAsset    *string
	TMDBRating       *float64
	TMDBVotes        *int
	IMDBRating       *float64
	IMDBVotes        *int
	OMDBRated        *string
	OMDBAwards       *string
}

// MergeSeries applies the PRD §5.4 per-field priority rules for
// one source's SeriesPatch against the current canon. Returns
// the merged Canon. The function is pure — it does not call
// time.Now (UpdatedAt is the caller's responsibility, stamped
// by the repository at write time).
//
// Priority rules encoded:
//
//	Title          Sonarr > TMDB
//	OriginalTitle  TMDB only
//	Status         TMDB > Sonarr
//	FirstAirDate   TMDB > Sonarr
//	LastAirDate    TMDB > Sonarr
//	NextAirDate    Sonarr > TMDB
//	Year           Sonarr > TMDB
//	RuntimeMinutes Sonarr > TMDB
//	Homepage       TMDB only
//	Popularity     TMDB only
//	InProduction   TMDB only
//	TMDBRating     TMDB only
//	TMDBVotes      TMDB only
//	IMDBRating     OMDb only
//	IMDBVotes      OMDb only
//	OMDBRated      OMDb only
//	OMDBAwards     OMDb only
//	PosterAsset    TMDB > Sonarr
//	BackdropAsset  TMDB > Sonarr
//	TMDBID         Sonarr > TMDB (Sonarr already carries tmdbId)
//	TVDBID         Sonarr only
//	IMDBID         Sonarr > TMDB
//
// Returns the canon with applied patch + updated Hydration:
// stub → full on the first TMDB write (TMDB carries the full
// authoritative dataset). Sonarr writes do NOT lift hydration
// — Sonarr is shallow data only.
func MergeSeries(canon SeriesCanon, patch SeriesPatch, source Source) SeriesCanon {
	switch source {
	case SourceSonarr:
		// Sonarr-priority fields (overwrite if patch supplies).
		if patch.Title != nil {
			canon.Title = *patch.Title
		}
		if patch.Year != nil {
			canon.Year = patch.Year
		}
		if patch.RuntimeMinutes != nil {
			canon.RuntimeMinutes = patch.RuntimeMinutes
		}
		if patch.NextAirDate != nil {
			canon.NextAirDate = patch.NextAirDate
		}
		if patch.TMDBID != nil {
			canon.TMDBID = patch.TMDBID
		}
		if patch.TVDBID != nil {
			canon.TVDBID = patch.TVDBID
		}
		if patch.IMDBID != nil {
			canon.IMDBID = patch.IMDBID
		}
		// Sonarr fallback for fields TMDB owns (fill empty only).
		if patch.Status != nil && (canon.Status == nil || *canon.Status == "") {
			canon.Status = patch.Status
		}
		if patch.FirstAirDate != nil && canon.FirstAirDate == nil {
			canon.FirstAirDate = patch.FirstAirDate
		}
		if patch.LastAirDate != nil && canon.LastAirDate == nil {
			canon.LastAirDate = patch.LastAirDate
		}
		if patch.PosterAsset != nil && (canon.PosterAsset == nil || *canon.PosterAsset == "") {
			canon.PosterAsset = patch.PosterAsset
		}
		if patch.BackdropAsset != nil && (canon.BackdropAsset == nil || *canon.BackdropAsset == "") {
			canon.BackdropAsset = patch.BackdropAsset
		}

	case SourceTMDBSeries:
		// TMDB-priority fields (overwrite).
		if patch.OriginalTitle != nil {
			canon.OriginalTitle = patch.OriginalTitle
		}
		if patch.Status != nil {
			canon.Status = patch.Status
		}
		if patch.FirstAirDate != nil {
			canon.FirstAirDate = patch.FirstAirDate
		}
		if patch.LastAirDate != nil {
			canon.LastAirDate = patch.LastAirDate
		}
		if patch.Homepage != nil {
			canon.Homepage = patch.Homepage
		}
		if patch.OriginalLanguage != nil {
			canon.OriginalLanguage = patch.OriginalLanguage
		}
		if patch.OriginCountry != nil {
			canon.OriginCountry = patch.OriginCountry
		}
		// TMDB-priority: full overwrite when the patch supplies a non-nil
		// slice (including empty slice — TMDB authoritatively dropped all
		// countries). nil patch leaves canon untouched.
		if patch.OriginCountries != nil {
			canon.OriginCountries = patch.OriginCountries
		}
		if patch.Popularity != nil {
			canon.Popularity = patch.Popularity
		}
		if patch.InProduction != nil {
			canon.InProduction = *patch.InProduction
		}
		if patch.PosterAsset != nil {
			canon.PosterAsset = patch.PosterAsset
		}
		if patch.BackdropAsset != nil {
			canon.BackdropAsset = patch.BackdropAsset
		}
		if patch.TMDBRating != nil {
			canon.TMDBRating = patch.TMDBRating
		}
		if patch.TMDBVotes != nil {
			canon.TMDBVotes = patch.TMDBVotes
		}
		// TMDB fallback for Sonarr-priority fields (fill empty).
		if patch.Title != nil && canon.Title == "" {
			canon.Title = *patch.Title
		}
		if patch.Year != nil && canon.Year == nil {
			canon.Year = patch.Year
		}
		if patch.RuntimeMinutes != nil && canon.RuntimeMinutes == nil {
			canon.RuntimeMinutes = patch.RuntimeMinutes
		}
		if patch.NextAirDate != nil && canon.NextAirDate == nil {
			canon.NextAirDate = patch.NextAirDate
		}
		if patch.TMDBID != nil && canon.TMDBID == nil {
			canon.TMDBID = patch.TMDBID
		}
		if patch.IMDBID != nil && (canon.IMDBID == nil || *canon.IMDBID == "") {
			canon.IMDBID = patch.IMDBID
		}
		// TMDB write lifts hydration to full (authoritative payload).
		canon.Hydration = LevelFull

	case SourceOMDb:
		// OMDb-only fields.
		if patch.IMDBRating != nil {
			canon.IMDBRating = patch.IMDBRating
		}
		if patch.IMDBVotes != nil {
			canon.IMDBVotes = patch.IMDBVotes
		}
		if patch.OMDBRated != nil {
			canon.OMDBRated = patch.OMDBRated
		}
		if patch.OMDBAwards != nil {
			canon.OMDBAwards = patch.OMDBAwards
		}
	}
	return canon
}

// SeasonPatch / SeasonCanon — mirrors the same pattern.
// Per PRD §5.4 season rules: name / overview / poster_asset
// are TMDB only; episode_count is Sonarr > TMDB.
type SeasonPatch struct {
	Name         *string
	Overview     *string
	AirDate      *time.Time
	EpisodeCount *int
	PosterAsset  *string
	TMDBSeasonID *int
}

type SeasonCanon struct {
	SeasonNumber int
	TMDBSeasonID *int
	Name         *string
	Overview     *string
	AirDate      *time.Time
	EpisodeCount *int
	PosterAsset  *string
}

// MergeSeason applies the §5.4 rules for season fields.
//
//	Name          TMDB only
//	Overview      TMDB only
//	AirDate       TMDB only
//	EpisodeCount  Sonarr > TMDB
//	PosterAsset   TMDB only
//	TMDBSeasonID  TMDB only
func MergeSeason(canon SeasonCanon, patch SeasonPatch, source Source) SeasonCanon {
	switch source {
	case SourceTMDBSeason:
		if patch.Name != nil {
			canon.Name = patch.Name
		}
		if patch.Overview != nil {
			canon.Overview = patch.Overview
		}
		if patch.AirDate != nil {
			canon.AirDate = patch.AirDate
		}
		if patch.PosterAsset != nil {
			canon.PosterAsset = patch.PosterAsset
		}
		if patch.TMDBSeasonID != nil {
			canon.TMDBSeasonID = patch.TMDBSeasonID
		}
		// TMDB fallback for Sonarr-priority field.
		if patch.EpisodeCount != nil && canon.EpisodeCount == nil {
			canon.EpisodeCount = patch.EpisodeCount
		}
	case SourceSonarr:
		// Sonarr authoritative for episode_count.
		if patch.EpisodeCount != nil {
			canon.EpisodeCount = patch.EpisodeCount
		}
	}
	return canon
}

// EpisodePatch / EpisodeCanon — §5.4 episode rules.
type EpisodePatch struct {
	TMDBEpisodeNumber *int
	TMDBEpisodeID     *int
	SonarrEpisodeID   *int
	AbsoluteNumber    *int
	AirDate           *time.Time
	RuntimeMinutes    *int
	FinaleType        *string
	StillAsset        *string
	TMDBRating        *float64
	TMDBVotes         *int
}

type EpisodeCanon struct {
	SeasonNumber      int
	EpisodeNumber     int
	TMDBEpisodeNumber *int
	TMDBEpisodeID     *int
	SonarrEpisodeID   *int
	AbsoluteNumber    *int
	AirDate           *time.Time
	RuntimeMinutes    *int
	FinaleType        *string
	StillAsset        *string
	TMDBRating        *float64
	TMDBVotes         *int
}

// MergeEpisode applies §5.4 episode rules:
//
//	AirDate         Sonarr > TMDB
//	RuntimeMinutes  Sonarr > TMDB
//	FinaleType      Sonarr > TMDB
//	StillAsset      TMDB only
//	TMDBRating      TMDB only
//	TMDBVotes       TMDB only
//	TMDBEpisodeID   TMDB only
//	SonarrEpisodeID Sonarr only
func MergeEpisode(canon EpisodeCanon, patch EpisodePatch, source Source) EpisodeCanon {
	switch source {
	case SourceSonarr:
		if patch.AirDate != nil {
			canon.AirDate = patch.AirDate
		}
		if patch.RuntimeMinutes != nil {
			canon.RuntimeMinutes = patch.RuntimeMinutes
		}
		if patch.FinaleType != nil {
			canon.FinaleType = patch.FinaleType
		}
		if patch.SonarrEpisodeID != nil {
			canon.SonarrEpisodeID = patch.SonarrEpisodeID
		}
		if patch.AbsoluteNumber != nil {
			canon.AbsoluteNumber = patch.AbsoluteNumber
		}
	case SourceTMDBSeason:
		// TMDB season payload carries episode metadata too.
		if patch.StillAsset != nil {
			canon.StillAsset = patch.StillAsset
		}
		if patch.TMDBRating != nil {
			canon.TMDBRating = patch.TMDBRating
		}
		if patch.TMDBVotes != nil {
			canon.TMDBVotes = patch.TMDBVotes
		}
		if patch.TMDBEpisodeID != nil {
			canon.TMDBEpisodeID = patch.TMDBEpisodeID
		}
		if patch.TMDBEpisodeNumber != nil {
			canon.TMDBEpisodeNumber = patch.TMDBEpisodeNumber
		}
		// TMDB fallback for Sonarr-priority fields.
		if patch.AirDate != nil && canon.AirDate == nil {
			canon.AirDate = patch.AirDate
		}
		if patch.RuntimeMinutes != nil && canon.RuntimeMinutes == nil {
			canon.RuntimeMinutes = patch.RuntimeMinutes
		}
		if patch.FinaleType != nil && (canon.FinaleType == nil || *canon.FinaleType == "") {
			canon.FinaleType = patch.FinaleType
		}
	}
	return canon
}

// PersonPatch / PersonCanon — §5.4 person rules. All
// person fields are TMDB-only per PRD; the patch struct
// keeps the source-discriminator pattern uniform but
// MergePerson rejects writes from any source != SourceTMDBPerson.
type PersonPatch struct {
	TMDBID             *int
	IMDBID             *string
	Name               *string
	OriginalName       *string
	Gender             *int
	Birthday           *time.Time
	Deathday           *time.Time
	PlaceOfBirth       *string
	KnownForDepartment *string
	Popularity         *float64
	ProfileAsset       *string
}

type PersonCanon struct {
	Hydration          HydrationLevel
	TMDBID             *int
	IMDBID             *string
	Name               string
	OriginalName       *string
	Gender             *int
	Birthday           *time.Time
	Deathday           *time.Time
	PlaceOfBirth       *string
	KnownForDepartment *string
	Popularity         *float64
	ProfileAsset       *string
}

// MergePerson — only SourceTMDBPerson has authority for person
// fields (§5.4: every person column is TMDB-only). Writes from
// other sources are no-ops; the function still returns the canon
// unchanged so callers can chain merge calls without branching.
// A TMDB person write lifts Hydration stub → full.
func MergePerson(canon PersonCanon, patch PersonPatch, source Source) PersonCanon {
	if source != SourceTMDBPerson {
		return canon
	}
	if patch.TMDBID != nil {
		canon.TMDBID = patch.TMDBID
	}
	if patch.IMDBID != nil {
		canon.IMDBID = patch.IMDBID
	}
	if patch.Name != nil {
		canon.Name = *patch.Name
	}
	if patch.OriginalName != nil {
		canon.OriginalName = patch.OriginalName
	}
	if patch.Gender != nil {
		canon.Gender = patch.Gender
	}
	if patch.Birthday != nil {
		canon.Birthday = patch.Birthday
	}
	if patch.Deathday != nil {
		canon.Deathday = patch.Deathday
	}
	if patch.PlaceOfBirth != nil {
		canon.PlaceOfBirth = patch.PlaceOfBirth
	}
	if patch.KnownForDepartment != nil {
		canon.KnownForDepartment = patch.KnownForDepartment
	}
	if patch.Popularity != nil {
		canon.Popularity = patch.Popularity
	}
	if patch.ProfileAsset != nil {
		canon.ProfileAsset = patch.ProfileAsset
	}
	canon.Hydration = LevelFull
	return canon
}
