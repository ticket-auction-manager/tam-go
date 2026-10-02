package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Saves from the pages.
//
// A page sends each row it saves with the values the volunteer started
// from ("base"). A field the volunteer changed is written only while it
// still holds that starting value: when another computer changed it in the
// meantime, the newer value stays and the field is reported as not taken
// (see Conflict), so an older record never overwrites a newer one. The
// fields the volunteer left alone are not written at all. A row the store
// does not have yet is written whole. A save without base values, as
// tam-client before this version sends, replaces the fields as they come.
//
// Each form saves its own fields: the Tickets form and the Search page a
// ticket's names, phone number and contact preference, the Baskets form a
// basket's description and donors, the Drawing form its winning ticket,
// and Settings a prefix's colour and weight.

// TicketValues are the fields of a ticket a volunteer edits.
type TicketValues struct {
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	PhoneNumber string `json:"phone_number"`
	Pref        string `json:"pref"`
}

// TicketSave is a ticket as a page saves it, with the values the volunteer
// started from (nil when none were sent).
type TicketSave struct {
	Ticket
	Base *TicketValues `json:"base,omitempty"`
}

// UnmarshalJSON reads the ticket and its base values.
func (t *TicketSave) UnmarshalJSON(b []byte) error {
	if err := t.Ticket.UnmarshalJSON(b); err != nil {
		return err
	}
	var extra struct {
		Base *TicketValues `json:"base"`
	}
	if err := json.Unmarshal(b, &extra); err != nil {
		return err
	}
	t.Base = extra.Base
	return nil
}

func (t TicketSave) base() (Ticket, bool) {
	if t.Base == nil {
		return Ticket{}, false
	}
	return Ticket{Prefix: t.Prefix, TID: t.TID, FirstName: t.Base.FirstName, LastName: t.Base.LastName, PhoneNumber: t.Base.PhoneNumber, Pref: t.Base.Pref}, true
}

// BasketValues are the fields of a basket the Baskets form edits.
type BasketValues struct {
	Description string `json:"description"`
	Donors      string `json:"donors"`
}

// BasketSave is a basket as the Baskets form saves it, with the values the
// volunteer started from (nil when none were sent).
type BasketSave struct {
	Basket
	Base *BasketValues `json:"base,omitempty"`
}

// UnmarshalJSON reads the basket and its base values.
func (bs *BasketSave) UnmarshalJSON(b []byte) error {
	if err := bs.Basket.UnmarshalJSON(b); err != nil {
		return err
	}
	var extra struct {
		Base *BasketValues `json:"base"`
	}
	if err := json.Unmarshal(b, &extra); err != nil {
		return err
	}
	bs.Base = extra.Base
	return nil
}

func (bs BasketSave) base() (Basket, bool) {
	if bs.Base == nil {
		return Basket{}, false
	}
	return Basket{Prefix: bs.Prefix, BID: bs.BID, Description: bs.Base.Description, Donors: bs.Base.Donors}, true
}

// DrawingValues is the field of a basket the Drawing form edits.
type DrawingValues struct {
	WinningTicket Int `json:"winning_ticket"`
}

// DrawingSave is a basket's winning ticket as the Drawing form saves it,
// with the value the volunteer started from (nil when none was sent).
type DrawingSave struct {
	Basket
	Base *DrawingValues `json:"base,omitempty"`
}

// UnmarshalJSON reads the basket and its base value.
func (ds *DrawingSave) UnmarshalJSON(b []byte) error {
	if err := ds.Basket.UnmarshalJSON(b); err != nil {
		return err
	}
	var extra struct {
		Base *DrawingValues `json:"base"`
	}
	if err := json.Unmarshal(b, &extra); err != nil {
		return err
	}
	ds.Base = extra.Base
	return nil
}

func (ds DrawingSave) base() (Basket, bool) {
	if ds.Base == nil {
		return Basket{}, false
	}
	return Basket{Prefix: ds.Prefix, BID: ds.BID, WinningTicket: int(ds.Base.WinningTicket)}, true
}

// PrefixValues are the fields of a prefix Settings edits.
type PrefixValues struct {
	Color  string `json:"color"`
	Weight Int    `json:"weight"`
}

// PrefixSave is a prefix as Settings saves it, with the values the
// volunteer started from (nil when none were sent).
type PrefixSave struct {
	Prefix
	Base *PrefixValues `json:"base,omitempty"`
}

