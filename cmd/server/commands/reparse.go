package commands

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/alexmorbo/seasonfill/application/bootstrap"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/internal/admin/infrastructure/ratelimit"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	"github.com/alexmorbo/seasonfill/internal/config"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	grabpersistence "github.com/alexmorbo/seasonfill/internal/grab/persistence"
	"github.com/alexmorbo/seasonfill/internal/logger"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/reload"
)

// Reparse is the CLI entry point for `seasonfill grabs reparse`.
// Initializes DB and Sonarr clients, then delegates to reparseInternal.
func Reparse(ctx context.Context, args []string) error {
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
	if sqlDB, err := db.DB(); err == nil {
		defer func() { _ = sqlDB.Close() }()
	}

	if err := database.Migrate(db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	instanceRepo := repositories.NewSonarrInstanceRepository(db)
	bgCtx := context.Background()

	// Resolve API key and cipher.
	tempRuntimeRepo := catalogpersistence.NewRuntimeConfigRepository(db, nil)
	masterKey, err := bootstrap.ResolveAPIKey(bgCtx, cfg.Auth.APIKey, tempRuntimeRepo, log)
	if err != nil {
		return fmt.Errorf("resolve api key: %w", err)
	}
	cipher, err := crypto.New(masterKey)
	if err != nil {
		return fmt.Errorf("derive cipher: %w", err)
	}

	instances, err := instanceRepo.List(bgCtx, cipher)
	if err != nil {
		return fmt.Errorf("list instances: %w", err)
	}
	for i := range instances {
		runtime.ApplyInstanceDefaults(&instances[i])
	}

	// Build Sonarr clients. Use unlimited rate limiting for CLI.
	var globalLimiterPtr atomic.Pointer[ratelimit.Limiter]
	globalLimiterPtr.Store(ratelimit.NewFromRPMWithOptions(0, 0))
	clientFactory := reload.NewSonarrClientFactory(&globalLimiterPtr, log)
	sonarrClientsByName := make(map[string]ports.SonarrClient, len(instances))
	for _, sc := range instances {
		sonarrClientsByName[sc.Name] = clientFactory(sc)
	}

	clientFor := func(name string) (ports.SonarrClient, bool) {
		c, ok := sonarrClientsByName[name]
		return c, ok
	}

	grabRepo := grabpersistence.NewGrabRepository(db)
	return reparseInternal(ctx, args, grabRepo, clientFor, log)
}

// reparseInternal implements `seasonfill grabs reparse --since=<dur>` —
// iterates every grab_records row whose parsed_at IS NULL AND
// created_at >= now - since, calls Sonarr /api/v3/parse for each,
// and writes the result via repo.UpdateParsed. --dry-run prints the
// rowcount without writing.
func reparseInternal(
	ctx context.Context,
	args []string,
	grabs ports.GrabRepository,
	clientFor func(name string) (ports.SonarrClient, bool),
	logger *slog.Logger,
) error {
	fs := flag.NewFlagSet("grabs reparse", flag.ContinueOnError)
	since := fs.Duration("since", 24*time.Hour, "look back this far from now")
	dryRun := fs.Bool("dry-run", false, "report rowcount but skip writes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cutoff := time.Now().UTC().Add(-*since)
	recs, err := grabs.ListUnparsedSince(ctx, cutoff, 10000)
	if err != nil {
		return fmt.Errorf("list unparsed: %w", err)
	}
	logger.InfoContext(ctx, "reparse_starting",
		slog.Int("rows", len(recs)), slog.Duration("since", *since), slog.Bool("dry_run", *dryRun))
	if *dryRun {
		_, _ = fmt.Fprintf(os.Stdout, "reparse: %d rows would be processed (dry-run)\n", len(recs))
		return nil
	}
	ok, errCount := 0, 0
	for _, rec := range recs {
		client, present := clientFor(string(rec.InstanceName))
		if !present || client == nil {
			logger.WarnContext(ctx, "reparse_skip_no_client",
				slog.String("instance", string(rec.InstanceName)), slog.String("grab_id", rec.ID.String()))
			continue
		}
		pr, perr := client.ParseRelease(ctx, rec.ReleaseTitle)
		now := time.Now().UTC()
		if perr != nil {
			observability.IncParseRelease(rec.InstanceName, "error")
			errCount++
			logger.WarnContext(ctx, "reparse_parse_failed",
				slog.String("grab_id", rec.ID.String()), slog.String("error", perr.Error()))
			continue
		}
		extras := sonarr.ExtractExtras(rec.ReleaseTitle)
		merged := sonarr.MergeParse(sonarr.ParseResult{
			Quality:      pr.Quality,
			Source:       pr.Source,
			Resolution:   pr.Resolution,
			Languages:    pr.Languages,
			ReleaseGroup: pr.ReleaseGroup,
		}, extras)
		var payload *grab.Parsed
		if !merged.IsZero() {
			payload = &merged
		}
		if uerr := grabs.UpdateParsed(ctx, rec.ID, payload, now); uerr != nil {
			observability.IncParseRelease(rec.InstanceName, "error")
			errCount++
			logger.WarnContext(ctx, "reparse_persist_failed",
				slog.String("grab_id", rec.ID.String()), slog.String("error", uerr.Error()))
			continue
		}
		observability.IncParseRelease(rec.InstanceName, "ok")
		ok++
	}
	_, _ = fmt.Fprintf(os.Stdout, "reparse done: ok=%d errors=%d total=%d\n", ok, errCount, len(recs))
	return nil
}
