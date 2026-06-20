package commands

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/alexmorbo/seasonfill/application/bootstrap"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/runtimeconfig"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/logger"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

// AuthMode implements `seasonfill auth-mode`. Two modes (mutually
// exclusive):
//
//	--get          print current auth mode + exit 0
//	--set <mode>   set auth mode (one of forms|basic|none), bump
//	               SessionEpoch, exit 0
//
// Reuses the same DB bootstrap path as reset-password so a sysadmin
// who can run one command can run the other. Idempotent: --set forms
// on a row already in forms still bumps the epoch (the operator is
// explicitly invalidating live sessions, that's the whole point of
// the rescue command).
func AuthMode(args []string) error {
	fs := flag.NewFlagSet("auth-mode", flag.ContinueOnError)
	getMode := fs.Bool("get", false, "Print the current auth mode")
	setVal := fs.String("set", "", "Set the auth mode (forms|basic|none)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *getMode == (*setVal != "") {
		return errors.New("exactly one of --get or --set <mode> is required")
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

	instanceRepo := repositories.NewSonarrInstanceRepository(db)
	ctx := context.Background()

	// Need a cipher for the runtimeconfig.UseCase publish path, but
	// publish is best-effort here (no bus subscribers — the live
	// server process owns those). Resolve the master key the same
	// way main does so cipher init succeeds.
	tempRuntimeRepo := repositories.NewRuntimeConfigRepository(db, nil)
	masterKey, err := bootstrap.ResolveAPIKey(ctx, cfg.Auth.APIKey, tempRuntimeRepo, log)
	if err != nil {
		return fmt.Errorf("resolve api key: %w", err)
	}
	cipher, err := crypto.New(masterKey)
	if err != nil {
		return fmt.Errorf("derive cipher: %w", err)
	}
	runtimeRepo := repositories.NewRuntimeConfigRepository(db, cipher)

	uc := runtimeconfig.New(runtimeRepo, instanceRepo, cipher, nil, log)

	if *getMode {
		out, _, err := uc.Get(ctx)
		if err != nil {
			return fmt.Errorf("get runtime_config: %w", err)
		}
		if _, err := fmt.Fprintln(os.Stdout, out.Auth.Mode); err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
		return nil
	}

	mode := *setVal
	switch mode {
	case runtime.AuthModeForms, runtime.AuthModeBasic, runtime.AuthModeNone:
		// ok
	default:
		return fmt.Errorf("invalid mode %q (want forms|basic|none)", mode)
	}

	epoch, err := uc.SetAuthMode(ctx, mode)
	if err != nil {
		return fmt.Errorf("set auth mode: %w", err)
	}
	if _, err := fmt.Fprintf(os.Stdout, "auth mode set to %s (epoch=%d)\n", mode, epoch); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	log.Info("auth-mode.set",
		slog.String("mode", mode), slog.Int64("epoch", epoch))
	return nil
}
