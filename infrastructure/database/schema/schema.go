// Package schema is the single source-of-truth declarative target schema
// for the seasonfill database. It is consumed by the Atlas CLI at dev-time
// via `atlas migrate diff` to generate per-dialect SQL migrations under
// infrastructure/database/migrations/{postgres,sqlite}/.
//
// Runtime migrations are NOT applied via this package directly — production
// uses golang-migrate to replay the generated SQL files. See PRD §6.6
// Database Portability Contract and §D-1.
//
// Sub-stories 455..461 (D-1-2..D-1-8) populate this schema with the 14
// target tables. D-1-2 (story 455) landed the core series batch:
// series, seasons, episodes. D-1-3a (story 456a) appends the i18n texts
// batch: series_texts, episode_texts. D-1-3b (story 456b) appends the
// taxonomy + join batch: genres, networks, production_companies,
// keywords + 4 i18n siblings + series_genres, series_networks,
// series_companies, series_keywords. D-1-4a (story 457a) appends the
// people canon batch: people, person_credits, person_biographies.
// D-1-4b (story 457b) appends the series extras batch: videos,
// content_ratings, external_ids, series_recommendations.
// D-1-5 (story 458) appends the per-instance projection batch
// (series_cache, episode_states, season_stats) plus the
// enrichment_errors side-table — these are the post-cutover greenfield
// shapes, NOT the historical pre-cutover ones in
// internal/shared/db/migrations/.
// D-1-6a (story 459a) appends series_images — multi-language top-3
// poster/backdrop/logo references with the mediaproxy asset_hash
// column for future asset-store resolution (PRD §4.3).
// D-1-6b (story 459b) appends the admin batch: sonarr_instance,
// instance_secret, app_secret, external_service_config, and
// external_service_quota_state. Encrypted secrets live in side-tables
// (instance_secret, app_secret) keyed by name + surrogate id for FK
// stability across rotation. sonarr_instance and instance_secret have a
// dual FK pattern (back-ref token_secret_id SET NULL, forward-ref
// instance_name CASCADE) documented in the builder comments.
// D-1-7a (story 460a) appends the auth batch: users (with embedded
// preferred_language + avatar_mode + role columns; user_settings is
// NOT a separate table — collapsed for 1:1 cardinality) and
// user_instance_tags (composite PK (user_id, instance_name); CASCADE
// FKs to both parents). user_sessions is NOT in schema — auth is
// stateless cookie HMAC + session_epoch in runtime_config.
package schema

import (
	"fmt"
	"os"
	"strings"

	"ariga.io/atlas/sql/postgres"
	atlasschema "ariga.io/atlas/sql/schema"
	"ariga.io/atlas/sql/sqlite"
)

// SchemaName is exported for tests + Atlas provider integration; mirrors
// the literal passed to atlasschema.New below.
const SchemaName = "public"

// Dialect names the SQL backend Schema is being materialized for.
// The loader binary (infrastructure/database/schema/cmd/loader) reads
// the dialect flag and dispatches to Schema(d); tests call Schema(d)
// directly with the constant they want to inspect.
type Dialect string

const (
	DialectPostgres Dialect = "postgres"
	DialectSQLite   Dialect = "sqlite"
)

// EnvDialect is the env var the loader sets when invoking Load().
// Falls back to DialectPostgres if unset (chosen because it's the
// production target and emits the canonical type names).
const EnvDialect = "ATLAS_DIALECT"

// Load is a convenience entrypoint used by the loader binary and by
// dev-time tools that want a single zero-argument call. Reads
// ATLAS_DIALECT from env and dispatches to Schema(dialect). Panics on
// an unknown dialect rather than silently emitting the wrong DDL.
func Load() *atlasschema.Schema {
	d := Dialect(os.Getenv(EnvDialect))
	if d == "" {
		d = DialectPostgres
	}
	return Schema(d)
}

// Schema returns the declarative target schema for the seasonfill
// database in the given dialect. Tables, columns, and indexes are
// identical in shape across dialects; only type literals (BIGSERIAL vs
// INTEGER AUTOINCREMENT, TIMESTAMPTZ vs TIMESTAMP) and partial-index
// predicate attrs differ.
func Schema(d Dialect) *atlasschema.Schema {
	switch d {
	case DialectPostgres, DialectSQLite:
		// known dialects, fall through
	default:
		panic(fmt.Sprintf("schema: unknown dialect %q (want postgres|sqlite)", d))
	}

	s := atlasschema.New(SchemaName)

	// D-1-2 (story 455) — core series tables.
	addCoreSeries(s, d)

	// D-1-3a (story 456a) — multi-language texts.
	addI18nTexts(s, d)

	// D-1-3b (story 456b) — canonical taxonomy dictionaries + i18n siblings.
	addTaxonomy(s, d)

	// D-1-3b (story 456b) — series ↔ taxonomy join tables.
	//
	// Dev-time split: setting ATLAS_SCHEMA_SKIP_TAXONOMY_JOINS=1 generates
	// the 000003_taxonomy migration without the join tables, so the
	// follow-up `make migrations-diff NAME=taxonomy_joins` produces the
	// 000004 migration with ONLY the join tables. Production runtime never
	// sets this var — production paths always materialize the full schema.
	if os.Getenv("ATLAS_SCHEMA_SKIP_TAXONOMY_JOINS") == "" {
		addTaxonomyJoins(s, d)
	}

	// D-1-4a (story 457a) — canonical people + person_credits +
	// person_biographies.
	//
	// Dev-time split: setting ATLAS_SCHEMA_SKIP_PEOPLE=1 generates earlier
	// migrations without people, so the follow-up
	// `make migrations-diff NAME=people` produces 000005 cleanly. Production
	// runtime never sets this var — production paths always materialize the
	// full schema.
	if os.Getenv("ATLAS_SCHEMA_SKIP_PEOPLE") == "" {
		addPeople(s, d)
	}

	// D-1-4b (story 457b) — videos, content_ratings, external_ids,
	// series_recommendations. Same dev-time split pattern.
	//
	// Dev-time split: setting ATLAS_SCHEMA_SKIP_SERIES_EXTRAS=1 generates
	// earlier migrations without the series extras tables. Production
	// runtime never sets this var.
	if os.Getenv("ATLAS_SCHEMA_SKIP_SERIES_EXTRAS") == "" {
		addSeriesExtras(s, d)
	}

	// D-1-5 (story 458) — per-instance Sonarr projections (series_cache,
	// episode_states, season_stats). Soft-deleted via deleted_at; readers
	// filter `WHERE deleted_at IS NULL`. No FK on instance_name (cascade
	// is app-managed in SonarrInstanceRepository.Delete). episode_states
	// FK → episodes(id), series_cache FK → series(id), both NO ACTION
	// (deletes are soft; a hard DELETE on canon should error rather than
	// silently wipe per-instance projections).
	//
	// Dev-time split: setting ATLAS_SCHEMA_SKIP_INSTANCE_PROJECTIONS=1
	// generates earlier migrations without these three tables.
	if os.Getenv("ATLAS_SCHEMA_SKIP_INSTANCE_PROJECTIONS") == "" {
		addInstanceProjections(s, d)
	}

	// D-1-5 (story 458) — enrichment_errors side-table. POLYMORPHIC: no
	// FK on entity_id (matches external_ids choice from D-1-4b). Tracks
	// per-attempt failures with exponential backoff schedule held in app
	// code (PRD §4.4 nextAttemptAt). NOTE: PRD §D-1 line 4392 also
	// mentions adding `series.enrichment_*_synced_at` columns in 000008
	// — these were moved forward into 000001 during D-1-2 and are
	// already shipped; do NOT re-add or duplicate them here.
	//
	// Dev-time split: setting ATLAS_SCHEMA_SKIP_ENRICHMENT_TRACKING=1
	// generates earlier migrations without this table.
	if os.Getenv("ATLAS_SCHEMA_SKIP_ENRICHMENT_TRACKING") == "" {
		addEnrichmentTracking(s, d)
	}

	// D-1-6a (story 459a) — series_images. Multi-language top-3
	// poster/backdrop/logo references with TMDB ranking signals
	// (vote_average, vote_count) and mediaproxy asset_hash for future
	// resolution from TMDB path → object-store hash. FK CASCADE on
	// series — images are derived enrichment data, dead on canon
	// drop. Language="" is the language-neutral equivalence class
	// (typical for backdrops without overlay text); NULL is NOT used
	// because it would break the UNIQUE composite-4 constraint (NULL
	// is not equal to NULL in SQL).
	//
	// Dev-time split: setting ATLAS_SCHEMA_SKIP_SERIES_IMAGES=1
	// generates earlier migrations without this table.
	if os.Getenv("ATLAS_SCHEMA_SKIP_SERIES_IMAGES") == "" {
		addSeriesImages(s, d)
	}

	// D-1-6b (story 459b) — admin tables: sonarr_instance,
	// instance_secret, app_secret, external_service_config, and
	// external_service_quota_state. 5 tables with a dual FK pattern
	// between sonarr_instance and instance_secret (back-ref SET NULL,
	// forward-ref CASCADE — see addAdmin doc).
	//
	// Dev-time split: setting ATLAS_SCHEMA_SKIP_ADMIN=1 generates
	// earlier migrations without these tables.
	if os.Getenv("ATLAS_SCHEMA_SKIP_ADMIN") == "" {
		addAdmin(s, d)
	}

	// D-1-7a (story 460a) — auth tables: users (with role/avatar_mode
	// CHECK constraints + embedded user_settings columns), and
	// user_instance_tags (composite PK + 2 CASCADE FKs). See addAuth
	// doc for the user_settings collapse decision.
	//
	// Dev-time split: setting ATLAS_SCHEMA_SKIP_AUTH=1 generates
	// earlier migrations without these tables.
	if os.Getenv("ATLAS_SCHEMA_SKIP_AUTH") == "" {
		addAuth(s, d)
	}

	// D-1-7b (story 460b) appends grab tables here.

	return s
}

// addCoreSeries appends series, seasons, episodes (with their indexes
// and FKs) to s. Called from Schema(d).
func addCoreSeries(s *atlasschema.Schema, d Dialect) {
	series := buildSeriesTable(d)
	seasons := buildSeasonsTable(d, series)
	episodes := buildEpisodesTable(d, series, seasons)
	s.AddTables(series, seasons, episodes)
}

