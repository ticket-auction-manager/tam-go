package db

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

// columns lists the columns of a table in order.
func columns(t *testing.T, sqldb *sql.DB, table string) []string {
	t.Helper()
	rows, err := sqldb.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	return out
}

func TestMigrateServerAddsKeyActivity(t *testing.T) {
	sqldb, err := Open(filepath.Join(t.TempDir(), "remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	if err := Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	if cols := columns(t, sqldb, "auth_key_activity"); cols != nil {
		t.Fatalf("Migrate alone must not add auth_key_activity; the client shares that schema: %v", cols)
	}

	// Twice: the migration runs on every start.
	for i := 0; i < 2; i++ {
		if err := MigrateServer(sqldb); err != nil {
			t.Fatalf("MigrateServer run %d: %v", i+1, err)
		}
	}
	if cols := columns(t, sqldb, "auth_key_activity"); !reflect.DeepEqual(cols, []string{"auth_key", "last_seen", "last_update"}) {
		t.Fatalf("auth_key_activity after MigrateServer = %v", cols)
	}
	if cols := columns(t, sqldb, "auth_keys"); !reflect.DeepEqual(cols, []string{"auth_key", "description"}) {
		t.Fatalf("auth_keys after MigrateServer = %v, want its two columns", cols)
	}
}

func TestMigrateServerOverDatabaseFromTheOriginalServer(t *testing.T) {
	sqldb := openWithSchema(t, originalServerSchema)
	if _, err := sqldb.Exec(`INSERT INTO auth_keys VALUES ('K', 'client')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	if err := MigrateServer(sqldb); err != nil {
		t.Fatalf("MigrateServer over the original server's schema: %v", err)
	}
	var desc string
	if err := sqldb.QueryRow(`SELECT description FROM auth_keys WHERE auth_key = 'K'`).Scan(&desc); err != nil || desc != "client" {
		t.Fatalf("existing key after migration = %q, %v", desc, err)
	}
	var activity int
	if err := sqldb.QueryRow(`SELECT COUNT(*) FROM auth_key_activity`).Scan(&activity); err != nil || activity != 0 {
		t.Fatalf("a key never seen by tam-server has %d activity rows (%v), want none", activity, err)
	}
}

func TestHasColumnUnknownTable(t *testing.T) {
	sqldb, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	if has, err := hasColumn(sqldb, "nothing_here", "x"); err != nil || has {
		t.Fatalf("hasColumn on a missing table = %v, %v; want false, nil", has, err)
	}
}
