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
// series_companies, series_keywords.
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

	// D-1-4..D-1-7 (stories 457..460) append further batches here.

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