// buildSeriesTable assembles the canonical `series` table — 32 columns
// + 6 indexes (4 plain b-tree + 2 partial).
func buildSeriesTable(d Dialect) *atlasschema.Table {
	id := pkColumn(d)
	tmdbID := atlasschema.NewNullIntColumn("tmdb_id", "integer")
	tvdbID := atlasschema.NewNullIntColumn("tvdb_id", "integer")
	imdbID := atlasschema.NewNullStringColumn("imdb_id", "text")
	hydration := atlasschema.NewStringColumn("hydration", "text").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "'stub'"})
	title := atlasschema.NewStringColumn("title", "text").SetNull(false)
	originalTitle := atlasschema.NewNullStringColumn("original_title", "text")
	status := atlasschema.NewNullStringColumn("status", "text")
	firstAirDate := dateColumn(d, "first_air_date")
	lastAirDate := dateColumn(d, "last_air_date")
	nextAirDate := timestampColumn(d, "next_air_date", false, false)
	year := atlasschema.NewNullIntColumn("year", "integer")
	runtimeMinutes := atlasschema.NewNullIntColumn("runtime_minutes", "integer")
	homepage := atlasschema.NewNullStringColumn("homepage", "text")
	originalLanguage := atlasschema.NewNullStringColumn("original_language", "text")
	originCountry := atlasschema.NewNullStringColumn("origin_country", "text")
	originCountries := atlasschema.NewStringColumn("origin_countries", "text").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "'[]'"})
	tmdbType := atlasschema.NewNullIntColumn("tmdb_type", "integer")
	popularity := atlasschema.NewNullFloatColumn("popularity", "double precision")
	inProduction := atlasschema.NewBoolColumn("in_production", "boolean").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "false"})
	posterAsset := atlasschema.NewNullStringColumn("poster_asset", "text")
	backdropAsset := atlasschema.NewNullStringColumn("backdrop_asset", "text")
	tmdbRating := atlasschema.NewNullFloatColumn("tmdb_rating", "double precision")
	tmdbVotes := atlasschema.NewNullIntColumn("tmdb_votes", "integer")
	imdbRating := atlasschema.NewNullFloatColumn("imdb_rating", "double precision")
	imdbVotes := atlasschema.NewNullIntColumn("imdb_votes", "integer")
	omdbRated := atlasschema.NewNullStringColumn("omdb_rated", "text")
	omdbAwards := atlasschema.NewNullStringColumn("omdb_awards", "text")
	enrichmentTMDBSyncedAt := timestampColumn(d, "enrichment_tmdb_synced_at", false, false)
	enrichmentOMDBSyncedAt := timestampColumn(d, "enrichment_omdb_synced_at", false, false)
	createdAt := timestampColumn(d, "created_at", true, true)
	updatedAt := timestampColumn(d, "updated_at", true, true)

	t := atlasschema.NewTable("series").
		AddColumns(
			id,
			tmdbID,
			tvdbID,
			imdbID,
			hydration,
			title,
			originalTitle,
			status,
			firstAirDate,
			lastAirDate,
			nextAirDate,
			year,
			runtimeMinutes,
			homepage,
			originalLanguage,
			originCountry,
			originCountries,
			tmdbType,
			popularity,
			inProduction,
			posterAsset,
			backdropAsset,
			tmdbRating,
			tmdbVotes,
			imdbRating,
			imdbVotes,
			omdbRated,
			omdbAwards,
			enrichmentTMDBSyncedAt,
			enrichmentOMDBSyncedAt,
			createdAt,
			updatedAt,
		).
		SetPrimaryKey(atlasschema.NewPrimaryKey(id)).
		AddIndexes(
			partialUniqueIndex(d, "series_tmdb_id_idx",
				[]*atlasschema.Column{tmdbID},
				"tmdb_id IS NOT NULL"),
			atlasschema.NewIndex("series_imdb_id_idx").
				AddColumns(imdbID),
			atlasschema.NewIndex("series_tvdb_id_idx").
				AddColumns(tvdbID),
			descIndex("series_popularity_idx", popularity),
			descIndex("series_tmdb_rating_idx", tmdbRating),
			partialIndex(d, "series_tmdb_type_idx",
				[]*atlasschema.Column{tmdbType},
				"tmdb_type IS NOT NULL"),
		)
	return t
}

// buildSeasonsTable assembles the canonical `seasons` table — 11 columns
// + seasons_natural unique index + FK series_id → series.id (no cascade).
func buildSeasonsTable(d Dialect, seriesTable *atlasschema.Table) *atlasschema.Table {
	id := pkColumn(d)
	seriesID := fkColumn(d, "series_id", false /* not null */)
	seasonNumber := atlasschema.NewIntColumn("season_number", "integer").SetNull(false)
	tmdbSeasonID := atlasschema.NewNullIntColumn("tmdb_season_id", "integer")
	name := atlasschema.NewNullStringColumn("name", "text")
	overview := atlasschema.NewNullStringColumn("overview", "text")
	airDate := dateColumn(d, "air_date")
	episodeCount := atlasschema.NewNullIntColumn("episode_count", "integer")
	posterAsset := atlasschema.NewNullStringColumn("poster_asset", "text")
	createdAt := timestampColumn(d, "created_at", true, true)
	updatedAt := timestampColumn(d, "updated_at", true, true)

	t := atlasschema.NewTable("seasons").
		AddColumns(
			id,
			seriesID,
			seasonNumber,
			tmdbSeasonID,
			name,
			overview,
			airDate,
			episodeCount,
			posterAsset,
			createdAt,
			updatedAt,
		).
		SetPrimaryKey(atlasschema.NewPrimaryKey(id)).
		AddIndexes(
			atlasschema.NewUniqueIndex("seasons_natural").
				AddColumns(seriesID, seasonNumber),
		).
		AddForeignKeys(
			atlasschema.NewForeignKey("seasons_series_id_fkey").
				AddColumns(seriesID).
				SetRefTable(seriesTable).
				AddRefColumns(seriesTable.Columns[0]).
				SetOnDelete(atlasschema.NoAction).
				SetOnUpdate(atlasschema.NoAction),
		)
	return t
}

// buildEpisodesTable assembles the canonical `episodes` table — 17 columns
// + 2 indexes (episodes_natural unique + episodes_air_date b-tree) + 2 FKs
// (series_id → series.id, season_id → seasons.id, both no-cascade).
func buildEpisodesTable(d Dialect, seriesTable, seasonsTable *atlasschema.Table) *atlasschema.Table {
	id := pkColumn(d)
	seriesID := fkColumn(d, "series_id", false /* not null */)
	seasonID := fkColumn(d, "season_id", true /* nullable per legacy 000026 */)
	seasonNumber := atlasschema.NewIntColumn("season_number", "integer").SetNull(false)
	episodeNumber := atlasschema.NewIntColumn("episode_number", "integer").SetNull(false)
	tmdbEpisodeNumber := atlasschema.NewNullIntColumn("tmdb_episode_number", "integer")
	tmdbEpisodeID := atlasschema.NewNullIntColumn("tmdb_episode_id", "integer")
	sonarrEpisodeID := atlasschema.NewNullIntColumn("sonarr_episode_id", "integer")
	absoluteNumber := atlasschema.NewNullIntColumn("absolute_number", "integer")
	airDate := timestampColumn(d, "air_date", false, false)
	runtimeMinutes := atlasschema.NewNullIntColumn("runtime_minutes", "integer")
	finaleType := atlasschema.NewNullStringColumn("finale_type", "text")
	stillAsset := atlasschema.NewNullStringColumn("still_asset", "text")
	tmdbRating := atlasschema.NewNullFloatColumn("tmdb_rating", "double precision")
	tmdbVotes := atlasschema.NewNullIntColumn("tmdb_votes", "integer")
	createdAt := timestampColumn(d, "created_at", true, true)
	updatedAt := timestampColumn(d, "updated_at", true, true)

	t := atlasschema.NewTable("episodes").
		AddColumns(
			id,
			seriesID,
			seasonID,
			seasonNumber,
			episodeNumber,
			tmdbEpisodeNumber,
			tmdbEpisodeID,
			sonarrEpisodeID,
			absoluteNumber,
			airDate,
			runtimeMinutes,
			finaleType,
			stillAsset,
			tmdbRating,
			tmdbVotes,
			createdAt,
			updatedAt,
		).
		SetPrimaryKey(atlasschema.NewPrimaryKey(id)).
		AddIndexes(
			atlasschema.NewUniqueIndex("episodes_natural").
				AddColumns(seriesID, seasonNumber, episodeNumber),
			atlasschema.NewIndex("episodes_air_date").
				AddColumns(airDate),
		).
		AddForeignKeys(
			atlasschema.NewForeignKey("episodes_series_id_fkey").
				AddColumns(seriesID).
				SetRefTable(seriesTable).
				AddRefColumns(seriesTable.Columns[0]).
				SetOnDelete(atlasschema.NoAction).
				SetOnUpdate(atlasschema.NoAction),
			atlasschema.NewForeignKey("episodes_season_id_fkey").
				AddColumns(seasonID).
				SetRefTable(seasonsTable).
				AddRefColumns(seasonsTable.Columns[0]).
				SetOnDelete(atlasschema.NoAction).
				SetOnUpdate(atlasschema.NoAction),
		)
	return t
}

// addI18nTexts appends series_texts and episode_texts (per-parent
// per-language text fan-out tables) to s. Called from Schema(d) after
// addCoreSeries — episode_texts depends on the episodes table created
// in D-1-2.
//
// PRD §4.3 — multi-language storage. Each (parent_id, language) row
// holds the localized strings (title, overview, [tagline]). enriched_at
// tracks when TMDB API was called for this language; updated_at tracks
// when the row was last written (regardless of source).
func addI18nTexts(s *atlasschema.Schema, d Dialect) {
	series := mustTable(s, "series")
	episodes := mustTable(s, "episodes")
	s.AddTables(
		buildSeriesTextsTable(d, series),
		buildEpisodeTextsTable(d, episodes),
	)
}

// buildSeriesTextsTable returns the series_texts table:
//
//	PK (series_id, language)
//	columns: title text NULL, overview text NULL, tagline text NULL,
//	         enriched_at timestamptz NULL, updated_at timestamptz NOT NULL DEFAULT now()
//	FK series_id → series(id) NO ACTION (canonical-to-canonical)
func buildSeriesTextsTable(d Dialect, seriesTable *atlasschema.Table) *atlasschema.Table {
	return i18nTextTable(d, "series_texts", seriesTable, "series_id",
		[]*atlasschema.Column{
			atlasschema.NewNullStringColumn("title", "text"),
			atlasschema.NewNullStringColumn("overview", "text"),
			atlasschema.NewNullStringColumn("tagline", "text"),
		},
		"",   // no (language, name) lookup index
		true, // include enriched_at
	)
}

// buildEpisodeTextsTable returns the episode_texts table:
//
//	PK (episode_id, language)
//	columns: title text NULL, overview text NULL,
//	         enriched_at timestamptz NULL, updated_at timestamptz NOT NULL DEFAULT now()
//	FK episode_id → episodes(id) NO ACTION
func buildEpisodeTextsTable(d Dialect, episodesTable *atlasschema.Table) *atlasschema.Table {
	return i18nTextTable(d, "episode_texts", episodesTable, "episode_id",
		[]*atlasschema.Column{
			atlasschema.NewNullStringColumn("title", "text"),
			atlasschema.NewNullStringColumn("overview", "text"),
		},
		"",
		true,
	)
}

// addTaxonomy appends the 4 canonical taxonomy dictionaries (genres,
// networks, production_companies, keywords) and their 4 per-language
// i18n sibling tables to s. Called from Schema(d) after addI18nTexts.
//
// PRD §4.3 / §D-1 line 4387. Canonical tables hold the language-neutral
// shape (id + tmdb_id + optional canonical columns + timestamps). i18n
// siblings hold the localized name/description per language. The
// (language, name) lookup index on each i18n sibling supports the PRD
// §5.4 Sonarr-genre fallback path ("resolve 'Drama' + 'en-US' → genre id").
func addTaxonomy(s *atlasschema.Schema, d Dialect) {
	genres := buildGenresTable(d)
	networks := buildNetworksTable(d)
	companies := buildProductionCompaniesTable(d)
	keywords := buildKeywordsTable(d)
	s.AddTables(
		genres,
		networks,
		companies,
		keywords,
		i18nTextTable(d, "genres_i18n", genres, "genre_id",
			[]*atlasschema.Column{
				atlasschema.NewStringColumn("name", "text").SetNull(false),
			},
			"genres_i18n_name", // (language, name) lookup
			false,              // no enriched_at on taxonomy i18n
		),
		i18nTextTable(d, "networks_i18n", networks, "network_id",
			[]*atlasschema.Column{
				atlasschema.NewStringColumn("name", "text").SetNull(false),
				atlasschema.NewNullStringColumn("description", "text"),
			},
			"networks_i18n_name",
			false,
		),
		i18nTextTable(d, "production_companies_i18n", companies, "company_id",
			[]*atlasschema.Column{
				atlasschema.NewStringColumn("name", "text").SetNull(false),
				atlasschema.NewNullStringColumn("description", "text"),
			},
			"production_companies_i18n_name",
			false,
		),
		i18nTextTable(d, "keywords_i18n", keywords, "keyword_id",
			[]*atlasschema.Column{
				atlasschema.NewStringColumn("name", "text").SetNull(false),
			},
			"keywords_i18n_name",
			false,
		),
	)
}

