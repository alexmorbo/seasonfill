// backfill_collection_i18n.go — F-08 S2 one-shot backfill for collection_texts.
//
// `seasonfill backfill-collection-i18n [--dry-run] [--delay <dur>]` iterates
// every row in `collections`, fetches /collection/{id}?append_to_response=
// translations ONCE, and writes an en-US + ru-RU collection_texts row per the
// two skip guards (mirror of the movie worker fan-out). Idempotent: the
// CollectionTextsRepository COALESCE upsert means a second run over a converged
// library writes the same rows — no dup, no blank-out.
//
// THROTTLED: every GetCollection flows through the shared TMDB token-bucket
// limiter inside Client.do; --delay (default 50ms) adds an extra inter-collection
// pause so a 1832-collection sweep stays gentle. Unlike backfill-base-lang (which
// only nudges DB timestamps for the running server to re-enrich) this command
// fetches + writes directly, because collections have NO re-enrichment loop.
package commands

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/internal/config"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/logger"
	infraextsvc "github.com/alexmorbo/seasonfill/internal/shared/clients/externalservices"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/locale"
	"github.com/alexmorbo/seasonfill/internal/wiring"
)

// collectionTMDBFetcher is the narrow GetCollection seam the backfill core
// consumes — satisfied by *tmdb.Client and by a test fake.
type collectionTMDBFetcher interface {
	GetCollection(ctx context.Context, id int64, language string) (*tmdb.CollectionResponse, error)
}

// BackfillCollectionI18nResult is the testable tally.
type BackfillCollectionI18nResult struct {
	Collections int64 // rows scanned in `collections`
	RowsWritten int64 // collection_texts rows upserted
}

// BackfillCollectionI18n implements `seasonfill backfill-collection-i18n`.
func BackfillCollectionI18n(args []string) error {
	fs := flag.NewFlagSet("backfill-collection-i18n", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "Count collections without fetching or writing")
	delay := fs.Duration("delay", 50*time.Millisecond, "Extra pause between collections")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logger.New(logger.Config{Level: cfg.Log.Level, Format: cfg.Log.Format, Output: os.Stderr})

	ctx := context.Background()
	persistence, err := wiring.BuildPersistence(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("build persistence: %w", err)
	}
	defer func() {
		if sqlDB, dErr := persistence.DB.DB(); dErr == nil {
			_ = sqlDB.Close()
		}
	}()

	var fetcher collectionTMDBFetcher
	if !*dryRun {
		extRepo := infraextsvc.NewRepository(persistence.DB, persistence.Cipher)
		dbSettings, gErr := extRepo.Get(ctx, infraextsvc.ServiceTMDB)
		if gErr != nil && !errors.Is(gErr, ports.ErrNotFound) {
			return fmt.Errorf("load tmdb settings: %w", gErr)
		}
		settings := infraextsvc.Merge(infraextsvc.ServiceTMDB, dbSettings, os.Getenv)
		if settings.APIKey == "" {
			return errors.New("tmdb api key not configured (set SEASONFILL_TMDB_TOKEN or configure via UI)")
		}
		client, cErr := adapters.BuildTMDBClient(settings, adapters.TMDBClientFactoryConfig{
			Language: tmdb.DefaultLanguage,
			Logger:   log,
		})
		if cErr != nil {
			return fmt.Errorf("build tmdb client: %w", cErr)
		}
		fetcher = client
	}

	res, err := runBackfillCollectionI18n(ctx, persistence.DB, fetcher, *dryRun, *delay, log)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(os.Stdout,
		"backfill-collection-i18n: %d collections, %d text rows written%s\n",
		res.Collections, res.RowsWritten, dryRunSuffix(*dryRun)); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}

// runBackfillCollectionI18n is the testable core. dryRun (or a nil fetcher)
// counts collections and writes nothing. Fetch/write failures are logged and
// skipped (best-effort) — one bad collection never aborts the sweep.
func runBackfillCollectionI18n(
	ctx context.Context,
	db *gorm.DB,
	fetcher collectionTMDBFetcher,
	dryRun bool,
	delay time.Duration,
	log *slog.Logger,
) (BackfillCollectionI18nResult, error) {
	var out BackfillCollectionI18nResult

	type collRow struct {
		ID               int64
		TMDBCollectionID int
	}
	var rows []collRow
	if err := db.WithContext(ctx).
		Table("collections").
		Select("id", "tmdb_collection_id").
		Order("id ASC").
		Scan(&rows).Error; err != nil {
		return out, fmt.Errorf("list collections: %w", err)
	}
	out.Collections = int64(len(rows))
	if dryRun || fetcher == nil {
		return out, nil
	}

	texts := enrichpersistence.NewCollectionTextsRepository(db)
	baseShort := shortLangCLI(tmdb.DefaultLanguage)

	for _, c := range rows {
		if c.TMDBCollectionID == 0 {
			continue
		}
		resp, err := fetcher.GetCollection(ctx, int64(c.TMDBCollectionID), tmdb.DefaultLanguage)
		if err != nil {
			log.WarnContext(ctx, "backfill_collection_i18n.fetch_failed",
				slog.Int("tmdb_collection_id", c.TMDBCollectionID), slog.String("error", err.Error()))
			continue
		}
		if resp == nil {
			continue
		}
		trByLang := collectionTrByLangCLI(resp)
		now := time.Now().UTC()
		for _, lang := range locale.SupportedUserLanguages {
			name, overview := resp.Name, resp.Overview
			if shortLangCLI(lang) != baseShort {
				tr, ok := trByLang[shortLangCLI(lang)]
				if !ok {
					continue
				}
				name, overview = tr.Title, tr.Overview
				if name == "" {
					continue
				}
			}
			if werr := texts.UpsertCollectionTexts(ctx, c.ID, lang, name, overview, now); werr != nil {
				log.WarnContext(ctx, "backfill_collection_i18n.write_failed",
					slog.Int64("collection_id", c.ID), slog.String("lang", lang), slog.String("error", werr.Error()))
				continue
			}
			out.RowsWritten++
		}
		if delay > 0 {
			select {
			case <-ctx.Done():
				return out, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	log.InfoContext(ctx, "backfill_collection_i18n.done",
		slog.Int64("collections", out.Collections), slog.Int64("rows_written", out.RowsWritten))
	return out, nil
}

// shortLangCLI mirrors enrichment.shortLang (bare primary subtag, lowercased) —
// duplicated here because that helper is unexported in the app package.
func shortLangCLI(lang string) string {
	if i := strings.IndexByte(lang, '-'); i >= 0 {
		lang = lang[:i]
	}
	return strings.ToLower(lang)
}

// collectionTrByLangCLI indexes translations by bare language code (mirror of
// enrichment.collectionTranslationsByLang).
func collectionTrByLangCLI(resp *tmdb.CollectionResponse) map[string]tmdb.MovieTranslationData {
	out := map[string]tmdb.MovieTranslationData{}
	if resp == nil || resp.Translations == nil {
		return out
	}
	for i := range resp.Translations.Translations {
		t := &resp.Translations.Translations[i]
		out[shortLangCLI(t.ISO6391)] = t.Data
	}
	return out
}
