// Story 346 — break-glass CLI for the canon-images recovery sweep.
//
// `seasonfill backfill-assets --kind=poster|backdrop [--dry-run]`
// demotes the hydration of canon rows missing the named asset column
// from 'full' to 'partial' so the cold-start scheduler re-enqueues
// them for the next enrichment tick. Idempotent; rescue tool only —
// the Story 319 cold-start sweep normally converges without it. Use
// when the sweep ran but the backlog isn't shrinking (defensive guard
// disabled, sweep loop blocked, etc.).
//
// Reuses the same DB bootstrap path as reset-password / auth-mode so a
// sysadmin who can run one rescue command can run this one.

package commands

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/logger"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// AssetKindPoster is the --kind=poster sentinel.
const AssetKindPoster = "poster"

// AssetKindBackdrop is the --kind=backdrop sentinel.
const AssetKindBackdrop = "backdrop"

// BackfillAssets implements `seasonfill backfill-assets`. Flags:
//
//	--kind <poster|backdrop>   which asset column to scan (required)
//	--dry-run                  count + log without mutating hydration
func BackfillAssets(args []string) error {
	fs := flag.NewFlagSet("backfill-assets", flag.ContinueOnError)
	kind := fs.String("kind", "", "Asset kind to backfill (poster|backdrop)")
	dryRun := fs.Bool("dry-run", false, "Count without mutating hydration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *kind == "" {
		return errors.New("--kind=poster|backdrop is required")
	}
	if *kind != AssetKindPoster && *kind != AssetKindBackdrop {
		return fmt.Errorf("invalid --kind=%q (want poster|backdrop)", *kind)
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
	rows, err := runBackfillAssets(ctx, db, *kind, *dryRun, log)
	if err != nil {
		return err
	}
	if *dryRun {
		if _, err := fmt.Fprintf(os.Stdout, "backfill-assets: %d row(s) would be demoted (dry-run, kind=%s)\n", rows, *kind); err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(os.Stdout, "backfill-assets: %d row(s) demoted to hydration=partial (kind=%s)\n", rows, *kind); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}

// runBackfillAssets is the testable core. Selects the population
// (tmdb_id NOT NULL, hydration='full', the named asset column IS NULL).
// dryRun=true → COUNT(*); false → UPDATE hydration='partial' over the set.
func runBackfillAssets(ctx context.Context, db *gorm.DB, kind string, dryRun bool, log *slog.Logger) (int64, error) {
	col := kind + "_asset"
	base := db.WithContext(ctx).
		Table("series").
		Where("tmdb_id IS NOT NULL").
		Where("hydration = ?", "full").
		Where(col + " IS NULL")
	if dryRun {
		var n int64
		if err := base.Count(&n).Error; err != nil {
			return 0, fmt.Errorf("count rows: %w", err)
		}
		log.InfoContext(ctx, "backfill_assets.dry_run",
			slog.String("kind", kind),
			slog.Int64("would_demote", n),
		)
		return n, nil
	}
	res := base.Update("hydration", "partial")
	if res.Error != nil {
		return 0, fmt.Errorf("demote rows: %w", res.Error)
	}
	log.InfoContext(ctx, "backfill_assets.demoted",
		slog.String("kind", kind),
		slog.Int64("rows_affected", res.RowsAffected),
	)
	return res.RowsAffected, nil
}
