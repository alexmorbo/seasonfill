// Story S-E1 — break-glass CLI for the base-lang (en-US) backfill.
//
// `seasonfill backfill-base-lang [--dry-run]` closes the two base-lang gaps
// the coverage metric (seasonfill_i18n_base_coverage) surfaces:
//
//  1. TMDB series (tmdb_id NOT NULL) that are MISSING an en-US series_texts
//     OR series_media_texts row → clear series.enrichment_tmdb_synced_at so
//     the running server's cold-start re-sweep (RunBackfillLoop →
//     BackfillSeries → ListMissingTMDBSync) re-enqueues them at PriorityCold
//     and the TMDB worker repopulates the base rows through the EXISTING
//     4.5-rps limiter. Paces over hours/days by design; safe to re-run.
//
//  2. Series WITHOUT tmdb_id (never TMDB-enrichable) MISSING an en-US
//     series_texts row → copy canon.title into series_texts{en-US} directly,
//     only-if-absent (INSERT … ON CONFLICT DO NOTHING). Sonarr titles are
//     latin, which is exactly the base-lang contract.
//
// Idempotent: a second run over a converged library nudges/copies zero rows.
// Reuses the same DB bootstrap path as backfill-assets / auth-mode.
package commands

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/logger"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/locale"
)

// BackfillBaseLang implements `seasonfill backfill-base-lang`. Flags:
//
//	--dry-run   count the two backfill sets without mutating anything
func BackfillBaseLang(args []string) error {
	fs := flag.NewFlagSet("backfill-base-lang", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "Count without mutating")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("load bootstrap config: %w", err)
	}
	log := logger.New(logger.Config{
		Level: cfg.Log.Level, Format: cfg.Log.Format, Output: os.Stderr,
	})

	db, err := database.Open(cfg.Database)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if sqlDB, dErr := db.DB(); dErr == nil {
			_ = sqlDB.Close()
		}
	}()
	if err := database.Migrate(db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	ctx := context.Background()
	res, err := runBackfillBaseLang(ctx, db, *dryRun, log)
	if err != nil {
		return err
	}
	verb := "nudged/copied"
	if *dryRun {
		verb = "would nudge/copy"
	}
	if _, err := fmt.Fprintf(os.Stdout,
		"backfill-base-lang: %s %d tmdb-series (re-enrich) + %d tmdb-less series (canon.title copy)%s\n",
		verb, res.TMDBNudged, res.CanonCopied, dryRunSuffix(*dryRun)); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}

func dryRunSuffix(dry bool) string {
	if dry {
		return " (dry-run)"
	}
	return ""
}

// BackfillBaseLangResult is the testable tally.
type BackfillBaseLangResult struct {
	// TMDBNudged is the number of TMDB series whose enrichment_tmdb_synced_at
	// was cleared (or would be, in dry-run) to trigger re-enrichment.
	TMDBNudged int64
	// CanonCopied is the number of tmdb-less series that received (or would
	// receive) a canon.title → series_texts{en-US} copy.
	CanonCopied int64
}

// runBackfillBaseLang is the testable core.
//
//   - dryRun=true  → COUNT both sets, mutate nothing.
//   - dryRun=false → clear enrichment_tmdb_synced_at on the deficient TMDB
//     set; INSERT … ON CONFLICT DO NOTHING the canon.title copies.
//
// The two sets are disjoint by construction (tmdb_id NOT NULL vs IS NULL),
// so ordering is irrelevant and the whole thing is idempotent.
func runBackfillBaseLang(ctx context.Context, db *gorm.DB, dryRun bool, log *slog.Logger) (BackfillBaseLangResult, error) {
	base := locale.Default() // "en-US"
	var out BackfillBaseLangResult

	// --- Set 1: TMDB series missing an en-US series_texts OR series_media_texts row.
	tmdbDeficient := db.WithContext(ctx).
		Table("series").
		Where("tmdb_id IS NOT NULL").
		Where(`(NOT EXISTS (SELECT 1 FROM series_texts st
		           WHERE st.series_id = series.id AND st.language = ?)
		        OR NOT EXISTS (SELECT 1 FROM series_media_texts smt
		           WHERE smt.series_id = series.id AND smt.language = ?))`,
			base, base)

	if dryRun {
		var n int64
		if err := tmdbDeficient.Session(&gorm.Session{}).Count(&n).Error; err != nil {
			return out, fmt.Errorf("count tmdb-deficient series: %w", err)
		}
		out.TMDBNudged = n
	} else {
		res := tmdbDeficient.Session(&gorm.Session{}).Updates(map[string]any{
			"enrichment_tmdb_synced_at": gorm.Expr("NULL"),
			"updated_at":                time.Now().UTC(),
		})
		if res.Error != nil {
			return out, fmt.Errorf("nudge tmdb-deficient series: %w", res.Error)
		}
		out.TMDBNudged = res.RowsAffected
		log.InfoContext(ctx, "backfill_base_lang.tmdb_nudged",
			slog.Int64("rows", res.RowsAffected))
	}

	// --- Set 2: tmdb-less series missing an en-US series_texts row → copy canon.title.
	if dryRun {
		var n int64
		if err := db.WithContext(ctx).
			Table("series AS s").
			Where("s.tmdb_id IS NULL").
			Where("s.title IS NOT NULL AND s.title <> ''").
			Where(`NOT EXISTS (SELECT 1 FROM series_texts st
			         WHERE st.series_id = s.id AND st.language = ?)`, base).
			Count(&n).Error; err != nil {
			return out, fmt.Errorf("count tmdb-less series: %w", err)
		}
		out.CanonCopied = n
	} else {
		// INSERT … SELECT … ON CONFLICT DO NOTHING keeps this atomic and
		// only-if-absent in one round-trip. ? binds are dialect-portable
		// (Postgres prod + SQLite test lane).
		res := db.WithContext(ctx).Exec(`
			INSERT INTO series_texts (series_id, language, title, updated_at)
			SELECT s.id, ?, s.title, ?
			  FROM series s
			 WHERE s.tmdb_id IS NULL
			   AND s.title IS NOT NULL AND s.title <> ''
			   AND NOT EXISTS (SELECT 1 FROM series_texts st
			         WHERE st.series_id = s.id AND st.language = ?)
			ON CONFLICT (series_id, language) DO NOTHING`,
			base, time.Now().UTC(), base)
		if res.Error != nil {
			return out, fmt.Errorf("copy canon.title to series_texts: %w", res.Error)
		}
		out.CanonCopied = res.RowsAffected
		log.InfoContext(ctx, "backfill_base_lang.canon_copied",
			slog.Int64("rows", res.RowsAffected))
	}

	return out, nil
}
