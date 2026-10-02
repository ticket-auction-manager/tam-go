package db

import (
	"database/sql"
	"fmt"
)

// ClientTables are the tables only tam-client has: the outbox of saves that
// have not reached the server yet, the ones the server rejected, the
// client's save numbers and the event its copy belongs to.
var ClientTables = []string{
	`CREATE TABLE IF NOT EXISTS outbox (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TEXT NOT NULL,
		method TEXT NOT NULL,
		path TEXT NOT NULL,
		body BLOB,
		attempts INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '')`,
	`CREATE TABLE IF NOT EXISTS outbox_failed (
		id INTEGER PRIMARY KEY,
		created_at TEXT NOT NULL,
		method TEXT NOT NULL,
		path TEXT NOT NULL,
		body BLOB,
		attempts INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		failed_at TEXT NOT NULL)`,
	// save_order is the client's name for the server and the number of its
	// last save (see store.NextSave): one row.
	`CREATE TABLE IF NOT EXISTS save_order (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		client TEXT NOT NULL,
		host TEXT NOT NULL,
		last_save INTEGER NOT NULL)`,
	// mirror names the event the client's copy belongs to (see
	// store.MirrorEvent): one row, once the client has met a server that
	// names its event.
	`CREATE TABLE IF NOT EXISTS mirror (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		event TEXT NOT NULL)`,
}

// clientColumns were added to the client tables after they first shipped:
// a queued save keeps the client name and number it was first sent with.
var clientColumns = []struct{ table, column, decl string }{
	{"outbox", "client", "TEXT NOT NULL DEFAULT ''"},
	{"outbox", "save_number", "INTEGER NOT NULL DEFAULT 0"},
	{"outbox_failed", "client", "TEXT NOT NULL DEFAULT ''"},
	{"outbox_failed", "save_number", "INTEGER NOT NULL DEFAULT 0"},
}

// MigrateClient creates the client-only tables. It runs after Migrate and
// is safe to run on every start.
func MigrateClient(sqldb *sql.DB) error {
	for _, stmt := range ClientTables {
		if _, err := sqldb.Exec(stmt); err != nil {
			return fmt.Errorf("apply client schema: %w", err)
		}
	}
	for _, c := range clientColumns {
		has, err := hasColumn(sqldb, c.table, c.column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := sqldb.Exec(`ALTER TABLE ` + c.table + ` ADD COLUMN ` + c.column + ` ` + c.decl); err != nil {
			return fmt.Errorf("add %s.%s: %w", c.table, c.column, err)
		}
	}
	return nil
}