// UnmarshalJSON reads the prefix and its base values.
func (ps *PrefixSave) UnmarshalJSON(b []byte) error {
	if err := ps.Prefix.UnmarshalJSON(b); err != nil {
		return err
	}
	var extra struct {
		Base *PrefixValues `json:"base"`
	}
	if err := json.Unmarshal(b, &extra); err != nil {
		return err
	}
	ps.Base = extra.Base
	return nil
}

func (ps PrefixSave) base() (Prefix, bool) {
	if ps.Base == nil {
		return Prefix{}, false
	}
	return Prefix{Prefix: ps.Prefix.Prefix, Color: ps.Base.Color, Weight: int(ps.Base.Weight)}, true
}

// --- validation ---

// ValidateTicketSaves validates the tickets as ValidateTickets does and
// trims the base contact preference as the ticket's is trimmed.
func ValidateTicketSaves(saves []TicketSave) error {
	ts := make([]Ticket, len(saves))
	for i := range saves {
		ts[i] = saves[i].Ticket
	}
	if err := ValidateTickets(ts); err != nil {
		return err
	}
	for i := range saves {
		saves[i].Ticket = ts[i]
		if saves[i].Base != nil {
			saves[i].Base.Pref = strings.TrimSpace(saves[i].Base.Pref)
		}
	}
	return nil
}

// ValidateBasketSaves validates the baskets as ValidateBaskets does.
func ValidateBasketSaves(saves []BasketSave) error {
	bs := make([]Basket, len(saves))
	for i := range saves {
		bs[i] = saves[i].Basket
	}
	if err := ValidateBaskets(bs); err != nil {
		return err
	}
	for i := range saves {
		saves[i].Basket = bs[i]
	}
	return nil
}

// ValidateDrawingSaves validates the baskets as ValidateBaskets does, and a
// base winning ticket as a winning ticket.
func ValidateDrawingSaves(saves []DrawingSave) error {
	bs := make([]Basket, len(saves))
	for i := range saves {
		bs[i] = saves[i].Basket
	}
	if err := ValidateBaskets(bs); err != nil {
		return err
	}
	for i := range saves {
		saves[i].Basket = bs[i]
		if saves[i].Base != nil && saves[i].Base.WinningTicket < 0 {
			return fmt.Errorf("basket %d (%s/%d): winning ticket must be zero or more", i+1, bs[i].Prefix, bs[i].BID)
		}
	}
	return nil
}

// ValidatePrefixSaves validates the prefixes as ValidatePrefixes does.
func ValidatePrefixSaves(saves []PrefixSave) error {
	ps := make([]Prefix, len(saves))
	for i := range saves {
		ps[i] = saves[i].Prefix
	}
	if err := ValidatePrefixes(ps); err != nil {
		return err
	}
	for i := range saves {
		saves[i].Prefix = ps[i]
	}
	return nil
}

// --- fields ---

// field is one value a form edits: its name in messages, its value as
// text (for comparing and for messages), and how to copy it between rows.
type field[R any] struct {
	name string
	text func(R) string
	copy func(dst *R, src R)
}

var ticketFields = []field[Ticket]{
	{"first name", func(t Ticket) string { return t.FirstName }, func(d *Ticket, s Ticket) { d.FirstName = s.FirstName }},
	{"last name", func(t Ticket) string { return t.LastName }, func(d *Ticket, s Ticket) { d.LastName = s.LastName }},
	{"phone number", func(t Ticket) string { return t.PhoneNumber }, func(d *Ticket, s Ticket) { d.PhoneNumber = s.PhoneNumber }},
	{"contact preference", func(t Ticket) string { return t.Pref }, func(d *Ticket, s Ticket) { d.Pref = s.Pref }},
}

var basketFields = []field[Basket]{
	{"description", func(b Basket) string { return b.Description }, func(d *Basket, s Basket) { d.Description = s.Description }},
	{"donors", func(b Basket) string { return b.Donors }, func(d *Basket, s Basket) { d.Donors = s.Donors }},
}

var drawingFields = []field[Basket]{
	{"winning ticket", func(b Basket) string { return strconv.Itoa(b.WinningTicket) }, func(d *Basket, s Basket) { d.WinningTicket = s.WinningTicket }},
}