// addTaxonomyJoins appends the 4 series ↔ taxonomy join tables. Called
// from Schema(d) after addTaxonomy.
//
// FK direction (see Investigation Notes / PRD §D-1 line 4408):
//   - series-side FK ON DELETE CASCADE (a join row has no meaning when
//     its series is gone — joins are projections, not canonical data)
//   - taxonomy-side FK ON DELETE NO ACTION (prevent dropping a genre
//     while series still reference it)
//
// series_keywords omits the position column (keywords are unordered per
// legacy 000028 line 92); the other 3 joins keep position INTEGER NULL
// to preserve TMDB display order.
func addTaxonomyJoins(s *atlasschema.Schema, d Dialect) {
	series := mustTable(s, "series")
	genres := mustTable(s, "genres")
	networks := mustTable(s, "networks")
	companies := mustTable(s, "production_companies")
	keywords := mustTable(s, "keywords")
	s.AddTables(
		joinTable(d, "series_genres", "series_id", series, "genre_id", genres, true /* position */),
		joinTable(d, "series_networks", "series_id", series, "network_id", networks, true),
		joinTable(d, "series_companies", "series_id", series, "company_id", companies, true),
		joinTable(d, "series_keywords", "series_id", series, "keyword_id", keywords, false /* no position */),
	)
}

// buildGenresTable returns the canonical genres table (id + tmdb_id +
// timestamps; 4 cols). Localized names live in the genres_i18n sibling.
func buildGenresTable(d Dialect) *atlasschema.Table {
	return canonDictTable(d, "genres", nil)
}

// buildKeywordsTable returns the canonical keywords table (4 cols).
// Localized names live in the keywords_i18n sibling.
func buildKeywordsTable(d Dialect) *atlasschema.Table {
	return canonDictTable(d, "keywords", nil)
}

// buildNetworksTable returns the canonical networks table (7 cols:
// + name + logo_asset + origin_country on top of the canonDictTable
// shape). Localized name + description live in networks_i18n.
func buildNetworksTable(d Dialect) *atlasschema.Table {
	return canonDictTable(d, "networks", []*atlasschema.Column{
		atlasschema.NewStringColumn("name", "text").SetNull(false),
		atlasschema.NewNullStringColumn("logo_asset", "text"),
		atlasschema.NewNullStringColumn("origin_country", "text"),
	})
}

// buildProductionCompaniesTable returns the canonical
// production_companies table (same shape as networks; 7 cols).
// Localized name + description live in production_companies_i18n.
func buildProductionCompaniesTable(d Dialect) *atlasschema.Table {
	return canonDictTable(d, "production_companies", []*atlasschema.Column{
		atlasschema.NewStringColumn("name", "text").SetNull(false),
		atlasschema.NewNullStringColumn("logo_asset", "text"),
		atlasschema.NewNullStringColumn("origin_country", "text"),
	})
}

// canonDictTable builds a "canonical dictionary" table:
//
//	id PK + tmdb_id NULL + extraCols + created_at + updated_at
//	plus a UNIQUE partial index on tmdb_id WHERE tmdb_id IS NOT NULL,
//	named "<name>_tmdb_id".
//
// The partial-unique on tmdb_id allows multiple rows with NULL tmdb_id
// (e.g., manually-seeded fallbacks) while still enforcing one row per
// TMDB id for rows the worker resolves from TMDB.
//
// Reused 4× by genres / keywords (no extraCols) and networks /
// production_companies (3 extraCols: name, logo_asset, origin_country).
func canonDictTable(d Dialect, name string, extraCols []*atlasschema.Column) *atlasschema.Table {
	id := pkColumn(d)
	tmdbID := atlasschema.NewNullIntColumn("tmdb_id", "integer")
	createdAt := timestampColumn(d, "created_at", true /* withDefault */, true /* notNull */)
	updatedAt := timestampColumn(d, "updated_at", true, true)

	cols := []*atlasschema.Column{id, tmdbID}
	cols = append(cols, extraCols...)
	cols = append(cols, createdAt, updatedAt)

	return atlasschema.NewTable(name).
		AddColumns(cols...).
		SetPrimaryKey(atlasschema.NewPrimaryKey(id)).
		AddIndexes(partialUniqueIndex(d, name+"_tmdb_id",
			[]*atlasschema.Column{tmdbID}, "tmdb_id IS NOT NULL"))
}

// joinTable builds a series ↔ taxonomy join table:
//
//	PK (leftColName, rightColName)
//	columns: leftColName, rightColName, [position integer NULL]
//	FK left → leftTable(id)  ON DELETE CASCADE  (series-side)
//	FK right → rightTable(id) ON DELETE NO ACTION (taxonomy-side)
//	Reverse-lookup index on right column, named
//	"<name>_<TrimSuffix(rightColName, "_id")>" — e.g.,
//	"series_genres_genre" for series_genres.genre_id.
//
// withPosition=true adds the `position` column (used for genres,
// networks, companies); false omits it (used for keywords per legacy
// 000028 line 92). The index is added BEFORE the FKs so the emitted
// SQL puts CREATE INDEX right after CREATE TABLE — matches Atlas's
// canonical ordering and keeps the generated diff deterministic.
func joinTable(
	d Dialect,
	name string,
	leftColName string,
	leftTable *atlasschema.Table,
	rightColName string,
	rightTable *atlasschema.Table,
	withPosition bool,
) *atlasschema.Table {
	leftID := fkColumn(d, leftColName, false /* not null */)
	rightID := fkColumn(d, rightColName, false)

	cols := []*atlasschema.Column{leftID, rightID}
	if withPosition {
		cols = append(cols, atlasschema.NewNullIntColumn("position", "integer"))
	}

	leftRef := parentRefCol(leftTable)
	rightRef := parentRefCol(rightTable)
	indexName := name + "_" + strings.TrimSuffix(rightColName, "_id")

	return atlasschema.NewTable(name).
		AddColumns(cols...).
		SetPrimaryKey(atlasschema.NewPrimaryKey(leftID, rightID)).
		AddIndexes(
			atlasschema.NewIndex(indexName).AddColumns(rightID),
		).
		AddForeignKeys(
			atlasschema.NewForeignKey(name+"_"+leftColName+"_fkey").
				AddColumns(leftID).
				SetRefTable(leftTable).
				AddRefColumns(leftRef).
				SetOnDelete(atlasschema.Cascade).
				SetOnUpdate(atlasschema.NoAction),
			atlasschema.NewForeignKey(name+"_"+rightColName+"_fkey").
				AddColumns(rightID).
				SetRefTable(rightTable).
				AddRefColumns(rightRef).
				SetOnDelete(atlasschema.NoAction).
				SetOnUpdate(atlasschema.NoAction),
		)
}

// addPeople appends the people canon dictionary + person_credits
// (TMDB filmography materialization) + person_biographies (i18n bio
// fan-out). Called from Schema(d) after addTaxonomyJoins.
//
// PRD §4.3 / §D-1 line 4389. Sourced from legacy migrations 000027 +
// 000030 + 000038 (consolidated final shape — 000038 added 3 fields
// to person_credits the mapper was silently dropping).
//
// FK cascade: person_biographies + person_credits use NoAction (canon-
// to-canon). Operator never deletes person rows directly in practice;
// if it ever happens, the children block on FK — that's the desired
// safety net.
func addPeople(s *atlasschema.Schema, d Dialect) {
	people := buildPeopleTable(d)
	s.AddTables(
		people,
		buildPersonCreditsTable(d, people),
		// person_biographies — i18n shape (person_id, language) PK,
		// single per-language `biography` text column. Reuses the
		// D-1-3a i18nTextTable helper (no nameLookupIdx, no enriched_at).
		i18nTextTable(d, "person_biographies", people, "person_id",
			[]*atlasschema.Column{atlasschema.NewNullStringColumn("biography", "text")},
			"", false),
	)
}

// buildPeopleTable returns the canonical `people` table — 15 cols + 2
// indexes (partial unique on tmdb_id; plain on imdb_id).
//
// Greenfield deviation from legacy 000027: gender is `integer` (was
// smallint) — portable to SQLite without Atlas-side dialect mapping.
// All other columns match legacy verbatim.
//
// Dedicated builder (NOT canonDictTable): people has 14 data columns
// with a date type (birthday/deathday) and an imdb_id plain index — the
// shape doesn't fit canonDictTable's thin contract.
func buildPeopleTable(d Dialect) *atlasschema.Table {
	id := pkColumn(d)
	tmdbID := atlasschema.NewNullIntColumn("tmdb_id", "integer")
	imdbID := atlasschema.NewNullStringColumn("imdb_id", "text")
	hydration := atlasschema.NewStringColumn("hydration", "text").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "'stub'"})
	name := atlasschema.NewStringColumn("name", "text").SetNull(false)
	originalName := atlasschema.NewNullStringColumn("original_name", "text")
	gender := atlasschema.NewNullIntColumn("gender", "integer")
	birthday := dateColumn(d, "birthday")
	deathday := dateColumn(d, "deathday")
	placeOfBirth := atlasschema.NewNullStringColumn("place_of_birth", "text")
	knownForDept := atlasschema.NewNullStringColumn("known_for_department", "text")
	popularity := atlasschema.NewNullFloatColumn("popularity", "double precision")
	profileAsset := atlasschema.NewNullStringColumn("profile_asset", "text")
	createdAt := timestampColumn(d, "created_at", true, true)
	updatedAt := timestampColumn(d, "updated_at", true, true)

	return atlasschema.NewTable("people").
		AddColumns(
			id, tmdbID, imdbID, hydration, name, originalName, gender,
			birthday, deathday, placeOfBirth, knownForDept, popularity,
			profileAsset, createdAt, updatedAt,
		).
		SetPrimaryKey(atlasschema.NewPrimaryKey(id)).
		AddIndexes(
			partialUniqueIndex(d, "people_tmdb_id",
				[]*atlasschema.Column{tmdbID}, "tmdb_id IS NOT NULL"),
			atlasschema.NewIndex("people_imdb_id").AddColumns(imdbID),
		)
}

