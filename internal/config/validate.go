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
	return nil
}