var prefixFields = []field[Prefix]{
	{"color", func(p Prefix) string { return p.Color }, func(d *Prefix, s Prefix) { d.Color = s.Color }},
	{"weight", func(p Prefix) string { return strconv.Itoa(p.Weight) }, func(d *Prefix, s Prefix) { d.Weight = s.Weight }},
}

// merge applies a save to a stored row: each field the volunteer changed
// (mine differs from base) is taken while the row still holds base. It
// reports whether anything changed.
func merge[R any](fields []field[R], cur, mine, base R) (R, bool) {
	out, changed := cur, false
	for _, f := range fields {
		if f.text(mine) != f.text(base) && f.text(cur) == f.text(base) && f.text(cur) != f.text(mine) {
			f.copy(&out, mine)
			changed = true
		}
	}
	return out, changed
}

// A Conflict is a change a save did not make: the field no longer held the
// value the volunteer started from, because another computer changed it
// meanwhile. Now is the value it holds.
type Conflict struct {
	Kind   string `json:"kind"` // ticket, basket, drawing or prefix
	Prefix string `json:"prefix"`
	ID     int    `json:"id"`
	Field  string `json:"field"`
	Yours  string `json:"yours"`
	Now    string `json:"now"`
}

// String says what the conflict was, for the failed list and the log.
func (c Conflict) String() string {
	what := map[string]string{"ticket": "ticket", "basket": "basket", "drawing": "basket", "prefix": "prefix"}[c.Kind]
	row := fmt.Sprintf("%s %s %d", what, c.Prefix, c.ID)
	if c.Kind == "prefix" {
		row = "prefix " + c.Prefix
	}
	return fmt.Sprintf("%s %s: another computer changed it to %q after this one loaded it; %q was not saved", row, c.Field, c.Now, c.Yours)
}

// conflicts lists the changes of mine (against base) that stored does not
// hold.
func conflicts[R any](kind, prefix string, id int, fields []field[R], mine, base, stored R) []Conflict {
	var out []Conflict
	for _, f := range fields {
		if f.text(mine) != f.text(base) && f.text(stored) != f.text(mine) {
			out = append(out, Conflict{Kind: kind, Prefix: prefix, ID: id, Field: f.name, Yours: f.text(mine), Now: f.text(stored)})
		}
	}
	return out
}

// kept is mine without the changes stored does not hold: the save a client
// queues for the server after its own copy refused some of them, so the
// server is not asked for them either.
func kept[R any](fields []field[R], mine, base, stored R) R {
	out := mine
	for _, f := range fields {
		if f.text(mine) != f.text(base) && f.text(stored) != f.text(mine) {
			f.copy(&out, base)
		}
	}
	return out
}

// again is a save of the changes stored does not hold, over what it holds:
// what the failed list keeps for a volunteer to apply deliberately (Retry).
// ok is false when stored holds every change.
func again[R any](fields []field[R], mine, base, stored R) (save R, newBase R, ok bool) {
	out := stored
	for _, f := range fields {
		if f.text(mine) != f.text(base) && f.text(stored) != f.text(mine) {
			f.copy(&out, mine)
			ok = true
		}
	}
	return out, stored, ok
}

// --- writing saves ---

// SaveTickets writes ticket saves in one transaction (see the saves above)
// and returns each ticket as stored afterwards, in the order of the saves.
// On a server a changed ticket gets the next order number (see Event); in
// a client's copy it keeps the number it had, which the server's answer
// replaces.
func (s *Store) SaveTickets(saves []TicketSave) ([]Ticket, error) {
	out := make([]Ticket, len(saves))
	err := s.tx(func(tx *sql.Tx) error {
		stamp, err := stamper(tx)
		if err != nil {
			return err
		}
		for i, sv := range saves {
			cur, err := ticketIn(tx, sv.Prefix, sv.TID)
			if err != nil {
				return err
			}
			next, changed := sv.Ticket, true
			if cur != nil {
				base, ok := sv.base()
				if !ok {
					base = *cur
				}
				next, changed = merge(ticketFields, *cur, sv.Ticket, base)
			}
			if !changed {
				out[i] = *cur
				continue
			}
			old := int64(0)
			if cur != nil {
				old = cur.Rev
			}
			if next.Rev, err = stamp(old); err != nil {
				return err
			}
			if _, err := tx.Exec(upsertTicketSQL, ticketArgs(next)...); err != nil {
				return err
			}
			out[i] = next
		}
		return nil
	})
	return out, err
}

