package commands

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"

	auth "github.com/alexmorbo/seasonfill/internal/admin/app"
	adminpersistence "github.com/alexmorbo/seasonfill/internal/admin/persistence"
	"github.com/alexmorbo/seasonfill/internal/config"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// ResetPassword implements `seasonfill reset-password --set <pw>`.
// Reads SEASONFILL_DATABASE_* env vars, opens the DB, applies any
// pending migrations, and persists a fresh bcrypt-cost-12 hash for the
// (single) admin user. Exits non-zero on any error.
func ResetPassword(args []string) error {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	setFlag := fs.String("set", "", "new password (plaintext; not logged)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	if *setFlag == "" {
		return errors.New("--set <password> is required")
	}
	if len(*setFlag) < auth.MinPasswordLen {
		return fmt.Errorf("password must be >=%d chars", auth.MinPasswordLen)
	}

	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	gormDB, err := database.Open(cfg.Database)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	if err := database.Migrate(gormDB); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	ctx := context.Background()
	repo := adminpersistence.NewUserRepository(gormDB)
	existing, err := repo.Get(ctx)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return errors.New("no admin user — run the pod once to bootstrap")
		}
		return fmt.Errorf("load user: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(*setFlag), auth.BcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := repo.UpdatePassword(ctx, existing.ID, string(hash)); err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return errors.New("no admin user — run the pod once to bootstrap")
		}
		return fmt.Errorf("update password: %w", err)
	}
	if _, err := fmt.Fprintln(os.Stdout, `{"status":"ok","username":"`+existing.Username+`"}`); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}
