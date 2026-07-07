//go:build integration

// Story 1083 — verifies migrations apply cleanly through 000035 on both
// backends, exercises the people_texts composite PK + people FK CASCADE, and
// rolls back clean.
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func insertPeopleTextSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO people_texts (person_id, language, name) VALUES ($1, $2, $3)`
	case "sqlite":
		return `INSERT INTO people_texts (person_id, language, name) VALUES (?, ?, ?)`
	}
	return ""
}

func deletePersonByIDSQL(driver string) string {
	switch driver {
	case "postgres":
		return `DELETE FROM people WHERE id = $1`
	case "sqlite":
		return `DELETE FROM people WHERE id = ?`
	}
	return ""
}

func countPeopleTextsSQL(driver string) string {
	switch driver {
	case "postgres":
		return `SELECT COUNT(*) FROM people_texts WHERE person_id = $1`
	case "sqlite":
		return `SELECT COUNT(*) FROM people_texts WHERE person_id = ?`
	}
	return ""
}

func TestStory1083_PeopleTextsMigrationRoundTrip(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			require.NoError(t, m.Up()) // applies through 000035

			// insertPersonSQL is defined in s_g_person_credits_texts_apply_test.go
			// (same package).
			var personID int64
			require.NoError(t, db.QueryRowContext(ctx, insertPersonSQL(b.name), "p-"+uuid.NewString()).Scan(&personID))
			require.Greater(t, personID, int64(0))

			// Happy insert — two languages for the same person.
			_, err := db.ExecContext(ctx, insertPeopleTextSQL(b.name), personID, "en-US", "Adam Scott")
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, insertPeopleTextSQL(b.name), personID, "ru-RU", "Адам Скотт")
			require.NoError(t, err)

			// Composite PK: dup (person_id, language) must fail.
			_, err = db.ExecContext(ctx, insertPeopleTextSQL(b.name), personID, "en-US", "Dup")
			require.Error(t, err, "duplicate composite PK should violate PK")

			// FK: orphan person_id must fail.
			_, err = db.ExecContext(ctx, insertPeopleTextSQL(b.name), int64(999999), "en-US", "Orphan")
			require.Error(t, err, "orphan person_id should fail FK")

			// CASCADE: deleting the person removes its texts.
			_, err = db.ExecContext(ctx, deletePersonByIDSQL(b.name), personID)
			require.NoError(t, err)
			var remaining int
			require.NoError(t, db.QueryRowContext(ctx, countPeopleTextsSQL(b.name), personID).Scan(&remaining))
			require.Equal(t, 0, remaining, "ON DELETE CASCADE should remove texts rows")

			// DOWN — table gone.
			require.NoError(t, m.Down())
			_, err = db.ExecContext(ctx, "SELECT 1 FROM people_texts LIMIT 1")
			require.Error(t, err, "people_texts should be dropped after Down")
		})
	}
}