// SaveBaskets writes the Baskets form's saves in one transaction: a
// basket's description and donors (see the saves above). A basket that
// does not exist yet is written whole, its winning ticket included. It
// returns each basket as stored afterwards, in the order of the saves.
func (s *Store) SaveBaskets(saves []BasketSave) ([]Basket, error) {
	out := make([]Basket, len(saves))
	err := s.tx(func(tx *sql.Tx) error {
		stamp, err := stamper(tx)
		if err != nil {
			return err
		}
		for i, sv := range saves {
			cur, err := basketIn(tx, sv.Prefix, sv.BID)
			if err != nil {
				return err
			}
			if cur == nil {
				next := sv.Basket
				if next.Rev, err = stamp(0); err != nil {
					return err
				}
				next.WinRev = next.Rev
				if _, err := tx.Exec(upsertBasketSQL, basketArgs(next)...); err != nil {
					return err
				}
				out[i] = next
				continue
			}
			base, ok := sv.base()
			if !ok {
				base = *cur
			}
			next, changed := merge(basketFields, *cur, sv.Basket, base)
			if changed {
				if next.Rev, err = stamp(cur.Rev); err != nil {
					return err
				}
				if _, err := tx.Exec(`UPDATE baskets SET description = ?, donors = ?, rev = ? WHERE prefix = ? AND b_id = ?`,
					next.Description, next.Donors, next.Rev, next.Prefix, next.BID); err != nil {
					return err
				}
			}
			out[i] = next
		}
		return nil
	})
	return out, err
}

// SaveWinning writes the Drawing form's saves in one transaction: a
// basket's winning ticket (see the saves above), creating the basket when
// it does not exist yet. It returns each basket as stored afterwards, in
// the order of the saves.
func (s *Store) SaveWinning(saves []DrawingSave) ([]Basket, error) {
	out := make([]Basket, len(saves))
	err := s.tx(func(tx *sql.Tx) error {
		stamp, err := stamper(tx)
		if err != nil {
			return err
		}
		for i, sv := range saves {
			cur, err := basketIn(tx, sv.Prefix, sv.BID)
			if err != nil {
				return err
			}
			next, changed := Basket{Prefix: sv.Prefix, BID: sv.BID, WinningTicket: sv.WinningTicket}, true
			if cur != nil {
				base, ok := sv.base()
				if !ok {
					base = *cur
				}
				next, changed = merge(drawingFields, *cur, sv.Basket, base)
			}
			if !changed {
				out[i] = *cur
				continue
			}
			old := int64(0)
			if cur != nil {
				old = cur.WinRev
			}
			if next.WinRev, err = stamp(old); err != nil {
				return err
			}
			if _, err := tx.Exec(upsertWinningSQL, next.Prefix, next.BID, next.WinningTicket, next.WinRev); err != nil {
				return err
			}
			out[i] = next
		}
		return nil
	})
	return out, err
}

// SavePrefixes writes Settings' prefix saves in one transaction: a prefix's
// colour and weight (see the saves above). On a server a saved prefix is no
// longer counted as deleted. It returns each prefix as stored afterwards,
// in the order of the saves.
func (s *Store) SavePrefixes(saves []PrefixSave) ([]Prefix, error) {
	out := make([]Prefix, len(saves))
	err := s.tx(func(tx *sql.Tx) error {
		stamp, err := stamper(tx)
		if err != nil {
			return err
		}
		ev, err := eventIn(tx)
		if err != nil {
			return err
		}
		for i, sv := range saves {
			cur, err := prefixIn(tx, sv.Prefix.Prefix)
			if err != nil {
				return err
			}
			next, changed := sv.Prefix, true
			if cur != nil {
				base, ok := sv.base()
				if !ok {
					base = *cur
				}
				next, changed = merge(prefixFields, *cur, sv.Prefix, base)
			}
			if !changed {
				out[i] = *cur
				continue
			}
			old := int64(0)
			if cur != nil {
				old = cur.Rev
			}
			if next.Rev, err = stamp(old); err != nil {
				return err
			}
			if _, err := tx.Exec(upsertPrefixSQL, prefixArgs(next)...); err != nil {
				return err
			}
			if ev != nil {
				if _, err := tx.Exec(`DELETE FROM deleted_prefixes WHERE prefix = ?`, next.Prefix); err != nil {
					return err
				}
			}
			out[i] = next
		}
		return nil
	})
	return out, err
}

