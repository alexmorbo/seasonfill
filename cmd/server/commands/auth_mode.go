package commands

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/runtimeconfig"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/logger"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/wiring"
)

func newDiscardLogger() *slog.Logger {
	return logger.New(logger.Config{
		Level:  "error",
		Format: "json",
		Output: io.Discard,
	})
}

// AuthMode implements `seasonfill auth-mode --get` and `--set <mode>`.
// The --set path opens the configured database, resolves the master
// API key (or auto-generates one), instantiates a runtimeconfig
// use case, and calls SetAuthMode — which switches mode + bumps the
// session epoch atomically. Prints `{"mode":"...","session_epoch":n}`
// on stdout for the operator to capture.
func AuthMode(args []string) error {
	fs := flag.NewFlagSet("auth-mode", flag.ContinueOnError)
	getFlag := fs.Bool("get", false, "print current auth mode")
	setFlag := fs.String("set", "", "set auth mode (forms|basic|none|oidc)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	if !*getFlag && *setFlag == "" {
		return errors.New("--get or --set <mode> required")
	}
	if *getFlag && *setFlag != "" {
		return errors.New("--get and --set are mutually exclusive")
	}

	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx := context.Background()
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

	// Resolve master key via the bootstrap helper so the cipher can
	// decrypt the OIDC secret if the operator switches to oidc with
	// an already-set client_secret.
	tempRuntimeRepo := catalogpersistence.NewRuntimeConfigRepository(db, nil)
	logger := newDiscardLogger()
	masterKey, err := wiring.ResolveAPIKey(ctx, cfg.Auth.APIKey, tempRuntimeRepo, logger)
	if err != nil {
		return fmt.Errorf("resolve api key: %w", err)
	}
	cipher, err := crypto.New(masterKey)
	if err != nil {
		return fmt.Errorf("derive cipher: %w", err)
	}
	runtimeRepo := catalogpersistence.NewRuntimeConfigRepository(db, cipher)
	instanceRepo := catalogpersistence.NewSonarrInstanceRepository(db)

	if *getFlag {
		row, err := runtimeRepo.Get(ctx)
		switch {
		case err == nil:
			if _, werr := fmt.Fprintf(os.Stdout, "%s\n", row.Auth.Mode); werr != nil {
				return fmt.Errorf("write stdout: %w", werr)
			}
			return nil
		case errors.Is(err, ports.ErrNotFound):
			if _, werr := fmt.Fprintf(os.Stdout, "%s\n", runtime.Defaults().Auth.Mode); werr != nil {
				return fmt.Errorf("write stdout: %w", werr)
			}
			return nil
		default:
			return fmt.Errorf("get runtime config: %w", err)
		}
	}

	mode := *setFlag
	if !validAuthMode(mode) {
		return fmt.Errorf("invalid mode %q: must be one of forms|basic|none|oidc", mode)
	}

	// The bus is intentionally nil — there's no live HTTP server to
	// publish to from a CLI invocation. SetAuthMode handles nil-bus
	// gracefully (logs a warn on publish, swallows the error).
	uc := runtimeconfig.New(runtimeRepo, instanceRepo, cipher, nil, logger)
	if cfg.Auth.OIDCClientSecret != "" {
		uc = uc.WithClientSecretEnv(cfg.Auth.OIDCClientSecret)
	}

	epoch, err := uc.SetAuthMode(ctx, mode)
	if err != nil {
		return fmt.Errorf("set auth mode: %w", err)
	}

	if _, werr := fmt.Fprintf(os.Stdout, `{"mode":%q,"session_epoch":%d}`+"\n", mode, epoch); werr != nil {
		return fmt.Errorf("write stdout: %w", werr)
	}
	return nil
}

func validAuthMode(m string) bool {
	switch m {
	case runtime.AuthModeForms, runtime.AuthModeBasic, runtime.AuthModeNone, runtime.AuthModeOIDC:
		return true
	}
	return false
}