// buildPersonCreditsTable returns person_credits — 18 cols + 3 indexes
// + FK person_id → people(id) NoAction. PRD §5.3 row "person_credits"
// + legacy 000027/000030 + 000038 (department/original_title/tmdb_votes
// addition).
//
// Greenfield deviation from legacy: department/original_title use `text`
// (legacy used varchar(64)/varchar(255) — N/A on SQLite, redundant on
// PG). See PRD §6.7 portability rule.
func buildPersonCreditsTable(d Dialect, peopleTable *atlasschema.Table) *atlasschema.Table {
	id := pkColumn(d)
	personID := fkColumn(d, "person_id", false)
	tmdbCreditID := atlasschema.NewStringColumn("tmdb_credit_id", "text").SetNull(false)
	mediaType := atlasschema.NewStringColumn("media_type", "text").SetNull(false)
	tmdbMediaID := atlasschema.NewIntColumn("tmdb_media_id", "integer").SetNull(false)
	title := atlasschema.NewStringColumn("title", "text").SetNull(false)
	originalTitle := atlasschema.NewNullStringColumn("original_title", "text")
	year := atlasschema.NewNullIntColumn("year", "integer")
	characterName := atlasschema.NewNullStringColumn("character_name", "text")
	kind := atlasschema.NewStringColumn("kind", "text").SetNull(false)
	department := atlasschema.NewNullStringColumn("department", "text")
	job := atlasschema.NewNullStringColumn("job", "text")
	posterPath := atlasschema.NewNullStringColumn("poster_path", "text")
	voteAverage := atlasschema.NewNullFloatColumn("vote_average", "double precision")
	tmdbVotes := atlasschema.NewNullIntColumn("tmdb_votes", "integer")
	episodeCount := atlasschema.NewNullIntColumn("episode_count", "integer")
	createdAt := timestampColumn(d, "created_at", true, true)
	updatedAt := timestampColumn(d, "updated_at", true, true)

	return atlasschema.NewTable("person_credits").
		AddColumns(
			id, personID, tmdbCreditID, mediaType, tmdbMediaID, title,
			originalTitle, year, characterName, kind, department, job,
			posterPath, voteAverage, tmdbVotes, episodeCount,
			createdAt, updatedAt,
		).
		SetPrimaryKey(atlasschema.NewPrimaryKey(id)).
		AddIndexes(
			atlasschema.NewUniqueIndex("person_credits_credit").
				AddColumns(personID, tmdbCreditID),
			atlasschema.NewIndex("person_credits_media").
				AddColumns(mediaType, tmdbMediaID),
			atlasschema.NewIndex("person_credits_person").
				AddColumns(personID),
		).
		AddForeignKeys(
			atlasschema.NewForeignKey("person_credits_person_id_fkey").
				AddColumns(personID).
				SetRefTable(peopleTable).
				AddRefColumns(parentRefCol(peopleTable)).
				SetOnDelete(atlasschema.NoAction).
				SetOnUpdate(atlasschema.NoAction),
		)
}

// addSeriesExtras appends the 4 series-extras tables (videos,
// content_ratings, external_ids, series_recommendations). Called from
// Schema(d) after addPeople.
//
// PRD §4.3 / §D-1 line 4390. Sourced from legacy migrations 000029.
//
// FK direction:
//   - videos / content_ratings / series_recommendations.{series_id,
//     recommended_series_id} → series(id) ON DELETE CASCADE (per-series
//     extensions, dead without parent).
//   - external_ids has NO FK on entity_id (polymorphic — entity_type
//     discriminates). This is a deliberate schema choice; do NOT add an
//     FK by reflex.
func addSeriesExtras(s *atlasschema.Schema, d Dialect) {
	series := mustTable(s, "series")
	s.AddTables(
		buildVideosTable(d, series),
		buildContentRatingsTable(d, series),
		buildExternalIDsTable(d), // polymorphic, no FK
		buildSeriesRecommendationsTable(d, series),
	)
}

// buildVideosTable returns the videos table — 12 cols + 2 indexes
// (partial unique on tmdb_video_id; composite series/type/official).
// FK series_id → series(id) ON DELETE CASCADE.
func buildVideosTable(d Dialect, seriesTable *atlasschema.Table) *atlasschema.Table {
	id := pkColumn(d)
	seriesID := fkColumn(d, "series_id", false)
	tmdbVideoID := atlasschema.NewNullStringColumn("tmdb_video_id", "text")
	name := atlasschema.NewStringColumn("name", "text").SetNull(false)
	site := atlasschema.NewNullStringColumn("site", "text")
	key := atlasschema.NewNullStringColumn("key", "text")
	typeCol := atlasschema.NewNullStringColumn("type", "text")
	official := atlasschema.NewBoolColumn("official", "boolean").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "false"})
	language := atlasschema.NewNullStringColumn("language", "text")
	publishedAt := timestampColumn(d, "published_at", false, false)
	createdAt := timestampColumn(d, "created_at", true, true)
	updatedAt := timestampColumn(d, "updated_at", true, true)

	return atlasschema.NewTable("videos").
		AddColumns(id, seriesID, tmdbVideoID, name, site, key, typeCol,
			official, language, publishedAt, createdAt, updatedAt).
		SetPrimaryKey(atlasschema.NewPrimaryKey(id)).
		AddIndexes(
			partialUniqueIndex(d, "videos_tmdb_id",
				[]*atlasschema.Column{tmdbVideoID}, "tmdb_video_id IS NOT NULL"),
			atlasschema.NewIndex("videos_series_type").
				AddColumns(seriesID, typeCol, official),
		).
		AddForeignKeys(
			atlasschema.NewForeignKey("videos_series_id_fkey").
				AddColumns(seriesID).
				SetRefTable(seriesTable).
				AddRefColumns(parentRefCol(seriesTable)).
				SetOnDelete(atlasschema.Cascade).
				SetOnUpdate(atlasschema.NoAction),
		)
}

// buildContentRatingsTable returns content_ratings — 4 cols, composite
// PK (series_id, country_code), FK series_id → series(id) CASCADE.
//
// Thin composite-PK child WITHOUT a separate `id` column — natural key
// is the (series_id, country_code) pair. First table with this shape in
// the schema (i18n tables also use composite PK but add nullable text
// cols + enriched_at).
func buildContentRatingsTable(d Dialect, seriesTable *atlasschema.Table) *atlasschema.Table {
	seriesID := fkColumn(d, "series_id", false)
	countryCode := atlasschema.NewStringColumn("country_code", "text").SetNull(false)
	rating := atlasschema.NewStringColumn("rating", "text").SetNull(false)
	updatedAt := timestampColumn(d, "updated_at", true, true)

	return atlasschema.NewTable("content_ratings").
		AddColumns(seriesID, countryCode, rating, updatedAt).
		SetPrimaryKey(atlasschema.NewPrimaryKey(seriesID, countryCode)).
		AddForeignKeys(
			atlasschema.NewForeignKey("content_ratings_series_id_fkey").
				AddColumns(seriesID).
				SetRefTable(seriesTable).
				AddRefColumns(parentRefCol(seriesTable)).
				SetOnDelete(atlasschema.Cascade).
				SetOnUpdate(atlasschema.NoAction),
		)
}

// buildExternalIDsTable returns external_ids — 5 cols, composite-3 PK
// (entity_type, entity_id, provider). POLYMORPHIC: entity_id has NO FK
// constraint. PRD §5.3 documents this. entity_type domain (series|
// person|episode) is enforced at the domain layer via the
// enrichment.EntityType enum, NOT by DB constraint — keeps the table
// schema-portable to future entity types without migration.
//
// Index external_ids_provider_value on (provider, value) — reverse
// lookup "find anything matching imdb=tt1234567".
//
// DO NOT add an FK on entity_id by reflex — the absence is intentional.
func buildExternalIDsTable(d Dialect) *atlasschema.Table {
	entityType := atlasschema.NewStringColumn("entity_type", "text").SetNull(false)
	entityID := atlasschema.NewIntColumn("entity_id", "bigint").SetNull(false)
	provider := atlasschema.NewStringColumn("provider", "text").SetNull(false)
	value := atlasschema.NewStringColumn("value", "text").SetNull(false)
	updatedAt := timestampColumn(d, "updated_at", true, true)

	return atlasschema.NewTable("external_ids").
		AddColumns(entityType, entityID, provider, value, updatedAt).
		SetPrimaryKey(atlasschema.NewPrimaryKey(entityType, entityID, provider)).
		AddIndexes(
			atlasschema.NewIndex("external_ids_provider_value").
				AddColumns(provider, value),
		)
}

// buildSeriesRecommendationsTable returns series_recommendations — 4
// cols, composite PK (series_id, recommended_series_id), 2 FKs (BOTH
// CASCADE — self-joining table, dead if either side is wiped).
//
// PRD §4.3 / §D-1 line 4390. Legacy 000029 shape; PRD §5.1.3 mentioned
// `kind` + `refreshed_at` as forward-looking additions — deliberately
// NOT in greenfield (single position-ordered list per series_id;
// composers consume the 4-col shape successfully).
//
// DO NOT re-add kind/refreshed_at by reflex; if a future story needs
// them it's a column addition (000015+).
func buildSeriesRecommendationsTable(d Dialect, seriesTable *atlasschema.Table) *atlasschema.Table {
	seriesID := fkColumn(d, "series_id", false)
	recommendedID := fkColumn(d, "recommended_series_id", false)
	position := atlasschema.NewNullIntColumn("position", "integer")
	updatedAt := timestampColumn(d, "updated_at", true, true)

	refCol := parentRefCol(seriesTable)
	return atlasschema.NewTable("series_recommendations").
		AddColumns(seriesID, recommendedID, position, updatedAt).
		SetPrimaryKey(atlasschema.NewPrimaryKey(seriesID, recommendedID)).
		AddIndexes(
			atlasschema.NewIndex("series_recommendations_position").
				AddColumns(seriesID, position),
		).
		AddForeignKeys(
			atlasschema.NewForeignKey("series_recommendations_series_id_fkey").
				AddColumns(seriesID).
				SetRefTable(seriesTable).
				AddRefColumns(refCol).
				SetOnDelete(atlasschema.Cascade).
				SetOnUpdate(atlasschema.NoAction),
			atlasschema.NewForeignKey("series_recommendations_recommended_series_id_fkey").
				AddColumns(recommendedID).
				SetRefTable(seriesTable).
				AddRefColumns(refCol).
				SetOnDelete(atlasschema.Cascade).
				SetOnUpdate(atlasschema.NoAction),
		)
}

// ----------------------------------------------------------------------
// Helpers — dialect-aware column / index constructors.
// ----------------------------------------------------------------------

// pkColumn returns the integer PK column for the given dialect.
//
//	Postgres: id BIGSERIAL PRIMARY KEY  (via column type "bigserial")
//	SQLite:   id INTEGER PRIMARY KEY AUTOINCREMENT  (via type "integer" + AutoIncrement attr)
func pkColumn(d Dialect) *atlasschema.Column {
	switch d {
	case DialectPostgres:
		return atlasschema.NewIntColumn("id", "bigserial").SetNull(false)
	case DialectSQLite:
		c := atlasschema.NewIntColumn("id", "integer").SetNull(false)
		c.AddAttrs(&sqlite.AutoIncrement{})
		return c
	}
	panic(fmt.Sprintf("pkColumn: unknown dialect %q", d))
}

// timestampColumn returns a timestamp-with-tz column.
//
//	Postgres: timestamptz [NOT NULL] [DEFAULT now()]
//	SQLite:   datetime    [NOT NULL] [DEFAULT CURRENT_TIMESTAMP]
func timestampColumn(d Dialect, name string, withDefault, notNull bool) *atlasschema.Column {
	typ := "timestamptz"
	var defExpr atlasschema.Expr = &atlasschema.RawExpr{X: "now()"}
	if d == DialectSQLite {
		typ = "datetime"
		defExpr = &atlasschema.RawExpr{X: "CURRENT_TIMESTAMP"}
	}
	var c *atlasschema.Column
	if notNull {
		c = atlasschema.NewTimeColumn(name, typ).SetNull(false)
	} else {
		c = atlasschema.NewNullTimeColumn(name, typ)
	}
	if withDefault {
		c.SetDefault(defExpr)
	}
	return c
}

