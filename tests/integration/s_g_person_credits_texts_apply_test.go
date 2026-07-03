//go:build integration

// S-G — verifies migrations apply cleanly through 000029 on both backends,
// exercises the person_credits_texts composite PK + person_credits FK CASCADE,
// and rolls back clean.
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func insertPersonSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO people (name, hydration) VALUES ($1, 'stub') RETURNING id`
	case "sqlite":
		return `INSERT INTO people (name, hydration) VALUES (?, 'stub') RETURNING id`
	}
	return ""
}

func insertPersonCreditSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO person_credits (person_id, tmdb_credit_id, media_type, tmdb_media_id, title, kind) VALUES ($1, $2, 'tv', 100, 'T', 'cast') RETURNING id`
	case "sqlite":
		return `INSERT INTO person_credits (person_id, tmdb_credit_id, media_type, tmdb_media_id, title, kind) VALUES (?, ?, 'tv', 100, 'T', 'cast') RETURNING id`
	}
	return ""
}

func insertPersonCreditTextSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO person_credits_texts (person_credit_id, language, character_name) VALUES ($1, $2, $3)`
	case "sqlite":
		return `INSERT INTO person_credits_texts (person_credit_id, language, character_name) VALUES (?, ?, ?)`
	}
	return ""
}

func deletePersonCreditByIDSQL(driver string) string {
	switch driver {
	case "postgres":
		return `DELETE FROM person_credits WHERE id = $1`
	case "sqlite":
		return `DELETE FROM person_credits WHERE id = ?`
	}
	return ""
}

func countPersonCreditTextsSQL(driver string) string {
	switch driver {
	case "postgres":
		return `SELECT COUNT(*) FROM person_credits_texts WHERE person_credit_id = $1`
	case "sqlite":
		return `SELECT COUNT(*) FROM person_credits_texts WHERE person_credit_id = ?`
	}
	return ""
}

func TestSG_PersonCreditsTextsMigrationRoundTrip(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			require.NoError(t, m.Up()) // applies through 000029

			var personID int64
			require.NoError(t, db.QueryRowContext(ctx, insertPersonSQL(b.name), "p-"+uuid.NewString()).Scan(&personID))
			require.Greater(t, personID, int64(0))

			var creditID int64
			require.NoError(t, db.QueryRowContext(ctx, insertPersonCreditSQL(b.name), personID, "cr-"+uuid.NewString()).Scan(&creditID))
			require.Greater(t, creditID, int64(0))

			// Happy insert — two languages for the same credit.
			_, err := db.ExecContext(ctx, insertPersonCreditTextSQL(b.name), creditID, "en-US", "Rick")
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, insertPersonCreditTextSQL(b.name), creditID, "ru-RU", "Рик")
			require.NoError(t, err)

			// Composite PK: dup (person_credit_id, language) must fail.
			_, err = db.ExecContext(ctx, insertPersonCreditTextSQL(b.name), creditID, "en-US", "Dup")
			require.Error(t, err, "duplicate composite PK should violate PK")

			// FK: orphan person_credit_id must fail.
			_, err = db.ExecContext(ctx, insertPersonCreditTextSQL(b.name), int64(999999), "en-US", "Orphan")
			require.Error(t, err, "orphan person_credit_id should fail FK")

			// CASCADE: deleting the credit removes its texts.
			// (sqlite harness opens with _pragma=foreign_keys(1); postgres enforces natively.)
			_, err = db.ExecContext(ctx, deletePersonCreditByIDSQL(b.name), creditID)
			require.NoError(t, err)
			var remaining int
			require.NoError(t, db.QueryRowContext(ctx, countPersonCreditTextsSQL(b.name), creditID).Scan(&remaining))
			require.Equal(t, 0, remaining, "ON DELETE CASCADE should remove texts rows")

			// DOWN — table gone.
			require.NoError(t, m.Down())
			_, err = db.ExecContext(ctx, "SELECT 1 FROM person_credits_texts LIMIT 1")
			require.Error(t, err, "person_credits_texts should be dropped after Down")
		})
	}
}
