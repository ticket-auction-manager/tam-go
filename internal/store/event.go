package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// Event is the event a server holds: a random name made on the server's
// first start, when it started, and the last order number it stamped. Every
// change the server accepts to a prefix, a ticket, or a basket's description
// or winning ticket gets the next order number, and every copy of a row
// carries the number it had (Prefix.Rev, Ticket.Rev, Basket.Rev and WinRev).
// So of two copies of a row from the same event the one with the higher
// number is the newer, wherever each copy has been meanwhile, and the
// event's name tells copies of one event from those of another.
type Event struct {
	Event   string `json:"event"`
	Started string `json:"started"`
	LastRev int64  `json:"last_rev"`
}

// ErrOtherEvent is a copy of another event's data offered to a server that
// already holds data of its own event.
var ErrOtherEvent = errors.New("this copy belongs to another event than the one this server holds")

// queryer is a database or a transaction.
type queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

// hasTable reports whether the database has the named table.
func hasTable(q queryer, name string) (bool, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return n > 0, err
}

// eventIn returns the server's event, or nil in a client's database, which
// has no event table.
func eventIn(q queryer) (*Event, error) {
	if ok, err := hasTable(q, "event"); err != nil || !ok {
		return nil, err
	}
	var ev Event
	err := q.QueryRow(`SELECT event, started, last_rev FROM event WHERE id = 1`).Scan(&ev.Event, &ev.Started, &ev.LastRev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("the server's event is missing; db.MigrateServer makes it")
	}
	return &ev, err
}

// Event returns the event this server holds, or nil in a client's database.
func (s *Store) Event() (*Event, error) {
	return eventIn(s.db)
}

// nextRev takes the server's next order number inside tx.
func nextRev(tx *sql.Tx) (int64, error) {
	var rev int64
	err := tx.QueryRow(`UPDATE event SET last_rev = last_rev + 1 WHERE id = 1 RETURNING last_rev`).Scan(&rev)
	return rev, err
}

// raiseRev makes the server's order numbers go on above rev, so a change
// made after copies were written is newer than all of them.
func raiseRev(tx *sql.Tx, rev int64) error {
	_, err := tx.Exec(`UPDATE event SET last_rev = ? WHERE id = 1 AND last_rev < ?`, rev, rev)
	return err
}

// maxRev is the highest order number in a backup file.
func maxRev(bf BackupFile) int64 {
	var top int64
	for _, p := range bf.Prefixes {
		top = max(top, p.Rev)
	}
	for _, t := range bf.Tickets {
		top = max(top, t.Rev)
	}
	for _, b := range bf.Baskets {
		top = max(top, b.Rev, b.WinRev)
	}
	return top
}

// stamper returns what a changed row is stamped with inside tx: on a
// server the next order number; in a client's copy the number the row
// already had (0 for a row it did not have), as only the server numbers
// changes and the client takes the number from its answer.
func stamper(tx *sql.Tx) (func(old int64) (int64, error), error) {
	ev, err := eventIn(tx)
	if err != nil {
		return nil, err
	}
	if ev == nil {
		return func(old int64) (int64, error) { return old, nil }, nil
	}
	return func(int64) (int64, error) { return nextRev(tx) }, nil
}

// MirrorEvent returns the event a client's copy belongs to, "" before the
// client met a server that names its event.
func (s *Store) MirrorEvent() (string, error) {
	var ev string
	err := s.db.QueryRow(`SELECT event FROM mirror WHERE id = 1`).Scan(&ev)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return ev, err
}

// SetMirrorEvent records the event a client's copy belongs to.
func (s *Store) SetMirrorEvent(ev string) error {
	_, err := s.exec(`INSERT INTO mirror (id, event) VALUES (1, ?) ON CONFLICT (id) DO UPDATE SET event = excluded.event`, ev)
	return err
}

// eventName is the event of what the database holds: the server's event,
// or the event of a client's copy.
func (s *Store) eventName() (string, error) {
	ev, err := s.Event()
	if err != nil {
		return "", err
	}
	if ev != nil {
		return ev.Event, nil
	}
	if ok, err := hasTable(s.db, "mirror"); err != nil || !ok {
		return "", err
	}
	return s.MirrorEvent()
}

// ClearCopy empties a client's copy of an event, for one of another event:
// every prefix, ticket and basket, and the name of the event it belonged to.
// The caller keeps a file of it first.
func (s *Store) ClearCopy() error {
	return s.tx(func(tx *sql.Tx) error {
		for _, stmt := range []string{`DELETE FROM prefixes`, `DELETE FROM tickets`, `DELETE FROM baskets`, `DELETE FROM mirror`} {
			if _, err := tx.Exec(stmt); err != nil {
				return err
			}
		}
		return nil
	})
}

// MergeResult counts what MergeNewer did with the rows of a copy.
type MergeResult struct {
	Added   int `json:"added"`   // rows the server did not have
	Updated int `json:"updated"` // rows newer in the copy than on the server
	Kept    int `json:"kept"`    // rows the server holds as new as the copy's, or newer
}

