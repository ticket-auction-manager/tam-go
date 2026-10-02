package store

import (
	"database/sql"
	"errors"
	"time"
)

// Outbox is one save that has not reached the server yet: the request the
// client would have sent, kept until the server takes it.
type Outbox struct {
	ID        int64
	CreatedAt time.Time
	Method    string
	Path      string
	Body      []byte
	Attempts  int
	LastError string
	Order     Order // the name and number it was first sent with
}

const outboxCols = `id, created_at, method, path, body, attempts, last_error, client, save_number`

// SaveQueued writes a save to the client's own copy and appends its
// request to the outbox in one transaction, and returns the request's id.
// Both happen or neither: a client that stops in between (a flat battery)
// never shows a save it will not send. write gets a Store whose writes go
// into that transaction. order is the save's name and number, kept for the
// replay.
func (s *Store) SaveQueued(method, path string, body []byte, order Order, write func(*Store) error) (int64, error) {
	return s.SaveQueuedAs(method, path, order, func(st *Store) ([]byte, error) { return body, write(st) })
}

// SaveQueuedAs is SaveQueued for a save whose request depends on what the
// write did: write returns the body to queue.
func (s *Store) SaveQueuedAs(method, path string, order Order, write func(*Store) ([]byte, error)) (int64, error) {
	var id int64
	err := s.tx(func(tx *sql.Tx) error {
		body, err := write(&Store{db: s.db, in: tx})
		if err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO outbox (created_at, method, path, body, client, save_number) VALUES (?, ?, ?, ?, ?, ?)`,
			time.Now().UTC().Format(time.RFC3339Nano), method, path, body, order.Client, order.Save)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

// EnqueueOutbox appends a request to the outbox and returns its id.
func (s *Store) EnqueueOutbox(method, path string, body []byte) (int64, error) {
	res, err := s.exec(`INSERT INTO outbox (created_at, method, path, body) VALUES (?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano), method, path, body)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOutbox(row rowScanner) (*Outbox, error) {
	var o Outbox
	var created string
	if err := row.Scan(&o.ID, &created, &o.Method, &o.Path, &o.Body, &o.Attempts, &o.LastError, &o.Order.Client, &o.Order.Save); err != nil {
		return nil, err
	}
	o.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &o, nil
}

// NextOutbox returns the oldest queued request, or nil when the outbox is
// empty.
func (s *Store) NextOutbox() (*Outbox, error) {
	o, err := scanOutbox(s.db.QueryRow(`SELECT ` + outboxCols + ` FROM outbox ORDER BY id LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return o, err
}

// ListFailed returns the requests the server rejected, oldest first.
func (s *Store) ListFailed() ([]Outbox, error) {
	rows, err := s.db.Query(`SELECT ` + outboxCols + ` FROM outbox_failed ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Outbox{}
	for rows.Next() {
		o, err := scanOutbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// DeleteOutbox removes a request the server has taken.
func (s *Store) DeleteOutbox(id int64) error {
	_, err := s.exec(`DELETE FROM outbox WHERE id = ?`, id)
	return err
}

// NoteOutboxAttempt records a failed attempt that will be retried.
func (s *Store) NoteOutboxAttempt(id int64, errText string) error {
	_, err := s.exec(`UPDATE outbox SET attempts = attempts + 1, last_error = ? WHERE id = ?`, errText, id)
	return err
}

// FailOutbox moves a request the server rejected to the failed list.
func (s *Store) FailOutbox(id int64, errText string) error {
	return s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO outbox_failed (id, created_at, method, path, body, attempts, last_error, failed_at, client, save_number)
			SELECT id, created_at, method, path, body, attempts + 1, ?, ?, client, save_number FROM outbox WHERE id = ?`,
			errText, time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM outbox WHERE id = ?`, id)
		return err
	})
}

// FinishOutbox takes a request the server answered off the queue, in one
// transaction with writing the server's rows into this client's copy
// (write, may be nil) and, when the server did not make some of its
// changes, keeping those changes in the failed list as a save of their own
// (again, the body of that save, with errText as the reason) for the
// volunteer to retry deliberately or discard.
func (s *Store) FinishOutbox(id int64, write func(*Store) error, again []byte, errText string) error {
	return s.tx(func(tx *sql.Tx) error {
		if write != nil {
			if err := write(&Store{db: s.db, in: tx}); err != nil {
				return err
			}
		}
		if again != nil {
			if _, err := tx.Exec(`INSERT INTO outbox_failed (id, created_at, method, path, body, attempts, last_error, failed_at, client, save_number)
				SELECT id, created_at, method, path, ?, attempts + 1, ?, ?, '', 0 FROM outbox WHERE id = ?`,
				again, errText, time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`DELETE FROM outbox WHERE id = ?`, id)
		return err
	})
}

// FailAllOutbox moves every waiting request to the failed list, in order,
// with errText as the reason, and returns how many it moved.
func (s *Store) FailAllOutbox(errText string) (int, error) {
	var n int
	err := s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO outbox_failed (id, created_at, method, path, body, attempts, last_error, failed_at, client, save_number)
			SELECT id, created_at, method, path, body, attempts, ?, ?, client, save_number FROM outbox ORDER BY id`,
			errText, time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		moved, _ := res.RowsAffected()
		n = int(moved)
		_, err = tx.Exec(`DELETE FROM outbox`)
		return err
	})
	return n, err
}

// OutboxCounts returns how many requests are waiting and how many failed.
func (s *Store) OutboxCounts() (pending, failed int, err error) {
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM outbox`).Scan(&pending); err != nil {
		return 0, 0, err
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM outbox_failed`).Scan(&failed); err != nil {
		return 0, 0, err
	}
	return pending, failed, nil
}

// OutboxWaiting reports whether any request is waiting in the outbox.
func (s *Store) OutboxWaiting() (bool, error) {
	var waiting bool
	err := s.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM outbox)`).Scan(&waiting)
	return waiting, err
}

// RetryFailed moves every failed request to the end of the outbox, in its
// original order, and returns how many it moved. Each gets a new number
// (see NextSave; host is this machine's name) and a new place in the queue:
// it is sent again now, after the saves already queued, and the server
// takes a client's saves in the order of their numbers (see InOrder), so
// the queue must be in that order too. The caller holds the client's saves
// in line meanwhile (sync.Syncer.Sending).
func (s *Store) RetryFailed(host string) (int, error) {
	var n int
	err := s.tx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id FROM outbox_failed ORDER BY id`)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range ids {
			o, err := nextSave(tx, host)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE outbox_failed SET client = ?, save_number = ? WHERE id = ?`, o.Client, o.Save, id); err != nil {
				return err
			}
		}
		res, err := tx.Exec(`INSERT INTO outbox (created_at, method, path, body, attempts, last_error, client, save_number)
			SELECT created_at, method, path, body, attempts, last_error, client, save_number FROM outbox_failed ORDER BY id`)
		if err != nil {
			return err
		}
		moved, _ := res.RowsAffected()
		n = int(moved)
		_, err = tx.Exec(`DELETE FROM outbox_failed`)
		return err
	})
	return n, err
}

// DiscardFailed drops every failed request and returns how many there were.
func (s *Store) DiscardFailed() (int, error) {
	res, err := s.exec(`DELETE FROM outbox_failed`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
