package config

import "fmt"

// Validate enforces the Bootstrap invariants. Anything covered by
// runtime DB schema (cron, scan, sonarr_instances, auth runtime
// fields) is validated at the HTTP-CRUD layer in 027b/c.
func (c *Bootstrap) Validate() error {
	switch c.Database.Driver {
	case "sqlite":
		if c.Database.SQLite.Path == "" {
			return ErrSQLitePath
		}
	case "postgres":
		if c.Database.Postgres.DSN == "" {
			return ErrPostgresDSN
		}
	default:
		return fmt.Errorf("%w: %s", ErrUnknownDriver, c.Database.Driver)
	}
	if c.Auth.WebPassword != "" && c.Auth.WebPasswordHash != "" {
		return ErrPasswordMutex
	}
	return c.MediaStore.Validate()
}

// Validate enforces the per-mode invariants of MediaStoreConfig. The
// store package re-checks these at construction time so callers that
// bypass Bootstrap (tests, future CLI tools) stay covered.
func (m *MediaStoreConfig) Validate() error {
	switch m.Mode {
	case "", "off":
		return nil
	case "s3":
		if m.S3.Endpoint == "" || m.S3.Bucket == "" || m.S3.AccessKey == "" || m.S3.SecretKey == "" {
			return ErrMediaStoreS3Missing
		}
		return nil
	case "fs":
		if m.FSPath == "" {
			return ErrMediaStoreFSMissing
		}
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrMediaStoreMode, m.Mode)
	}
}