// dateColumn returns a nullable DATE column. PRD §4.1 has all date
// columns nullable (first_air_date / last_air_date / air_date).
func dateColumn(_ Dialect, name string) *atlasschema.Column {
	return atlasschema.NewNullTimeColumn(name, "date")
}

// fkColumn returns a foreign-key column (matches the PK shape: bigint
// on Postgres, integer on SQLite — both equivalent for INTEGER
// AUTOINCREMENT / BIGSERIAL natural-row counters).
func fkColumn(d Dialect, name string, nullable bool) *atlasschema.Column {
	typ := "bigint"
	if d == DialectSQLite {
		typ = "integer"
	}
	if nullable {
		return atlasschema.NewNullIntColumn(name, typ)
	}
	return atlasschema.NewIntColumn(name, typ).SetNull(false)
}

// descIndex returns a non-unique descending index over a single column.
// Atlas emits the dialect-appropriate DESC NULLS LAST syntax — Postgres
// supports it natively, SQLite 3.30+ as well (modernc.org/sqlite ships 3.45).
func descIndex(name string, col *atlasschema.Column) *atlasschema.Index {
	return atlasschema.NewIndex(name).AddParts(
		atlasschema.NewColumnPart(col).SetDesc(true),
	)
}

// partialUniqueIndex builds a UNIQUE partial index with the dialect-
// specific IndexPredicate attribute.
func partialUniqueIndex(d Dialect, name string, cols []*atlasschema.Column, predicate string) *atlasschema.Index {
	idx := atlasschema.NewUniqueIndex(name).AddColumns(cols...)
	attachPredicate(d, idx, predicate)
	return idx
}

// partialIndex builds a non-unique partial index with the dialect-
// specific IndexPredicate attribute.
func partialIndex(d Dialect, name string, cols []*atlasschema.Column, predicate string) *atlasschema.Index {
	idx := atlasschema.NewIndex(name).AddColumns(cols...)
	attachPredicate(d, idx, predicate)
	return idx
}

// attachPredicate adds the IndexPredicate attr in the right dialect package.
func attachPredicate(d Dialect, idx *atlasschema.Index, predicate string) {
	switch d {
	case DialectPostgres:
		idx.AddAttrs(&postgres.IndexPredicate{P: predicate})
	case DialectSQLite:
		idx.AddAttrs(&sqlite.IndexPredicate{P: predicate})
	}
}

// i18nTextTable builds a per-parent per-language i18n table:
//
//	PK (parent_id, language)
//	columns: extraCols + [enriched_at NULL] + updated_at NOT NULL DEFAULT now()
//	FK parent_id → parentTable(id) NO ACTION  (parent PK column derived from
//	parentTable.PrimaryKey.Parts[0].C for future-proof resolution).
//
// Caller passes parentTable's logical id column name (e.g., "series_id" or
// "episode_id") to keep column names in scope with the table name.
// The helper does NOT take a separate index on the language column alone
// — fan-in lookups (`WHERE series_id = ?`) hit the PK; fan-out lookups
// (`WHERE language = ?`) are rare and can add a follow-up index later if
// measured slow.
//
// nameLookupIdx (when non-empty) adds an index on (language, name)
// — used by D-1-3b taxonomy i18n siblings for the PRD §5.4 Sonarr-genre
// fallback ("resolve 'Drama' + 'en-US' → genres.id"). The "name" column
// is resolved by-name from extraCols; the helper panics if it's missing
// (programmer error — the caller asked for the index but didn't put
// "name" in extraCols).
//
// Used by D-1-3a (series_texts, episode_texts; nameLookupIdx="") and
// D-1-3b (genres_i18n, networks_i18n, production_companies_i18n,
// keywords_i18n; nameLookupIdx=<sibling>_name).
func i18nTextTable(
	d Dialect,
	tableName string,
	parentTable *atlasschema.Table,
	parentIDColName string,
	extraCols []*atlasschema.Column,
	nameLookupIdx string,
	enrichedAt bool,
) *atlasschema.Table {
	parentID := fkColumn(d, parentIDColName, false /* not null */)
	language := atlasschema.NewStringColumn("language", "text").SetNull(false)
	updatedAt := timestampColumn(d, "updated_at", true /* withDefault */, true /* notNull */)

	cols := []*atlasschema.Column{parentID, language}
	cols = append(cols, extraCols...)
	if enrichedAt {
		cols = append(cols, timestampColumn(d, "enriched_at", false, false))
	}
	cols = append(cols, updatedAt)

	refCol := parentRefCol(parentTable)
	t := atlasschema.NewTable(tableName).
		AddColumns(cols...).
		SetPrimaryKey(atlasschema.NewPrimaryKey(parentID, language)).
		AddForeignKeys(
			atlasschema.NewForeignKey(tableName + "_" + parentIDColName + "_fkey").
				AddColumns(parentID).
				SetRefTable(parentTable).
				AddRefColumns(refCol).
				SetOnDelete(atlasschema.NoAction).
				SetOnUpdate(atlasschema.NoAction),
		)
	if nameLookupIdx != "" {
		nameCol := findCol(extraCols, "name")
		if nameCol == nil {
			panic(fmt.Sprintf("schema: i18nTextTable %q asked for nameLookupIdx but extraCols has no 'name' column", tableName))
		}
		t.AddIndexes(
			atlasschema.NewIndex(nameLookupIdx).AddColumns(language, nameCol),
		)
	}
	return t
}

// findCol returns the column with the given name from cols, or nil if
// absent. Used by i18nTextTable to late-bind the (language, name)
// lookup index without forcing the caller to pre-build the index.
func findCol(cols []*atlasschema.Column, name string) *atlasschema.Column {
	for _, c := range cols {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// parentRefCol returns the FK reference column for a parent table.
// Prefers PrimaryKey.Parts[0].C (future-proof against column-order
// rearrangement on the parent table) and falls back to Columns[0] if the
// PK was somehow not set — the fallback is a defensive guard, all
// shipped builders set the PK explicitly.
func parentRefCol(parentTable *atlasschema.Table) *atlasschema.Column {
	if parentTable.PrimaryKey != nil && len(parentTable.PrimaryKey.Parts) > 0 {
		return parentTable.PrimaryKey.Parts[0].C
	}
	return parentTable.Columns[0]
}

// mustTable looks up a previously-added table by name. Panics if
// absent — used by appenders that depend on FK targets already being
// installed in s. The panic is a programmer error indicator (Schema(d)
// is a pure function, table order is deterministic), never a runtime
// data condition.
func mustTable(s *atlasschema.Schema, name string) *atlasschema.Table {
	for _, t := range s.Tables {
		if t.Name == name {
			return t
		}
	}
	panic(fmt.Sprintf("schema: table %q not found in schema (table-order bug)", name))
}

// ----------------------------------------------------------------------
// D-1-5 — instance projections + enrichment tracking.
// ----------------------------------------------------------------------

// addInstanceProjections appends series_cache, episode_states,
// season_stats to s. Called from Schema(d). Looks up series + episodes
// canon tables via mustTable — both are guaranteed present by the
// table-order contract (D-1-2 lands them in addCoreSeries before any
// D-1-5 appender runs). NOTE: season_stats does NOT FK to series_cache
// — see story 458 §Investigation Notes for the rationale.
func addInstanceProjections(s *atlasschema.Schema, d Dialect) {
	series := mustTable(s, "series")
	episodes := mustTable(s, "episodes")
	s.AddTables(
		buildSeriesCacheTable(d, series),
		buildEpisodeStatesTable(d, episodes),
		buildSeasonStatsTable(d),
	)
}

// buildSeriesCacheTable returns series_cache — per-instance projection
// of one Sonarr series row, 11 cols, composite PK (instance_name,
// sonarr_series_id). FK series_id → series(id) NO ACTION (canon deletes
// are always soft; a hard DELETE on series should error rather than
// silently wipe per-instance projections). Soft-deleted via deleted_at;
// readers filter `WHERE deleted_at IS NULL`.
//
// Indexes:
//   - series_cache_instance_active ON (instance_name) WHERE deleted_at IS NULL
//     (hot read-path filter; reader fans by instance with soft-delete cut)
//   - series_cache_series_id ON (series_id) (resolver "find all instance
//     projections of this canon series" path; non-unique)
//
// DO NOT add an FK on instance_name — cascade is app-managed in
// SonarrInstanceRepository.Delete (consistent across the schema). DO NOT
// switch the series FK to CASCADE — the soft-delete contract requires
// the FK to error on hard-deletes (forces ops to soft-delete first).
func buildSeriesCacheTable(d Dialect, seriesTable *atlasschema.Table) *atlasschema.Table {
	instanceName := atlasschema.NewStringColumn("instance_name", "text").SetNull(false)
	sonarrSeriesID := atlasschema.NewIntColumn("sonarr_series_id", "integer").SetNull(false)
	seriesID := fkColumn(d, "series_id", false /* not null */)
	titleSlug := atlasschema.NewStringColumn("title_slug", "text").SetNull(false)
	monitored := atlasschema.NewBoolColumn("monitored", "boolean").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "false"})
	missingCount := atlasschema.NewIntColumn("missing_count", "integer").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "0"})
	episodeFileCount := atlasschema.NewIntColumn("episode_file_count", "integer").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "0"})
	sizeOnDiskBytes := atlasschema.NewIntColumn("size_on_disk_bytes", "bigint")
	if d == DialectSQLite {
		sizeOnDiskBytes = atlasschema.NewIntColumn("size_on_disk_bytes", "integer")
	}
	sizeOnDiskBytes.SetNull(false).SetDefault(&atlasschema.Literal{V: "0"})
	airedEpisodeCount := atlasschema.NewIntColumn("aired_episode_count", "integer").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "0"})
	updatedAt := timestampColumn(d, "updated_at", false /* withDefault */, true /* notNull */)
	deletedAt := timestampColumn(d, "deleted_at", false, false)

	return atlasschema.NewTable("series_cache").
		AddColumns(instanceName, sonarrSeriesID, seriesID, titleSlug,
			monitored, missingCount, episodeFileCount, sizeOnDiskBytes,
			airedEpisodeCount, updatedAt, deletedAt).
		SetPrimaryKey(atlasschema.NewPrimaryKey(instanceName, sonarrSeriesID)).
		AddIndexes(
			partialIndex(d, "series_cache_instance_active",
				[]*atlasschema.Column{instanceName}, "deleted_at IS NULL"),
			atlasschema.NewIndex("series_cache_series_id").
				AddColumns(seriesID),
		).
		AddForeignKeys(
			atlasschema.NewForeignKey("series_cache_series_id_fkey").
				AddColumns(seriesID).
				SetRefTable(seriesTable).
				AddRefColumns(parentRefCol(seriesTable)).
				SetOnDelete(atlasschema.NoAction).
				SetOnUpdate(atlasschema.NoAction),
		)
}

