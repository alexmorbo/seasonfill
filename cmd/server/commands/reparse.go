package commands

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	"github.com/alexmorbo/seasonfill/internal/config"
	grabapp "github.com/alexmorbo/seasonfill/internal/grab/app"
	grabpersistence "github.com/alexmorbo/seasonfill/internal/grab/persistence"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/wiring"
)

// Reparse is the CLI entry point for `seasonfill grabs reparse`. D-6
// (story 467c) wires the full replay loop: for every configured Sonarr
// instance, build a live client and invoke
// grab.ReparseUseCase.ReplayInstance against grab_records rows whose
// parsed_at IS NULL. Failure-isolated per instance — a single Sonarr
// outage logs WARN and the loop moves on to the next instance.
func Reparse(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("reparse", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	db, err := database.Open(cfg.Database)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	}()
	if err := database.Migrate(db); err != nil {
		return fmt.Errorf("migrate db: %w", err)
	}

	logger := newDiscardLogger()
	tempRuntimeRepo := catalogpersistence.NewRuntimeConfigRepository(db, nil)
	masterKey, err := wiring.ResolveAPIKey(ctx, cfg.Auth.APIKey, tempRuntimeRepo, logger)
	if err != nil {
		return fmt.Errorf("resolve api key: %w", err)
	}
	cipher, err := crypto.New(masterKey)
	if err != nil {
		return fmt.Errorf("derive cipher: %w", err)
	}
	instanceRepo := catalogpersistence.NewSonarrInstanceRepository(db)

	instances, err := instanceRepo.List(ctx, cipher)
	if err != nil {
		return fmt.Errorf("list instances: %w", err)
	}
	if len(instances) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "reparse: no Sonarr instances configured")
		return nil
	}

	grabRepo := grabpersistence.NewGrabRepository(db)
	totalProcessed := 0
	for _, inst := range instances {
		if inst.URL == "" || inst.APIKey == "" {
			_, _ = fmt.Fprintf(os.Stderr,
				"reparse: instance %q has no URL or APIKey — skipped\n", inst.Name)
			continue
		}
		timeout := inst.Timeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		client := sonarr.New(domain.InstanceName(inst.Name), inst.URL, inst.APIKey, timeout, logger)
		uc := grabapp.NewReparseUseCase(grabRepo, client, logger)
		count, err := uc.ReplayInstance(ctx, domain.InstanceName(inst.Name))
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr,
				"reparse: instance %q failed: %v\n", inst.Name, err)
			continue
		}
		_, _ = fmt.Fprintf(os.Stdout,
			"reparse: instance %q processed %d grab_records\n", inst.Name, count)
		totalProcessed += count
	}

	if totalProcessed == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "reparse: no unparsed grabs found")
	} else {
		_, _ = fmt.Fprintf(os.Stdout,
			"reparse: %d row(s) parsed across %d instance(s)\n",
			totalProcessed, len(instances))
	}
	return nil
}