// ticketIn reads one ticket inside tx; nil when there is none.
func ticketIn(tx *sql.Tx, prefix string, id int) (*Ticket, error) {
	var t Ticket
	var first, last, phone, pref sql.NullString
	err := tx.QueryRow(`SELECT `+ticketCols+` FROM tickets WHERE prefix = ? AND t_id = ?`, prefix, id).
		Scan(&t.Prefix, &t.TID, &first, &last, &phone, &pref, &t.Rev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.FirstName, t.LastName, t.PhoneNumber, t.Pref = nstr(first), nstr(last), nstr(phone), nstr(pref)
	return &t, nil
}

// basketIn reads one basket inside tx; nil when there is none.
func basketIn(tx *sql.Tx, prefix string, id int) (*Basket, error) {
	var b Basket
	var desc, donors sql.NullString
	var winning sql.NullInt64
	err := tx.QueryRow(`SELECT `+basketCols+` FROM baskets WHERE prefix = ? AND b_id = ?`, prefix, id).
		Scan(&b.Prefix, &b.BID, &desc, &donors, &winning, &b.Rev, &b.WinRev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b.Description, b.Donors, b.WinningTicket = nstr(desc), nstr(donors), nint(winning)
	return &b, nil
}

// prefixIn reads one prefix inside tx; nil when there is none.
func prefixIn(tx *sql.Tx, name string) (*Prefix, error) {
	var p Prefix
	var color sql.NullString
	var weight sql.NullInt64
	err := tx.QueryRow(`SELECT `+prefixCols+` FROM prefixes WHERE prefix = ?`, name).Scan(&p.Prefix, &color, &weight, &p.Rev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Color, p.Weight = nstr(color), nint(weight)
	return &p, nil
}

// --- answers ---

// ErrNotAnAnswer is an answer to a save that does not list the rows the
// save sent, in their order: not a TAM server's answer (a Wi-Fi login page
// answers anything with 200), so the save cannot count as delivered.
var ErrNotAnAnswer = errors.New("the answer does not list the saved rows")

// SaveResult is what the rows stored for a save mean for the client that
// sent them: the changes not made (Conflicts), those changes as a new save
// over what is stored now (Again, a request body; nil when there are none),
// the save without them (Kept, a request body), and the rows to write into
// the client's copy (Rows, written by Write).
type SaveResult struct {
	Conflicts []Conflict
	Again     []byte
	Kept      []byte
	write     func(*Store) error
	writeOwn  func(*Store) error
}

// Write puts the stored rows into a client's copy, as stored, order
// numbers included: for a save sent directly, which nothing saved since can
// have overtaken.
func (r SaveResult) Write(s *Store) error {
	if r.write == nil {
		return nil
	}
	return r.write(s)
}

// WriteUnlessNewer is Write for a save replayed from the queue: a row the
// copy holds differently from what this save sent was saved again here
// since, by a save still queued behind it, and stays as it is until that
// one is answered.
func (r SaveResult) WriteUnlessNewer(s *Store) error {
	if r.writeOwn == nil {
		return nil
	}
	return r.writeOwn(s)
}

// SavePath reports whether a save to path, by method, is one of the forms'
// saves that ResultOf understands.
func SavePath(method, path string) bool {
	if method != "POST" {
		return false
	}
	switch path {
	case "/api/tickets", "/api/search/tickets", "/api/baskets", "/api/drawing", "/api/prefixes":
		return true
	}
	return false
}

// ResultOf reads a save (request, the body sent to path) and the rows
// stored for it (answer, the server's answer, or what a client's own copy
// stored) and says what they mean (see SaveResult). The answer must list
// the saved rows in their order, or it is ErrNotAnAnswer.
func ResultOf(path string, request, answer []byte) (SaveResult, error) {
	switch path {
	case "/api/tickets", "/api/search/tickets":
		return result(request, answer, func(t TicketSave) Ticket { return t.Ticket }, TicketSave.base,
			func(t Ticket) (string, int) { return t.Prefix, t.TID }, "ticket", ticketFields,
			func(r Ticket, base Ticket) TicketSave {
				return TicketSave{Ticket: r, Base: &TicketValues{FirstName: base.FirstName, LastName: base.LastName, PhoneNumber: base.PhoneNumber, Pref: base.Pref}}
			},
			func(s *Store, t Ticket) (*Ticket, error) { return s.Ticket(t.Prefix, t.TID) }, (*Store).UpsertTickets)
	case "/api/baskets":
		return result(request, answer, func(b BasketSave) Basket { return b.Basket }, BasketSave.base,
			func(b Basket) (string, int) { return b.Prefix, b.BID }, "basket", basketFields,
			func(r Basket, base Basket) BasketSave {
				return BasketSave{Basket: r, Base: &BasketValues{Description: base.Description, Donors: base.Donors}}
			},
			func(s *Store, b Basket) (*Basket, error) { return s.Basket(b.Prefix, b.BID) }, (*Store).UpsertBasketDescriptions)
	case "/api/drawing":
		return result(request, answer, func(d DrawingSave) Basket { return d.Basket }, DrawingSave.base,
			func(b Basket) (string, int) { return b.Prefix, b.BID }, "drawing", drawingFields,
			func(r Basket, base Basket) DrawingSave {
				return DrawingSave{Basket: r, Base: &DrawingValues{WinningTicket: Int(base.WinningTicket)}}
			},
			func(s *Store, b Basket) (*Basket, error) { return s.Basket(b.Prefix, b.BID) }, (*Store).UpsertWinning)
	case "/api/prefixes":
		return result(request, answer, func(p PrefixSave) Prefix { return p.Prefix }, PrefixSave.base,
			func(p Prefix) (string, int) { return p.Prefix, 0 }, "prefix", prefixFields,
			func(r Prefix, base Prefix) PrefixSave {
				return PrefixSave{Prefix: r, Base: &PrefixValues{Color: base.Color, Weight: Int(base.Weight)}}
			},
			func(s *Store, p Prefix) (*Prefix, error) { return s.PrefixByName(p.Prefix) }, (*Store).UpsertPrefixes)
	}
	return SaveResult{}, fmt.Errorf("no save is sent to %s", path)
}

// result is ResultOf for one kind of row: S the save, R the row.
func result[S any, R any](request, answer []byte, row func(S) R, baseOf func(S) (R, bool), key func(R) (string, int),
	kind string, fields []field[R], resave func(row R, base R) S, load func(*Store, R) (*R, error), write func(*Store, []R) error) (SaveResult, error) {
	var saves []S
	if err := json.Unmarshal(request, &saves); err != nil {
		return SaveResult{}, fmt.Errorf("read the save: %w", err)
	}
	var stored []R
	if err := json.Unmarshal(answer, &stored); err != nil || len(stored) != len(saves) {
		return SaveResult{}, ErrNotAnAnswer
	}
	var res SaveResult
	var redo, keep []S
	for i, sv := range saves {
		mine := row(sv)
		prefix, id := key(mine)
		if p, n := key(stored[i]); p != prefix || n != id {
			return SaveResult{}, ErrNotAnAnswer
		}
		base, ok := baseOf(sv)
		if !ok {
			// Without base values every field was the volunteer's to set.
			keep = append(keep, sv)
			continue
		}
		res.Conflicts = append(res.Conflicts, conflicts(kind, prefix, id, fields, mine, base, stored[i])...)
		keep = append(keep, resave(kept(fields, mine, base, stored[i]), base))
		if save, newBase, ok := again(fields, mine, base, stored[i]); ok {
			redo = append(redo, resave(save, newBase))
		}
	}
	var err error
	if res.Kept, err = json.Marshal(keep); err != nil {
		return SaveResult{}, err
	}
	if len(redo) > 0 {
		if res.Again, err = json.Marshal(redo); err != nil {
			return SaveResult{}, err
		}
	}
	res.write = func(s *Store) error { return write(s, stored) }
	res.writeOwn = func(s *Store) error {
		var rows []R
		for i, sv := range saves {
			mine := row(sv)
			cur, err := load(s, mine)
			if err != nil {
				return err
			}
			if cur != nil && !sameFields(fields, *cur, mine) {
				continue
			}
			rows = append(rows, stored[i])
		}
		return write(s, rows)
	}
	return res, nil
}

// sameFields reports whether a and b hold the same value in every field.
func sameFields[R any](fields []field[R], a, b R) bool {
	for _, f := range fields {
		if f.text(a) != f.text(b) {
			return false
		}
	}
	return true
}