// buildEpisodeStatesTable returns episode_states — per-instance per-
// episode file state, 13 cols, composite PK (instance_name, episode_id).
// FK episode_id → episodes(id) NO ACTION (consistent with series_cache).
// Soft-deleted via deleted_at; story 218 (E-2) SeriesDelete cascade
// stamps this column.
//
// mediaInfo columns (video_codec, audio_codec, audio_channels,
// release_group) are nullable — Sonarr's mediaInfo block is absent
// until the episode file has been probed.
//
// Index episode_states_deleted_at ON (instance_name, deleted_at)
// WHERE deleted_at IS NOT NULL — cascade-housekeeping path "find
// rows to hard-purge later" (story 218 pattern). Inverse predicate
// compared to series_cache: this one indexes the SOFT-DELETED rows.
func buildEpisodeStatesTable(d Dialect, episodesTable *atlasschema.Table) *atlasschema.Table {
	instanceName := atlasschema.NewStringColumn("instance_name", "text").SetNull(false)
	episodeID := fkColumn(d, "episode_id", false /* not null */)
	monitored := atlasschema.NewBoolColumn("monitored", "boolean").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "false"})
	hasFile := atlasschema.NewBoolColumn("has_file", "boolean").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "false"})
	episodeFileID := atlasschema.NewNullIntColumn("episode_file_id", "integer")
	quality := atlasschema.NewNullStringColumn("quality", "text")
	sizeBytes := atlasschema.NewNullIntColumn("size_bytes", "bigint")
	if d == DialectSQLite {
		sizeBytes = atlasschema.NewNullIntColumn("size_bytes", "integer")
	}
	videoCodec := atlasschema.NewNullStringColumn("video_codec", "text")
	audioCodec := atlasschema.NewNullStringColumn("audio_codec", "text")
	audioChannels := atlasschema.NewNullStringColumn("audio_channels", "text")
	releaseGroup := atlasschema.NewNullStringColumn("release_group", "text")
	updatedAt := timestampColumn(d, "updated_at", false, true)
	deletedAt := timestampColumn(d, "deleted_at", false, false)

	return atlasschema.NewTable("episode_states").
		AddColumns(instanceName, episodeID, monitored, hasFile,
			episodeFileID, quality, sizeBytes, videoCodec, audioCodec,
			audioChannels, releaseGroup, updatedAt, deletedAt).
		SetPrimaryKey(atlasschema.NewPrimaryKey(instanceName, episodeID)).
		AddIndexes(
			partialIndex(d, "episode_states_deleted_at",
				[]*atlasschema.Column{instanceName, deletedAt},
				"deleted_at IS NOT NULL"),
		).
		AddForeignKeys(
			atlasschema.NewForeignKey("episode_states_episode_id_fkey").
				AddColumns(episodeID).
				SetRefTable(episodesTable).
				AddRefColumns(parentRefCol(episodesTable)).
				SetOnDelete(atlasschema.NoAction).
				SetOnUpdate(atlasschema.NoAction),
		)
}

// buildSeasonStatsTable returns season_stats — per-instance per-series
// per-season Sonarr statistics projection, 11 cols, composite-3 PK
// (instance_name, sonarr_series_id, season_number). NO FKs — the
// (instance_name, sonarr_series_id) pair is a natural projection key
// also held by series_cache, but DB-level coupling is deliberately
// avoided (the SonarrSync cascade writes the two tables in two
// statements that aren't in the same transaction at all times; an FK
// would create a hard ordering constraint the existing code doesn't
// honor consistently). Soft-deleted via deleted_at; the SeriesDelete
// cascade (scan.CascadeSeriesDelete) stamps it alongside series_cache.
//
// Index season_stats_series ON (instance_name, sonarr_series_id)
// WHERE deleted_at IS NULL — the composers fan series → seasons via
// this prefix.
func buildSeasonStatsTable(d Dialect) *atlasschema.Table {
	instanceName := atlasschema.NewStringColumn("instance_name", "text").SetNull(false)
	sonarrSeriesID := atlasschema.NewIntColumn("sonarr_series_id", "integer").SetNull(false)
	seasonNumber := atlasschema.NewIntColumn("season_number", "integer").SetNull(false)
	episodeCount := atlasschema.NewIntColumn("episode_count", "integer").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "0"})
	episodeFileCount := atlasschema.NewIntColumn("episode_file_count", "integer").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "0"})
	totalEpisodeCount := atlasschema.NewIntColumn("total_episode_count", "integer").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "0"})
	airedEpisodeCount := atlasschema.NewIntColumn("aired_episode_count", "integer").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "0"})
	monitored := atlasschema.NewBoolColumn("monitored", "boolean").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "false"})
	sizeOnDiskBytes := atlasschema.NewIntColumn("size_on_disk_bytes", "bigint")
	if d == DialectSQLite {
		sizeOnDiskBytes = atlasschema.NewIntColumn("size_on_disk_bytes", "integer")
	}
	sizeOnDiskBytes.SetNull(false).SetDefault(&atlasschema.Literal{V: "0"})
	updatedAt := timestampColumn(d, "updated_at", false, true)
	deletedAt := timestampColumn(d, "deleted_at", false, false)

	return atlasschema.NewTable("season_stats").
		AddColumns(instanceName, sonarrSeriesID, seasonNumber,
			episodeCount, episodeFileCount, totalEpisodeCount,
			airedEpisodeCount, monitored, sizeOnDiskBytes,
			updatedAt, deletedAt).
		SetPrimaryKey(atlasschema.NewPrimaryKey(instanceName, sonarrSeriesID, seasonNumber)).
		AddIndexes(
			partialIndex(d, "season_stats_series",
				[]*atlasschema.Column{instanceName, sonarrSeriesID},
				"deleted_at IS NULL"),
		)
}

// addEnrichmentTracking appends enrichment_errors to s. Single-table
// migration (000008). Called from Schema(d). The PRD §D-1 line 4392
// also lists series.enrichment_*_synced_at columns under 000008 — those
// were moved forward to 000001 during D-1-2 (story 455) and are NOT
// re-added here; see the per-builder comment for rationale.
func addEnrichmentTracking(s *atlasschema.Schema, d Dialect) {
	s.AddTables(buildEnrichmentErrorsTable(d))
}

// buildEnrichmentErrorsTable returns enrichment_errors — 9 cols, single
// surrogate PK `id`, UNIQUE composite-3 (entity_type, entity_id,
// source), 1 partial index on next_attempt_at WHERE NOT NULL.
//
// POLYMORPHIC: entity_id has NO FK (mirrors external_ids.entity_id
// from D-1-4b). Domain (`series` | `season` | `episode` | `person`) is
// enforced at the use-case layer via the enrichment.EntityType enum,
// NOT by DB constraint. Sources (`tmdb` | `omdb` | `sonarr`) are
// enforced by the enrichment.Source enum.
//
// The partial index on next_attempt_at speeds the worker's
// "errors-ready-for-retry" scan: `WHERE next_attempt_at <= now()`
// — covers only rows the worker is actually waiting on.
//
// Timestamps differ from the instance projections: first_seen_at and
// last_seen_at both DEFAULT now() at insert (the row is created on
// first failure and rewritten on each subsequent failure; the writer
// always passes last_seen_at explicitly, but DEFAULT now() keeps
// hand-written test inserts simple).
func buildEnrichmentErrorsTable(d Dialect) *atlasschema.Table {
	id := pkColumn(d)
	entityType := atlasschema.NewStringColumn("entity_type", "text").SetNull(false)
	entityID := atlasschema.NewIntColumn("entity_id", "bigint").SetNull(false)
	if d == DialectSQLite {
		entityID = atlasschema.NewIntColumn("entity_id", "integer").SetNull(false)
	}
	source := atlasschema.NewStringColumn("source", "text").SetNull(false)
	lastError := atlasschema.NewStringColumn("last_error", "text").SetNull(false)
	attempts := atlasschema.NewIntColumn("attempts", "integer").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "1"})
	firstSeenAt := timestampColumn(d, "first_seen_at", true /* withDefault */, true /* notNull */)
	lastSeenAt := timestampColumn(d, "last_seen_at", true, true)
	nextAttemptAt := timestampColumn(d, "next_attempt_at", false, false)

	return atlasschema.NewTable("enrichment_errors").
		AddColumns(id, entityType, entityID, source, lastError,
			attempts, firstSeenAt, lastSeenAt, nextAttemptAt).
		SetPrimaryKey(atlasschema.NewPrimaryKey(id)).
		AddIndexes(
			atlasschema.NewUniqueIndex("enrichment_errors_entity_source").
				AddColumns(entityType, entityID, source),
			partialIndex(d, "enrichment_errors_next_attempt",
				[]*atlasschema.Column{nextAttemptAt},
				"next_attempt_at IS NOT NULL"),
		)
}

// ----------------------------------------------------------------------
// D-1-6a — multi-language images.
// ----------------------------------------------------------------------

// addSeriesImages appends series_images to s. Called from Schema(d).
// Looks up the series canon table via mustTable — guaranteed present
// by the table-order contract (D-1-2 lands it in addCoreSeries before
// any D-1-6a appender runs).
func addSeriesImages(s *atlasschema.Schema, d Dialect) {
	series := mustTable(s, "series")
	s.AddTables(buildSeriesImagesTable(d, series))
}

// buildSeriesImagesTable returns series_images — multi-language top-3
// poster/backdrop/logo references, 13 cols, single PK `id`. FK CASCADE
// to series (derived enrichment data — dies with the canon row).
//
// Schema highlights:
//   - language is BCP-47 (e.g., "en-US", "ru-RU") OR "" for language-
//     neutral. NULL is NOT used — would break the UNIQUE composite
//     constraint (NULL != NULL).
//   - kind ∈ {'poster', 'backdrop', 'logo'} (domain-enforced, NOT a
//     CHECK constraint — consistent with the rest of the schema's
//     "validate at use-case layer" pattern).
//   - asset_hash NULL pre-resolution; populated by mediaproxy when
//     the TMDB path is fetched + stored. Future D-2/D-3 GC paths key
//     off non-NULL asset_hash to count active asset refs.
//   - iso_lang holds TMDB's raw iso_639_1 ("en", "ru", NULL) — distinct
//     from the BCP-47 `language` column. Composer maps iso_lang →
//     language during upsert (e.g., iso_lang="en" + region="US" →
//     language="en-US").
//   - vote_count tie-breaks rows with identical vote_average.
//   - position 0=best, 1=second, 2=third. The DB enforces uniqueness
//     on (series_id, language, kind, position); the app enforces top-3
//     cardinality at write time (rows with position=3 are technically
//     allowed by the DB but readers ignore them; producers MUST cap).
//
// Indexes:
//   - series_images_series_lang_kind_position UNIQUE composite-4 — the
//     producer's idempotency key (upsert ON CONFLICT lands here).
//   - series_images_series_kind_position — non-unique composite-3 hot
//     composer read path: "top-3 posters for series 42, all languages".
//
// FK CASCADE on series mirrors videos/content_ratings/
// series_recommendations from D-1-4b. DO NOT switch to NO ACTION by
// reflex — derived enrichment tables follow the CASCADE-on-canon-drop
// idiom (vs. instance projections D-1-5 which are NO ACTION because
// per-instance state outlives canon soft-deletes).
func buildSeriesImagesTable(d Dialect, seriesTable *atlasschema.Table) *atlasschema.Table {
	id := pkColumn(d)
	seriesID := fkColumn(d, "series_id", false /* not null */)
	language := atlasschema.NewStringColumn("language", "text").SetNull(false)
	kind := atlasschema.NewStringColumn("kind", "text").SetNull(false)
	tmdbPath := atlasschema.NewStringColumn("tmdb_path", "text").SetNull(false)
	assetHash := atlasschema.NewNullStringColumn("asset_hash", "text")
	isoLang := atlasschema.NewNullStringColumn("iso_lang", "text")
	voteAverage := atlasschema.NewNullFloatColumn("vote_average", "double precision")
	voteCount := atlasschema.NewNullIntColumn("vote_count", "integer")
	width := atlasschema.NewNullIntColumn("width", "integer")
	height := atlasschema.NewNullIntColumn("height", "integer")
	position := atlasschema.NewIntColumn("position", "integer").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "0"})
	updatedAt := timestampColumn(d, "updated_at", true /* withDefault */, true /* notNull */)

	return atlasschema.NewTable("series_images").
		AddColumns(id, seriesID, language, kind, tmdbPath, assetHash,
			isoLang, voteAverage, voteCount, width, height, position,
			updatedAt).
		SetPrimaryKey(atlasschema.NewPrimaryKey(id)).
		AddIndexes(
			atlasschema.NewUniqueIndex("series_images_series_lang_kind_position").
				AddColumns(seriesID, language, kind, position),
			atlasschema.NewIndex("series_images_series_kind_position").
				AddColumns(seriesID, kind, position),
		).
		AddForeignKeys(
			atlasschema.NewForeignKey("series_images_series_id_fkey").
				AddColumns(seriesID).
				SetRefTable(seriesTable).
				AddRefColumns(parentRefCol(seriesTable)).
				SetOnDelete(atlasschema.Cascade).
				SetOnUpdate(atlasschema.NoAction),
		)
}

