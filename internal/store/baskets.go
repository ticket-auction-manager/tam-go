package store

import "database/sql"

const basketCols = `prefix, b_id, description, donors, winning_ticket, rev, win_rev`

// upsertBasketSQL writes a whole basket with its order numbers.
const upsertBasketSQL = `INSERT INTO baskets (prefix, b_id, description, donors, winning_ticket, rev, win_rev) VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (prefix, b_id) DO UPDATE SET description = EXCLUDED.description, donors = EXCLUDED.donors,
	winning_ticket = EXCLUDED.winning_ticket, rev = EXCLUDED.rev, win_rev = EXCLUDED.win_rev`

// basketArgs are the arguments of upsertBasketSQL.
func basketArgs(b Basket) []any {
	return []any{b.Prefix, b.BID, b.Description, b.Donors, b.WinningTicket, b.Rev, b.WinRev}
}

// upsertWinningSQL writes a winning ticket with its order number, creating
// the basket when it does not exist yet.
const upsertWinningSQL = `INSERT INTO baskets (prefix, b_id, winning_ticket, win_rev) VALUES (?, ?, ?, ?)
	ON CONFLICT (prefix, b_id) DO UPDATE SET winning_ticket = EXCLUDED.winning_ticket, win_rev = EXCLUDED.win_rev`

func (s *Store) queryBaskets(query string, args ...any) ([]Basket, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Basket{}
	for rows.Next() {
		var b Basket
		var id, winning sql.NullInt64
		var desc, donors sql.NullString
		if err := rows.Scan(&b.Prefix, &id, &desc, &donors, &winning, &b.Rev, &b.WinRev); err != nil {
			return nil, err
		}
		b.BID, b.WinningTicket = nint(id), nint(winning)
		b.Description, b.Donors = nstr(desc), nstr(donors)
		out = append(out, b)
	}
	return out, rows.Err()
}

// AllBaskets returns every basket ordered by prefix, then id.
func (s *Store) AllBaskets() ([]Basket, error) {
	return s.queryBaskets(`SELECT ` + basketCols + ` FROM baskets ORDER BY prefix, b_id`)
}

// BasketsByPrefix returns the baskets of one prefix ordered by id.
func (s *Store) BasketsByPrefix(prefix string) ([]Basket, error) {
	return s.queryBaskets(`SELECT `+basketCols+` FROM baskets WHERE prefix = ? ORDER BY b_id`, prefix)
}

// Basket returns one basket, or nil when it does not exist.
func (s *Store) Basket(prefix string, id int) (*Basket, error) {
	bs, err := s.queryBaskets(`SELECT `+basketCols+` FROM baskets WHERE prefix = ? AND b_id = ?`, prefix, id)
	if err != nil || len(bs) == 0 {
		return nil, err
	}
	return &bs[0], nil
}

// BasketRange returns the existing baskets with ids between from and to inclusive.
func (s *Store) BasketRange(prefix string, from, to int) ([]Basket, error) {
	return s.queryBaskets(`SELECT `+basketCols+` FROM baskets WHERE prefix = ? AND b_id BETWEEN ? AND ? ORDER BY b_id`, prefix, from, to)
}

// UpsertBaskets writes whole baskets, with their order numbers, in one
// transaction: what the server answered, into a client's copy.
func (s *Store) UpsertBaskets(bs []Basket) error {
	return s.tx(func(tx *sql.Tx) error {
		return execEach(tx, upsertBasketSQL, len(bs), func(i int) []any { return basketArgs(bs[i]) })
	})
}

// UpsertBasketDescriptions writes the description and donors of baskets,
// with their order number, and whole baskets that do not exist yet: what
// the server answered for the Baskets form, into a client's copy, leaving a
// winning ticket the copy holds alone.
func (s *Store) UpsertBasketDescriptions(bs []Basket) error {
	return s.tx(func(tx *sql.Tx) error {
		return execEach(tx, `INSERT INTO baskets (prefix, b_id, description, donors, winning_ticket, rev, win_rev) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (prefix, b_id) DO UPDATE SET description = EXCLUDED.description, donors = EXCLUDED.donors, rev = EXCLUDED.rev`,
			len(bs), func(i int) []any { return basketArgs(bs[i]) })
	})
}

// UpsertWinning writes the winning ticket of each basket, with its order
// number, creating the basket when it does not exist yet: what the server
// answered for the drawing, into a client's copy.
func (s *Store) UpsertWinning(bs []Basket) error {
	return s.tx(func(tx *sql.Tx) error {
		return execEach(tx, upsertWinningSQL, len(bs), func(i int) []any {
			b := bs[i]
			return []any{b.Prefix, b.BID, b.WinningTicket, b.WinRev}
		})
	})
}

// --- drawing view ---

const drawingCols = `prefix, b_id, description, winning_ticket, last_name, first_name, phone_number, win_rev`

func (s *Store) queryDrawing(query string, args ...any) ([]DrawingLine, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DrawingLine{}
	for rows.Next() {
		var d DrawingLine
		var id, winning sql.NullInt64
		var desc, last, first, phone sql.NullString
		if err := rows.Scan(&d.Prefix, &id, &desc, &winning, &last, &first, &phone, &d.WinRev); err != nil {
			return nil, err
		}
		d.BID, d.WinningTicket = nint(id), nint(winning)
		d.Description, d.LastName, d.FirstName, d.PhoneNumber = nstr(desc), nstr(last), nstr(first), nstr(phone)
		out = append(out, d)
	}
	return out, rows.Err()
}

// AllDrawing returns every basket joined with its winner.
func (s *Store) AllDrawing() ([]DrawingLine, error) {
	return s.queryDrawing(`SELECT ` + drawingCols + ` FROM drawing ORDER BY prefix, b_id`)
}

// DrawingByPrefix returns the drawing lines of one prefix.
func (s *Store) DrawingByPrefix(prefix string) ([]DrawingLine, error) {
	return s.queryDrawing(`SELECT `+drawingCols+` FROM drawing WHERE prefix = ? ORDER BY b_id`, prefix)
}

// DrawingLine returns one drawing line, or nil when the basket does not exist.
func (s *Store) DrawingLine(prefix string, id int) (*DrawingLine, error) {
	ds, err := s.queryDrawing(`SELECT `+drawingCols+` FROM drawing WHERE prefix = ? AND b_id = ?`, prefix, id)
	if err != nil || len(ds) == 0 {
		return nil, err
	}
	return &ds[0], nil
}

// DrawingRange returns the existing drawing lines with ids between from and to inclusive.
func (s *Store) DrawingRange(prefix string, from, to int) ([]DrawingLine, error) {
	return s.queryDrawing(`SELECT `+drawingCols+` FROM drawing WHERE prefix = ? AND b_id BETWEEN ? AND ? ORDER BY b_id`, prefix, from, to)
}
