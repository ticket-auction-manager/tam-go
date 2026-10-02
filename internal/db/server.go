package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"
)

// MigrateServer applies the schema additions only tam-server needs, after
// Migrate: the client_saves, event, deleted_prefixes and auth_key_activity
// tables. It is safe to run on every start.
func MigrateServer(sqldb *sql.DB) error {
	// client_saves holds, per client, the number and digest of the last save
	// applied from it, so a copy of an older save the network delivers late
	// is skipped, and a repeat of the last one is told from a different save
	// (see store.InOrder).
	if _, err := sqldb.Exec(`CREATE TABLE IF NOT EXISTS client_saves (
		client TEXT PRIMARY KEY,
		last_save INTEGER NOT NULL,
		last_hash TEXT NOT NULL DEFAULT '')`); err != nil {
		return fmt.Errorf("apply server schema: %w", err)
	}
	has, err := hasColumn(sqldb, "client_saves", "last_hash")
	if err != nil {
		return err
	}
	if !has {
		if _, err := sqldb.Exec(`ALTER TABLE client_saves ADD COLUMN last_hash TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add client_saves.last_hash: %w", err)
		}
	}
	// event names the event this server holds and counts the order numbers
	// it stamps on the changes it accepts (see store.Event): one row, made
	// on the first start. deleted_prefixes remembers the prefixes deleted
	// here, with the order number of the delete, so a copy older than the
	// delete cannot bring one back.
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS event (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			event TEXT NOT NULL,
			started TEXT NOT NULL,
			last_rev INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS deleted_prefixes (
			prefix TEXT PRIMARY KEY,
			rev INTEGER NOT NULL)`,
	} {
		if _, err := sqldb.Exec(stmt); err != nil {
			return fmt.Errorf("apply server schema: %w", err)
		}
	}
	id, err := newEventID()
	if err != nil {
		return err
	}
	if _, err := sqldb.Exec(`INSERT INTO event (id, event, started, last_rev)
		SELECT 1, ?, ?, (SELECT max(0, coalesce(max(rev), 0)) FROM (
			SELECT rev FROM prefixes UNION ALL SELECT rev FROM tickets
			UNION ALL SELECT rev FROM baskets UNION ALL SELECT win_rev FROM baskets))
		WHERE NOT EXISTS (SELECT 1 FROM event)`, id, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("start the event: %w", err)
	}
	return migrateKeyActivity(sqldb)
}

// newEventID makes the random name of an event: 16 bytes, in hex.
func newEventID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("name the event: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// keyActivity are the columns of auth_key_activity besides the key: last_seen
// is the time of the key's last authenticated request and last_update that
// of its last accepted write.
var keyActivity = []string{"last_seen", "last_update"}

// migrateKeyActivity creates auth_key_activity, where tam-server records
// when each access key was last seen and last saved anything.
func migrateKeyActivity(sqldb *sql.DB) error {
	if _, err := sqldb.Exec(`CREATE TABLE IF NOT EXISTS auth_key_activity (auth_key TEXT PRIMARY KEY)`); err != nil {
		return fmt.Errorf("apply server schema: %w", err)
	}
	for _, column := range keyActivity {
		has, err := hasColumn(sqldb, "auth_key_activity", column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := sqldb.Exec(`ALTER TABLE auth_key_activity ADD COLUMN ` + column + ` TEXT`); err != nil {
			return fmt.Errorf("add auth_key_activity.%s: %w", column, err)
		}
	}
	return nil
}

// quoteName quotes an SQL identifier, one read from the schema included.
func quoteName(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// tableColumns lists the columns of table in order; none when there is no
// such table.
func tableColumns(sqldb *sql.DB, table string) ([]string, error) {
	rows, err := sqldb.Query(`PRAGMA table_info(` + quoteName(table) + `)`)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", table, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var (
			cid       int
			name, typ string
			notNull   int
			dflt      sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("inspect %s: %w", table, err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// hasColumn reports whether table has a column named column.
func hasColumn(sqldb *sql.DB, table, column string) (bool, error) {
	columns, err := tableColumns(sqldb, table)
	return slices.Contains(columns, column), err
}