// ----------------------------------------------------------------------
// D-1-6b — admin tables.
// ----------------------------------------------------------------------

// addAdmin appends the 5 admin tables to s. Called from Schema(d).
//
// FK cascade graph:
//
//	sonarr_instance.name (TEXT PK)
//	  ←CASCADE←  instance_secret.instance_name
//	             instance_secret (id BIGSERIAL PK; UNIQUE(instance_name, secret_name))
//	               ←SET NULL←  sonarr_instance.token_secret_id (denormalized current-token pointer)
//
//	app_secret.id (BIGSERIAL PK)
//	  ←SET NULL←  external_service_config.api_key_secret_id
//	  ←SET NULL←  external_service_config.proxy_pass_secret_id
//
// The cyclic FK between sonarr_instance and instance_secret is handled
// by building an instance_secret stub FIRST (without its instance_name
// FK resolved), then sonarr_instance with the back-reference, then
// post-wiring the instance_secret.instance_name FK to the now-existing
// sonarr_instance table. Atlas v0.31.0 emits this as CREATE TABLE +
// ALTER TABLE ADD CONSTRAINT in the generated SQL (PG); SQLite uses a
// recreate-table workaround because ALTER TABLE ADD CONSTRAINT for FKs
// is not supported.
//
// No FK on sonarr_instance ← series_cache.instance_name — app-managed
// cascade (consistent with D-1-5 458 design).
func addAdmin(s *atlasschema.Schema, d Dialect) {
	instanceSecret := buildInstanceSecretTableStub(d)
	sonarrInstance := buildSonarrInstanceTable(d, instanceSecret)
	wireInstanceSecretFK(instanceSecret, sonarrInstance)

	appSecret := buildAppSecretTable(d)
	externalServiceConfig := buildExternalServiceConfigTable(d, appSecret)
	externalServiceQuotaState := buildExternalServiceQuotaStateTable(d)

	s.AddTables(
		sonarrInstance,
		instanceSecret,
		appSecret,
		externalServiceConfig,
		externalServiceQuotaState,
	)
}

// buildInstanceSecretTableStub builds the instance_secret table WITHOUT
// the instance_name FK to sonarr_instance — that gets wired by
// wireInstanceSecretFK after sonarr_instance exists. Two-step build is
// needed because instance_secret.id is FK-referenced from
// sonarr_instance.token_secret_id (cyclic dependency).
//
// Columns:
//
//	id              BIGSERIAL PK (stable FK target across rotation)
//	instance_name   TEXT NOT NULL (FK wired in step 2)
//	secret_name     TEXT NOT NULL ('token' | 'webhook_signing_key' | …)
//	encrypted_value BYTEA NOT NULL (AES-GCM ciphertext: nonce|ct|tag)
//	created_at, updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
//
// UNIQUE composite-2 on (instance_name, secret_name) — primary lookup
// path (`SELECT … WHERE instance_name = ? AND secret_name = 'token'`).
func buildInstanceSecretTableStub(d Dialect) *atlasschema.Table {
	id := pkColumn(d)
	instanceName := atlasschema.NewStringColumn("instance_name", "text").SetNull(false)
	secretName := atlasschema.NewStringColumn("secret_name", "text").SetNull(false)
	encryptedValue := atlasschema.NewBinaryColumn("encrypted_value", "bytea").SetNull(false)
	createdAt := timestampColumn(d, "created_at", true, true)
	updatedAt := timestampColumn(d, "updated_at", true, true)

	return atlasschema.NewTable("instance_secret").
		AddColumns(id, instanceName, secretName, encryptedValue,
			createdAt, updatedAt).
		SetPrimaryKey(atlasschema.NewPrimaryKey(id)).
		AddIndexes(
			atlasschema.NewUniqueIndex("instance_secret_lookup").
				AddColumns(instanceName, secretName),
		)
}

// wireInstanceSecretFK adds the instance_name → sonarr_instance.name
// CASCADE FK on the instance_secret table AFTER both tables exist.
// Mutates instanceSecret in place — relies on the Atlas table being a
// builder-mutable struct in v0.31.0.
func wireInstanceSecretFK(instanceSecret, sonarrInstance *atlasschema.Table) {
	instanceNameCol := findCol(instanceSecret.Columns, "instance_name")
	if instanceNameCol == nil {
		panic("schema: instance_secret missing instance_name column (programmer error)")
	}
	instanceSecret.AddForeignKeys(
		atlasschema.NewForeignKey("instance_secret_instance_name_fkey").
			AddColumns(instanceNameCol).
			SetRefTable(sonarrInstance).
			AddRefColumns(parentRefCol(sonarrInstance)).
			SetOnDelete(atlasschema.Cascade).
			SetOnUpdate(atlasschema.NoAction),
	)
}

// buildSonarrInstanceTable returns sonarr_instance — 10 cols, single PK
// on TEXT `name` (natural key, operator-friendly). Forward-ref FK
// token_secret_id → instance_secret.id ON DELETE SET NULL.
//
// Columns:
//
//	name              TEXT PK
//	url               TEXT NOT NULL (Sonarr API base)
//	public_url        TEXT NULL (browser deeplinks)
//	mode              TEXT NOT NULL DEFAULT 'auto'
//	token_secret_id   BIGINT NULL FK → instance_secret.id SET NULL
//	health            TEXT NOT NULL DEFAULT 'unknown'
//	last_check_at     TIMESTAMPTZ NULL
//	transitions_count INTEGER NOT NULL DEFAULT 0
//	created_at, updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
//
// Partial index sonarr_instance_unhealthy ON (last_check_at) WHERE
// health <> 'healthy' — watchdog scan path "instances needing
// recheck", covers only the small subset with non-healthy state.
//
// Brief vs PRD reconciliation: brief asks `name PK TEXT`; legacy used
// surrogate `id BIGSERIAL`. Greenfield uses natural key for
// operator-friendly cross-table queries (series_cache.instance_name TEXT
// already correlates with this name).
func buildSonarrInstanceTable(d Dialect, instanceSecretTable *atlasschema.Table) *atlasschema.Table {
	name := atlasschema.NewStringColumn("name", "text").SetNull(false)
	url := atlasschema.NewStringColumn("url", "text").SetNull(false)
	publicURL := atlasschema.NewNullStringColumn("public_url", "text")
	mode := atlasschema.NewStringColumn("mode", "text").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "'auto'"})
	tokenSecretID := fkColumn(d, "token_secret_id", true /* nullable */)
	health := atlasschema.NewStringColumn("health", "text").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "'unknown'"})
	lastCheckAt := timestampColumn(d, "last_check_at", false, false)
	transitionsCount := atlasschema.NewIntColumn("transitions_count", "integer").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "0"})
	createdAt := timestampColumn(d, "created_at", true, true)
	updatedAt := timestampColumn(d, "updated_at", true, true)

	return atlasschema.NewTable("sonarr_instance").
		AddColumns(name, url, publicURL, mode, tokenSecretID, health,
			lastCheckAt, transitionsCount, createdAt, updatedAt).
		SetPrimaryKey(atlasschema.NewPrimaryKey(name)).
		AddIndexes(
			partialIndex(d, "sonarr_instance_unhealthy",
				[]*atlasschema.Column{lastCheckAt},
				"health <> 'healthy'"),
		).
		AddForeignKeys(
			atlasschema.NewForeignKey("sonarr_instance_token_secret_id_fkey").
				AddColumns(tokenSecretID).
				SetRefTable(instanceSecretTable).
				AddRefColumns(parentRefCol(instanceSecretTable)).
				SetOnDelete(atlasschema.SetNull).
				SetOnUpdate(atlasschema.NoAction),
		)
}

// buildAppSecretTable returns app_secret — 5 cols, single PK `id`.
// App-level (non-instance-specific) encrypted secrets keyed by name.
//
// Columns:
//
//	id              BIGSERIAL PK
//	secret_name     TEXT NOT NULL UNIQUE ('tmdb_api_key' | 'omdb_api_key' | …)
//	encrypted_value BYTEA NOT NULL (AES-GCM ciphertext)
//	created_at, updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
//
// UNIQUE on secret_name — singleton-per-name semantics. The id
// surrogate is for FK targeting from external_service_config (FK
// targets BIGINT, not TEXT — keeps the join cheap).
func buildAppSecretTable(d Dialect) *atlasschema.Table {
	id := pkColumn(d)
	secretName := atlasschema.NewStringColumn("secret_name", "text").SetNull(false)
	encryptedValue := atlasschema.NewBinaryColumn("encrypted_value", "bytea").SetNull(false)
	createdAt := timestampColumn(d, "created_at", true, true)
	updatedAt := timestampColumn(d, "updated_at", true, true)

	return atlasschema.NewTable("app_secret").
		AddColumns(id, secretName, encryptedValue, createdAt, updatedAt).
		SetPrimaryKey(atlasschema.NewPrimaryKey(id)).
		AddIndexes(
			atlasschema.NewUniqueIndex("app_secret_name").
				AddColumns(secretName),
		)
}

