// Package store is the data access layer shared by both daemons. Every
// query lives here; handlers never touch SQL.
package store

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"
)

// Store wraps one shared *sql.DB.
//
// Writes take turns through wmu, in the order they arrive. SQLite lets one
// writer in at a time and has the others retry in a busy wait that is not
// first come, first served; where every commit is flushed to a slow disk, a
// writer could time out behind a crowd of others and fail. Reads do not
// wait: with WAL journaling they never block on a writer.
type Store struct {
	db  *sql.DB
	wmu sync.Mutex
	in  *sql.Tx // set on the Store SaveQueued hands out: its writes go into this transaction
}

// New returns a Store over an opened, migrated database.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func nstr(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}

func nint(v sql.NullInt64) int {
	if v.Valid {
		return int(v.Int64)
	}
	return 0
}

// exec runs one statement that writes, in its turn.
func (s *Store) exec(query string, args ...any) (sql.Result, error) {
	if s.in != nil {
		return s.in.Exec(query, args...)
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.db.Exec(query, args...)
}

// execReturning runs one statement that writes and returns a row (DELETE
// ... RETURNING), in its turn, and scans the row into dest.
func (s *Store) execReturning(query string, args []any, dest ...any) error {
	if s.in != nil {
		return s.in.QueryRow(query, args...).Scan(dest...)
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.db.QueryRow(query, args...).Scan(dest...)
}

// tx runs fn inside a transaction, in its turn among the writes, and
// commits it when fn returns nil.
func (s *Store) tx(fn func(*sql.Tx) error) error {
	if s.in != nil {
		return fn(s.in)
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// --- prefixes ---

const prefixCols = `prefix, color, weight, rev`

func scanPrefixes(rows *sql.Rows) ([]Prefix, error) {
	defer rows.Close()
	out := []Prefix{}
	for rows.Next() {
		var p Prefix
		var color sql.NullString
		var weight sql.NullInt64
		if err := rows.Scan(&p.Prefix, &color, &weight, &p.Rev); err != nil {
			return nil, err
		}
		p.Color, p.Weight = nstr(color), nint(weight)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListPrefixes returns every prefix ordered by weight, then name.
func (s *Store) ListPrefixes() ([]Prefix, error) {
	rows, err := s.db.Query(`SELECT ` + prefixCols + ` FROM prefixes ORDER BY weight, prefix`)
	if err != nil {
		return nil, err
	}
	return scanPrefixes(rows)
}

// PrefixByName returns one prefix, or nil when it does not exist.
func (s *Store) PrefixByName(name string) (*Prefix, error) {
	rows, err := s.db.Query(`SELECT `+prefixCols+` FROM prefixes WHERE prefix = ?`, name)
	if err != nil {
		return nil, err
	}
	ps, err := scanPrefixes(rows)
	if err != nil || len(ps) == 0 {
		return nil, err
	}
	return &ps[0], nil
}

// upsertPrefixSQL writes a whole prefix with its order number.
const upsertPrefixSQL = `INSERT INTO prefixes (prefix, color, weight, rev) VALUES (?, ?, ?, ?)
	ON CONFLICT (prefix) DO UPDATE SET color = EXCLUDED.color, weight = EXCLUDED.weight, rev = EXCLUDED.rev`

// prefixArgs are the arguments of upsertPrefixSQL.
func prefixArgs(p Prefix) []any { return []any{p.Prefix, p.Color, p.Weight, p.Rev} }

// UpsertPrefixes writes whole prefixes, with their order numbers, in one
// transaction: what the server answered, into a client's copy.
func (s *Store) UpsertPrefixes(ps []Prefix) error {
	return s.tx(func(tx *sql.Tx) error {
		return execEach(tx, upsertPrefixSQL, len(ps), func(i int) []any { return prefixArgs(ps[i]) })
	})
}

// DeletePrefix removes a prefix and returns the deleted row, or nil when
// there was none. On a server the delete gets an order number of its own,
// kept in deleted_prefixes, so an older copy of the prefix cannot bring it
// back (see MergeNewer). Its tickets and baskets stay.
func (s *Store) DeletePrefix(name string) (*Prefix, error) {
	var gone *Prefix
	err := s.tx(func(tx *sql.Tx) error {
		var p Prefix
		var color sql.NullString
		var weight sql.NullInt64
		err := tx.QueryRow(`DELETE FROM prefixes WHERE prefix = ? RETURNING prefix, color, weight, rev`, name).Scan(&p.Prefix, &color, &weight, &p.Rev)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		p.Color, p.Weight = nstr(color), nint(weight)
		gone = &p
		ev, err := eventIn(tx)
		if err != nil || ev == nil {
			return err
		}
		rev, err := nextRev(tx)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO deleted_prefixes (prefix, rev) VALUES (?, ?)
			ON CONFLICT (prefix) DO UPDATE SET rev = excluded.rev`, name, rev)
		return err
	})
	return gone, err
}

// execEach prepares query once inside tx and executes it n times with the
// arguments produced by args(i).
func execEach(tx *sql.Tx, query string, n int, args func(i int) []any) error {
	if n == 0 {
		return nil
	}
	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i := 0; i < n; i++ {
		if _, err := stmt.Exec(args(i)...); err != nil {
			return err
		}
	}
	return nil
}

// --- auth keys ---

const keyAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func generateKey() (string, error) {
	buf := make([]byte, 32)
	max := big.NewInt(int64(len(keyAlphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = keyAlphabet[n.Int64()]
	}
	return string(buf), nil
}

// hasKeyActivity reports whether the database has the auth_key_activity
// table that db.MigrateServer adds. The client's database never gets it.
func (s *Store) hasKeyActivity() (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'auth_key_activity'`).Scan(&n)
	return n > 0, err
}

// ListKeys returns every access key ordered by description, then key.
// LastSeen and LastUpdate are filled in on a database with
// auth_key_activity.
func (s *Store) ListKeys() ([]AuthKey, error) {
	withActivity, err := s.hasKeyActivity()
	if err != nil {
		return nil, err
	}
	query := `SELECT auth_key, description, NULL, NULL FROM auth_keys ORDER BY description, auth_key`
	if withActivity {
		query = `SELECT k.auth_key, k.description, a.last_seen, a.last_update FROM auth_keys k
			LEFT JOIN auth_key_activity a ON a.auth_key = k.auth_key
			ORDER BY k.description, k.auth_key`
	}
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuthKey{}
	for rows.Next() {
		var k AuthKey
		var desc, seen, updated sql.NullString
		if err := rows.Scan(&k.AuthKey, &desc, &seen, &updated); err != nil {
			return nil, err
		}
		k.Description, k.LastSeen, k.LastUpdate = nstr(desc), nstr(seen), nstr(updated)
		out = append(out, k)
	}
	return out, rows.Err()
}

// TouchKey records now as the key's last_seen time. It needs the table
// db.MigrateServer adds; a missing key is not an error and gets no row.
func (s *Store) TouchKey(key string) error {
	_, err := s.exec(`INSERT INTO auth_key_activity (auth_key, last_seen)
		SELECT auth_key, ? FROM auth_keys WHERE auth_key = ?
		ON CONFLICT (auth_key) DO UPDATE SET last_seen = excluded.last_seen`, time.Now().UTC().Format(time.RFC3339), key)
	return err
}

// MarkKeyUpdated records now as the key's last_update time, the time of
// its last accepted write. It needs the table db.MigrateServer adds; a
// missing key is not an error and gets no row.
func (s *Store) MarkKeyUpdated(key string) error {
	_, err := s.exec(`INSERT INTO auth_key_activity (auth_key, last_update)
		SELECT auth_key, ? FROM auth_keys WHERE auth_key = ?
		ON CONFLICT (auth_key) DO UPDATE SET last_update = excluded.last_update`, time.Now().UTC().Format(time.RFC3339), key)
	return err
}

// CreateKey stores a new random 32-character key with a description.
func (s *Store) CreateKey(description string) (AuthKey, error) {
	for attempt := 0; attempt < 5; attempt++ {
		key, err := generateKey()
		if err != nil {
			return AuthKey{}, err
		}
		exists, err := s.KeyExists(key)
		if err != nil {
			return AuthKey{}, err
		}
		if exists {
			continue
		}
		if _, err := s.exec(`INSERT INTO auth_keys (auth_key, description) VALUES (?, ?)`, key, description); err != nil {
			return AuthKey{}, err
		}
		return AuthKey{AuthKey: key, Description: description}, nil
	}
	return AuthKey{}, errors.New("could not generate a unique key")
}

// DeleteKey removes a key, with what auth_key_activity holds of it, and
// returns the deleted row, or nil when there was none.
func (s *Store) DeleteKey(key string) (*AuthKey, error) {
	withActivity, err := s.hasKeyActivity()
	if err != nil {
		return nil, err
	}
	var k AuthKey
	var desc sql.NullString
	err = s.tx(func(tx *sql.Tx) error {
		if err := tx.QueryRow(`DELETE FROM auth_keys WHERE auth_key = ? RETURNING auth_key, description`, key).Scan(&k.AuthKey, &desc); err != nil {
			return err
		}
		if !withActivity {
			return nil
		}
		_, err := tx.Exec(`DELETE FROM auth_key_activity WHERE auth_key = ?`, key)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	k.Description = nstr(desc)
	return &k, nil
}

// KeyExists reports whether key is a valid access key.
func (s *Store) KeyExists(key string) (bool, error) {
	if key == "" {
		return false, nil
	}
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM auth_keys WHERE auth_key = ?`, key).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// --- status ---

// Counts returns the number of prefixes, tickets and baskets.
func (s *Store) Counts() (prefixes, tickets, baskets int, err error) {
	err = s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM prefixes), (SELECT COUNT(*) FROM tickets), (SELECT COUNT(*) FROM baskets)`).Scan(&prefixes, &tickets, &baskets)
	return prefixes, tickets, baskets, err
}

// --- backup and restore ---

// Export returns every prefix, basket and ticket with their order numbers,
// and the event they belong to: on a server the one it holds (see Event),
// on a client the one of its copy (see MirrorEvent).
func (s *Store) Export() (BackupFile, error) {
	bf := NewBackupFile()
	var err error
	if bf.Event, err = s.eventName(); err != nil {
		return bf, err
	}
	if bf.Prefixes, err = s.ListPrefixes(); err != nil {
		return bf, err
	}
	if bf.Baskets, err = s.AllBaskets(); err != nil {
		return bf, err
	}
	if bf.Tickets, err = s.AllTickets(); err != nil {
		return bf, err
	}
	return bf, nil
}

// Import writes every row of a backup file in one transaction, with the
// order numbers the file gives them (0 where it has none). Existing rows are
// overwritten, which is what a restore is for. On a server the file's event
// becomes the server's, when the file names one, and the order numbers go
// on from the highest in the file, so a later change is always newer; a
// prefix the file holds is no longer counted as deleted.
func (s *Store) Import(bf BackupFile) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := execEach(tx, upsertPrefixSQL, len(bf.Prefixes), func(i int) []any { return prefixArgs(bf.Prefixes[i]) }); err != nil {
			return fmt.Errorf("prefixes: %w", err)
		}
		if err := execEach(tx, upsertBasketSQL, len(bf.Baskets), func(i int) []any { return basketArgs(bf.Baskets[i]) }); err != nil {
			return fmt.Errorf("baskets: %w", err)
		}
		if err := execEach(tx, upsertTicketSQL, len(bf.Tickets), func(i int) []any { return ticketArgs(bf.Tickets[i]) }); err != nil {
			return fmt.Errorf("tickets: %w", err)
		}
		ev, err := eventIn(tx)
		if err != nil || ev == nil {
			return err
		}
		if err := execEach(tx, `DELETE FROM deleted_prefixes WHERE prefix = ?`, len(bf.Prefixes), func(i int) []any { return []any{bf.Prefixes[i].Prefix} }); err != nil {
			return err
		}
		if bf.Event != "" && bf.Event != ev.Event {
			if _, err := tx.Exec(`UPDATE event SET event = ? WHERE id = 1`, bf.Event); err != nil {
				return err
			}
		}
		return raiseRev(tx, maxRev(bf))
	})
}
