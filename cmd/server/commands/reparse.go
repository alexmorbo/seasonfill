package commands

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/wiring"
)

// Reparse is the CLI entry point for `seasonfill grabs reparse`. D-5
// (466b) restores the bedrock plumbing — open DB, run migrations,
// resolve master key, build Sonarr instance list — so the command
// boots without panicking on the D-2 stubs. The actual replay loop
// (build Sonarr clients per instance, call grab.ReparseUseCase) is
// owned by D-6; until then this prints `reparse: no grab usecase
// yet — pending D-6 grab+watchdog rewrite` and exits 0 so operator
// scripts don't break.
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

	// TODO(D-6): wire grab.ReparseUseCase per instance + invoke the
	// replay loop. The grab bounded context is currently D-2-stubbed;
	// the use case lands in D-6.
	_ = errors.New
	_, _ = fmt.Fprintf(os.Stdout,
		"reparse: %d Sonarr instance(s) resolved — replay loop pending D-6 grab rewrite\n",
		len(instances))
	return nil
}