// MergeNewer writes a copy of an event's rows, a client's copy sent by
// Push, where it is newer than what the server holds: a row the server
// does not have, or a row whose order number in the copy is above the
// server's. A basket's description and donors and its winning ticket are
// compared apart. Everything else stays as it is, so an older copy never
// undoes a newer change. A prefix deleted on the server after the copy's
// change stays deleted. Rows without order numbers (from a copy made before
// they existed, or by a client working alone) are only added where the
// server has none.
//
// The copy's event must be the server's. A server that holds nothing yet
// takes the copy's event instead: that is a server rebuilt from a client's
// copy. A copy that names no event is taken as one of this event.
func (s *Store) MergeNewer(bf BackupFile) (MergeResult, error) {
	var res MergeResult
	err := s.tx(func(tx *sql.Tx) error {
		ev, err := eventIn(tx)
		if err != nil {
			return err
		}
		if ev == nil {
			return errors.New("only a server merges copies")
		}
		if bf.Event != "" && bf.Event != ev.Event {
			var rows int
			if err := tx.QueryRow(`SELECT (SELECT COUNT(*) FROM prefixes) + (SELECT COUNT(*) FROM tickets) + (SELECT COUNT(*) FROM baskets)`).Scan(&rows); err != nil {
				return err
			}
			if rows > 0 {
				return ErrOtherEvent
			}
			if _, err := tx.Exec(`UPDATE event SET event = ? WHERE id = 1`, bf.Event); err != nil {
				return err
			}
		}
		for _, p := range bf.Prefixes {
			var rev int64
			err := tx.QueryRow(`SELECT rev FROM prefixes WHERE prefix = ?`, p.Prefix).Scan(&rev)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				var deleted int64
				err := tx.QueryRow(`SELECT rev FROM deleted_prefixes WHERE prefix = ?`, p.Prefix).Scan(&deleted)
				if err == nil && p.Rev <= deleted {
					res.Kept++
					continue
				}
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				if _, err := tx.Exec(upsertPrefixSQL, prefixArgs(p)...); err != nil {
					return fmt.Errorf("prefix %s: %w", p.Prefix, err)
				}
				if _, err := tx.Exec(`DELETE FROM deleted_prefixes WHERE prefix = ?`, p.Prefix); err != nil {
					return err
				}
				res.Added++
			case err != nil:
				return err
			case p.Rev > rev:
				if _, err := tx.Exec(upsertPrefixSQL, prefixArgs(p)...); err != nil {
					return fmt.Errorf("prefix %s: %w", p.Prefix, err)
				}
				res.Updated++
			default:
				res.Kept++
			}
		}
		for _, t := range bf.Tickets {
			var rev int64
			err := tx.QueryRow(`SELECT rev FROM tickets WHERE prefix = ? AND t_id = ?`, t.Prefix, t.TID).Scan(&rev)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				if _, err := tx.Exec(upsertTicketSQL, ticketArgs(t)...); err != nil {
					return fmt.Errorf("ticket %s/%d: %w", t.Prefix, t.TID, err)
				}
				res.Added++
			case err != nil:
				return err
			case t.Rev > rev:
				if _, err := tx.Exec(upsertTicketSQL, ticketArgs(t)...); err != nil {
					return fmt.Errorf("ticket %s/%d: %w", t.Prefix, t.TID, err)
				}
				res.Updated++
			default:
				res.Kept++
			}
		}
		for _, b := range bf.Baskets {
			var rev, winRev int64
			err := tx.QueryRow(`SELECT rev, win_rev FROM baskets WHERE prefix = ? AND b_id = ?`, b.Prefix, b.BID).Scan(&rev, &winRev)
			if errors.Is(err, sql.ErrNoRows) {
				if _, err := tx.Exec(upsertBasketSQL, basketArgs(b)...); err != nil {
					return fmt.Errorf("basket %s/%d: %w", b.Prefix, b.BID, err)
				}
				res.Added++
				continue
			}
			if err != nil {
				return err
			}
			updated := false
			if b.Rev > rev {
				if _, err := tx.Exec(`UPDATE baskets SET description = ?, donors = ?, rev = ? WHERE prefix = ? AND b_id = ?`,
					b.Description, b.Donors, b.Rev, b.Prefix, b.BID); err != nil {
					return fmt.Errorf("basket %s/%d: %w", b.Prefix, b.BID, err)
				}
				updated = true
			}
			if b.WinRev > winRev {
				if _, err := tx.Exec(`UPDATE baskets SET winning_ticket = ?, win_rev = ? WHERE prefix = ? AND b_id = ?`,
					b.WinningTicket, b.WinRev, b.Prefix, b.BID); err != nil {
					return fmt.Errorf("basket %s/%d: %w", b.Prefix, b.BID, err)
				}
				updated = true
			}
			if updated {
				res.Updated++
			} else {
				res.Kept++
			}
		}
		return raiseRev(tx, maxRev(bf))
	})
	return res, err
}
