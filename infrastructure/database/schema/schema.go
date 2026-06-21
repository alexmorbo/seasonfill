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
// target tables. D-1-2 (this commit) lands the first batch:
// series, seasons, episodes.
package schema

import (
	"fmt"
	"os"

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

	// D-1-3..D-1-7 (stories 456..460) append further batches here.

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