// buildExternalServiceConfigTable returns external_service_config —
// 8 cols, single PK on TEXT `service_name`. 2 FKs to app_secret.id
// (api key + proxy password) both SET NULL on delete.
//
// Columns:
//
//	service_name           TEXT PK ('tmdb' | 'omdb' | 'tvdb')
//	api_key_secret_id      BIGINT NULL FK → app_secret.id SET NULL
//	enabled                BOOLEAN NOT NULL DEFAULT FALSE
//	proxy_url              TEXT NULL
//	proxy_user             TEXT NULL
//	proxy_pass_secret_id   BIGINT NULL FK → app_secret.id SET NULL
//	last4                  TEXT NULL (masked UI display)
//	updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
//
// No created_at — singleton-per-service-name; updated_at suffices.
func buildExternalServiceConfigTable(d Dialect, appSecretTable *atlasschema.Table) *atlasschema.Table {
	serviceName := atlasschema.NewStringColumn("service_name", "text").SetNull(false)
	apiKeySecretID := fkColumn(d, "api_key_secret_id", true /* nullable */)
	enabled := atlasschema.NewBoolColumn("enabled", "boolean").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "false"})
	proxyURL := atlasschema.NewNullStringColumn("proxy_url", "text")
	proxyUser := atlasschema.NewNullStringColumn("proxy_user", "text")
	proxyPassSecretID := fkColumn(d, "proxy_pass_secret_id", true)
	last4 := atlasschema.NewNullStringColumn("last4", "text")
	updatedAt := timestampColumn(d, "updated_at", true, true)

	refCol := parentRefCol(appSecretTable)
	return atlasschema.NewTable("external_service_config").
		AddColumns(serviceName, apiKeySecretID, enabled, proxyURL,
			proxyUser, proxyPassSecretID, last4, updatedAt).
		SetPrimaryKey(atlasschema.NewPrimaryKey(serviceName)).
		AddForeignKeys(
			atlasschema.NewForeignKey("external_service_config_api_key_secret_id_fkey").
				AddColumns(apiKeySecretID).
				SetRefTable(appSecretTable).
				AddRefColumns(refCol).
				SetOnDelete(atlasschema.SetNull).
				SetOnUpdate(atlasschema.NoAction),
			atlasschema.NewForeignKey("external_service_config_proxy_pass_secret_id_fkey").
				AddColumns(proxyPassSecretID).
				SetRefTable(appSecretTable).
				AddRefColumns(refCol).
				SetOnDelete(atlasschema.SetNull).
				SetOnUpdate(atlasschema.NoAction),
		)
}

// buildExternalServiceQuotaStateTable returns
// external_service_quota_state — 6 cols, composite-2 PK (service_name,
// window_start). Per-window rate-limit counter row (PRD §5.10 OMDb
// adaptive rate limiter).
//
// Columns:
//
//	service_name    TEXT NOT NULL (composite PK part 1)
//	window_start    TIMESTAMPTZ NOT NULL (composite PK part 2)
//	requests_made   INTEGER NOT NULL DEFAULT 0
//	requests_quota  INTEGER NOT NULL DEFAULT 0 (upstream cap; 0=unknown)
//	exhausted_at    TIMESTAMPTZ NULL (stamped when made>=quota)
//	updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
//
// Index window_start (non-partial, non-unique) for the GC sweep
// (`DELETE WHERE window_start < $cutoff`). Cheap.
//
// Brief vs legacy reconciliation: brief said DATE; legacy + PRD §5.10
// uses TIMESTAMPTZ for sub-day windows. We use TIMESTAMPTZ.
func buildExternalServiceQuotaStateTable(d Dialect) *atlasschema.Table {
	serviceName := atlasschema.NewStringColumn("service_name", "text").SetNull(false)
	windowStart := timestampColumn(d, "window_start", false /* withDefault */, true /* notNull */)
	requestsMade := atlasschema.NewIntColumn("requests_made", "integer").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "0"})
	requestsQuota := atlasschema.NewIntColumn("requests_quota", "integer").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "0"})
	exhaustedAt := timestampColumn(d, "exhausted_at", false, false)
	updatedAt := timestampColumn(d, "updated_at", true, true)

	return atlasschema.NewTable("external_service_quota_state").
		AddColumns(serviceName, windowStart, requestsMade, requestsQuota,
			exhaustedAt, updatedAt).
		SetPrimaryKey(atlasschema.NewPrimaryKey(serviceName, windowStart)).
		AddIndexes(
			atlasschema.NewIndex("external_service_quota_state_window").
				AddColumns(windowStart),
		)
}

// ----------------------------------------------------------------------
// D-1-7a — auth tables.
// ----------------------------------------------------------------------

// addAuth appends the 2 auth tables to s. Called from Schema(d).
//
// FK cascade graph:
//
//	users.id (BIGSERIAL PK)
//	  ←CASCADE←  user_instance_tags.user_id
//
//	sonarr_instance.name (TEXT PK, shipped by addAdmin)
//	  ←CASCADE←  user_instance_tags.instance_name
//
// PRD-vs-reality reconciliations (full notes in story 460a):
//
//  1. user_settings is NOT a separate table — preferred_language and
//     avatar_mode are folded into users (1:1 cardinality, no per-context
//     override). PRD §D-1 line 4395 lists user_settings; PRD §4.5
//     doesn't define its shape. Collapsing is correct greenfield.
//
//  2. user_sessions is NOT in schema — auth is stateless cookie HMAC
//     signed against runtime_config.auth_session_epoch. Sessions are
//     not row-tracked.
//
//  3. users.role exists per NG-1 future RBAC. App treats every user as
//     admin until NG-1 ships role enforcement. CHECK ('admin','user')
//     keeps the enum closed.
//
// Depends on sonarr_instance from addAdmin — Schema(d) runs addAdmin
// immediately before addAuth, so the table is guaranteed to exist.
func addAuth(s *atlasschema.Schema, d Dialect) {
	users := buildUsersTable(d)
	s.AddTables(users)

	sonarrInstance := mustTable(s, "sonarr_instance")
	userInstanceTags := buildUserInstanceTagsTable(d, users, sonarrInstance)
	s.AddTables(userInstanceTags)
}

// buildUsersTable returns users — 11 cols, single PK on BIGSERIAL id.
// Embeds preferred_language + avatar_mode columns (user_settings
// collapsed for 1:1 cardinality — see addAuth doc).
//
// Columns:
//
//	id                  BIGSERIAL PK
//	username            TEXT NOT NULL (UNIQUE)
//	email               TEXT NULL
//	password_hash       TEXT NULL (NULL = OIDC-only user)
//	oidc_subject        TEXT NULL (partial UNIQUE; nullable for forms users)
//	role                TEXT NOT NULL DEFAULT 'admin' CHECK IN ('admin','user')
//	avatar_mode         TEXT NOT NULL DEFAULT 'auto' CHECK IN ('auto','monogram','gravatar')
//	preferred_language  TEXT NULL ('en-US' | 'ru-RU' | NULL = server default)
//	created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
//	updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
//	last_login_at       TIMESTAMPTZ NULL (stamped on successful auth)
//
// Indexes:
//
//	users_username_uniq          UNIQUE on (username) — login lookup
//	users_oidc_subject_uniq      UNIQUE on (oidc_subject) PARTIAL WHERE oidc_subject IS NOT NULL
//	                              (matches legacy 000003 pattern; lets many NULL rows coexist)
//
// CHECK constraints:
//
//	users_role_check        role IN ('admin', 'user')
//	users_avatar_mode_check avatar_mode IN ('auto', 'monogram', 'gravatar')
//
// Brief-vs-PRD: user_settings (PRD §D-1 line 4395) collapsed into this
// table — preferred_language + avatar_mode are 1:1 with user, no
// per-context override exists in app code.
func buildUsersTable(d Dialect) *atlasschema.Table {
	id := pkColumn(d)
	username := atlasschema.NewStringColumn("username", "text").SetNull(false)
	email := atlasschema.NewNullStringColumn("email", "text")
	passwordHash := atlasschema.NewNullStringColumn("password_hash", "text")
	oidcSubject := atlasschema.NewNullStringColumn("oidc_subject", "text")
	role := atlasschema.NewStringColumn("role", "text").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "'admin'"})
	avatarMode := atlasschema.NewStringColumn("avatar_mode", "text").
		SetNull(false).
		SetDefault(&atlasschema.Literal{V: "'auto'"})
	preferredLanguage := atlasschema.NewNullStringColumn("preferred_language", "text")
	createdAt := timestampColumn(d, "created_at", true, true)
	updatedAt := timestampColumn(d, "updated_at", true, true)
	lastLoginAt := timestampColumn(d, "last_login_at", false, false)

	roleCheck := atlasschema.NewCheck().
		SetName("users_role_check").
		SetExpr("role IN ('admin', 'user')")
	avatarModeCheck := atlasschema.NewCheck().
		SetName("users_avatar_mode_check").
		SetExpr("avatar_mode IN ('auto', 'monogram', 'gravatar')")

	return atlasschema.NewTable("users").
		AddColumns(id, username, email, passwordHash, oidcSubject,
			role, avatarMode, preferredLanguage,
			createdAt, updatedAt, lastLoginAt).
		SetPrimaryKey(atlasschema.NewPrimaryKey(id)).
		AddIndexes(
			atlasschema.NewUniqueIndex("users_username_uniq").
				AddColumns(username),
			partialUniqueIndex(d, "users_oidc_subject_uniq",
				[]*atlasschema.Column{oidcSubject},
				"oidc_subject IS NOT NULL"),
		).
		AddChecks(roleCheck, avatarModeCheck)
}

// buildUserInstanceTagsTable returns user_instance_tags — 6 cols,
// composite PK (user_id, instance_name), 2 FKs CASCADE.
//
// Columns:
//
//	user_id          BIGINT NOT NULL  FK→users.id CASCADE
//	instance_name    TEXT NOT NULL    FK→sonarr_instance.name CASCADE
//	sonarr_tag_id    INTEGER NOT NULL (Sonarr-side numeric tag id)
//	sonarr_tag_label TEXT NOT NULL    ('sf-alice' — Sonarr-visible label)
//	created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
//	updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
//
// PRIMARY KEY (user_id, instance_name) — natural composite identity.
//
// UNIQUE composite-2 on (instance_name, sonarr_tag_label) — prevents
// two users claiming the same Sonarr label on one instance. PK doesn't
// cover this; standalone index needed.
//
// No standalone index on instance_name alone — fan-out "list all
// users tagging on instance X" is a rare admin path; PK won't help
// because instance_name is the SECOND PK column. Skipped per
// over-indexing avoidance; add in D-2 if it materializes.
func buildUserInstanceTagsTable(d Dialect, usersTable, sonarrInstanceTable *atlasschema.Table) *atlasschema.Table {
	userID := fkColumn(d, "user_id", false /* not nullable */)
	instanceName := atlasschema.NewStringColumn("instance_name", "text").SetNull(false)
	sonarrTagID := atlasschema.NewIntColumn("sonarr_tag_id", "integer").SetNull(false)
	sonarrTagLabel := atlasschema.NewStringColumn("sonarr_tag_label", "text").SetNull(false)
	createdAt := timestampColumn(d, "created_at", true, true)
	updatedAt := timestampColumn(d, "updated_at", true, true)

	return atlasschema.NewTable("user_instance_tags").
		AddColumns(userID, instanceName, sonarrTagID, sonarrTagLabel, createdAt, updatedAt).
		SetPrimaryKey(atlasschema.NewPrimaryKey(userID, instanceName)).
		AddIndexes(
			atlasschema.NewUniqueIndex("user_instance_tags_label").
				AddColumns(instanceName, sonarrTagLabel),
		).
		AddForeignKeys(
			atlasschema.NewForeignKey("user_instance_tags_user_id_fkey").
				AddColumns(userID).
				SetRefTable(usersTable).
				AddRefColumns(parentRefCol(usersTable)).
				SetOnDelete(atlasschema.Cascade).
				SetOnUpdate(atlasschema.NoAction),
			atlasschema.NewForeignKey("user_instance_tags_instance_name_fkey").
				AddColumns(instanceName).
				SetRefTable(sonarrInstanceTable).
				AddRefColumns(parentRefCol(sonarrInstanceTable)).
				SetOnDelete(atlasschema.Cascade).
				SetOnUpdate(atlasschema.NoAction),
		)
}
