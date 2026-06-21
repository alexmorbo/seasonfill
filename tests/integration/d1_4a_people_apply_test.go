//go:build integration

// D-1-4a (story 457a) — verifies 000001..000005 apply cleanly on both
// backends, exercises insert + FK + composite PK + unique index
// enforcement on the new people-domain tables, and rolls back to clean
// state. Uses the shared d1_helpers extracted from D-1-2/3a.
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestD14a_PeopleMigrationRoundTrip(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			// UP — applies 000001..000005 in sequence.
			require.NoError(t, m.Up())

			// Seed: insert a person row.
			personName := "d1-4a-" + uuid.NewString()
			var personID int64
			switch b.name {
			case "postgres":
				row := db.QueryRowContext(ctx,
					`INSERT INTO people (hydration, name, created_at, updated_at)
					 VALUES ($1, $2, now(), now()) RETURNING id`,
					"stub", personName)
				require.NoError(t, row.Scan(&personID))
			case "sqlite":
				res, err := db.ExecContext(ctx,
					`INSERT INTO people (hydration, name, created_at, updated_at)
					 VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
					"stub", personName)
				require.NoError(t, err)
				personID, err = res.LastInsertId()
				require.NoError(t, err)
			default:
				t.Fatalf("unknown driver %q", b.name)
			}
			require.Greater(t, personID, int64(0))

			// person_biographies happy path.
			_, err := db.ExecContext(ctx,
				personBioInsertSQL(b.name),
				personID, "en-US", "English bio")
			require.NoError(t, err, "person_biographies insert should succeed")

			// Composite PK violation: duplicate (person_id, language).
			_, err = db.ExecContext(ctx,
				personBioInsertSQL(b.name),
				personID, "en-US", "DUPE")
			require.Error(t, err, "duplicate (person_id, language) should fail PK")

			// Different language is fine.
			_, err = db.ExecContext(ctx,
				personBioInsertSQL(b.name),
				personID, "ru-RU", "Русский bio")
			require.NoError(t, err, "second language should succeed")

			// Orphan biography: non-existent person_id.
			_, err = db.ExecContext(ctx,
				personBioInsertSQL(b.name),
				999999, "en-US", "Orphan")
			require.Error(t, err, "orphan person_biographies should fail FK")

			// person_credits happy path.
			creditID := "credit-" + uuid.NewString()
			_, err = db.ExecContext(ctx,
				personCreditInsertSQL(b.name),
				personID, creditID, "tv", 12345, "Sample Title", "actor")
			require.NoError(t, err, "person_credits insert should succeed")

			// Unique index violation: same (person_id, tmdb_credit_id).
			_, err = db.ExecContext(ctx,
				personCreditInsertSQL(b.name),
				personID, creditID, "tv", 67890, "DUPE Title", "actor")
			require.Error(t, err, "duplicate (person_id, tmdb_credit_id) should fail unique index")

			// Different tmdb_credit_id same person — OK.
			_, err = db.ExecContext(ctx,
				personCreditInsertSQL(b.name),
				personID, "credit-"+uuid.NewString(), "tv", 11111, "Other", "actor")
			require.NoError(t, err, "different credit_id should succeed")

			// Orphan credit: non-existent person_id.
			_, err = db.ExecContext(ctx,
				personCreditInsertSQL(b.name),
				999999, "orphan-"+uuid.NewString(), "tv", 22222, "Orphan", "actor")
			require.Error(t, err, "orphan person_credits should fail FK")

			// DOWN — rolls back 000005 then earlier migrations.
			require.NoError(t, m.Down())
			_, err = db.ExecContext(ctx, "SELECT 1 FROM people LIMIT 1")
			require.Error(t, err, "people should be dropped after Down")
			_, err = db.ExecContext(ctx, "SELECT 1 FROM person_credits LIMIT 1")
			require.Error(t, err, "person_credits should be dropped after Down")
			_, err = db.ExecContext(ctx, "SELECT 1 FROM person_biographies LIMIT 1")
			require.Error(t, err, "person_biographies should be dropped after Down")
		})
	}
}

func personBioInsertSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO person_biographies (person_id, language, biography, updated_at)
		        VALUES ($1, $2, $3, now())`
	case "sqlite":
		return `INSERT INTO person_biographies (person_id, language, biography, updated_at)
		        VALUES (?, ?, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

func personCreditInsertSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO person_credits
		        (person_id, tmdb_credit_id, media_type, tmdb_media_id, title, kind, created_at, updated_at)
		        VALUES ($1, $2, $3, $4, $5, $6, now(), now())`
	case "sqlite":
		return `INSERT INTO person_credits
		        (person_id, tmdb_credit_id, media_type, tmdb_media_id, title, kind, created_at, updated_at)
		        VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}
