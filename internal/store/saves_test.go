package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ticket-auction-manager/tam-go/internal/db"
)

// newServerTestStore is a store with the server's schema: it has an event
// and numbers the changes it accepts.
func newServerTestStore(t *testing.T) *Store {
	t.Helper()
	sqldb, err := db.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	if err := db.Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateServer(sqldb); err != nil {
		t.Fatal(err)
	}
	return New(sqldb)
}

// annLee is a ticket as two volunteers loaded it.
var annLee = TicketValues{FirstName: "Ann", LastName: "Lee", PhoneNumber: "555-0001", Pref: "CALL"}

func ticketSave(prefix string, id int, v TicketValues, base *TicketValues) TicketSave {
	return TicketSave{Ticket: Ticket{Prefix: prefix, TID: id, FirstName: v.FirstName, LastName: v.LastName, PhoneNumber: v.PhoneNumber, Pref: v.Pref}, Base: base}
}

func saveTickets(t *testing.T, s *Store, saves ...TicketSave) []Ticket {
	t.Helper()
	stored, err := s.SaveTickets(saves)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

// A volunteer who corrects one field of a ticket changes that field only:
// another computer's newer correction of another field stays.
func TestASaveChangesOnlyTheFieldsTheVolunteerChanged(t *testing.T) {
	s := newServerTestStore(t)
	base := annLee
	saveTickets(t, s, ticketSave("A", 1, annLee, nil))

	// Another computer corrects the last name.
	other := annLee
	other.LastName = "Leigh"
	saveTickets(t, s, ticketSave("A", 1, other, &base))

	// This computer, which loaded the ticket before that, fixes the phone.
	mine := annLee
	mine.PhoneNumber = "555-0777"
	stored := saveTickets(t, s, ticketSave("A", 1, mine, &base))
	if got := stored[0]; got.LastName != "Leigh" || got.PhoneNumber != "555-0777" || got.FirstName != "Ann" {
		t.Fatalf("after both corrections the ticket reads %+v, want Ann Leigh 555-0777", got)
	}
}

// An older record never overwrites a newer one: a change based on a value
// another computer has changed since is not made, and the answer shows the
// newer value.
func TestAnOlderRecordDoesNotOverwriteANewerOne(t *testing.T) {
	s := newServerTestStore(t)
	base := annLee
	saveTickets(t, s, ticketSave("A", 1, annLee, nil))

	newer := annLee
	newer.PhoneNumber = "555-0888"
	saveTickets(t, s, ticketSave("A", 1, newer, &base))

	// A save from a computer that was offline since it loaded the ticket.
	late := annLee
	late.PhoneNumber = "555-0777"
	stored := saveTickets(t, s, ticketSave("A", 1, late, &base))
	if stored[0].PhoneNumber != "555-0888" {
		t.Fatalf("the late save changed the phone to %q over the newer 555-0888", stored[0].PhoneNumber)
	}
	got, _ := s.Ticket("A", 1)
	if got.PhoneNumber != "555-0888" {
		t.Fatalf("the stored ticket has %q", got.PhoneNumber)
	}

	sv := ticketSave("A", 1, late, &base)
	request, _ := json.Marshal([]TicketSave{sv})
	answer, _ := json.Marshal(stored)
	res, err := ResultOf("/api/tickets", request, answer)
	if err != nil {
		t.Fatal(err)
	}
	want := []Conflict{{Kind: "ticket", Prefix: "A", ID: 1, Field: "phone number", Yours: "555-0777", Now: "555-0888"}}
	if !reflect.DeepEqual(res.Conflicts, want) {
		t.Fatalf("conflicts = %+v, want %+v", res.Conflicts, want)
	}
	if !strings.Contains(want[0].String(), "another computer changed it") {
		t.Fatalf("conflict text = %q", want[0].String())
	}

	// Retry applies it deliberately, over what is stored now.
	var again []TicketSave
	if err := json.Unmarshal(res.Again, &again); err != nil || len(again) != 1 || again[0].PhoneNumber != "555-0777" || again[0].Base.PhoneNumber != "555-0888" {
		t.Fatalf("the save to retry is %s (%v)", res.Again, err)
	}
	if stored := saveTickets(t, s, again...); stored[0].PhoneNumber != "555-0777" {
		t.Fatalf("retrying the refused change left %q", stored[0].PhoneNumber)
	}
}

// The same change made on two computers is no conflict.
func TestTheSameChangeTwiceIsNoConflict(t *testing.T) {
	s := newServerTestStore(t)
	base := annLee
	saveTickets(t, s, ticketSave("A", 1, annLee, nil))
	fix := annLee
	fix.LastName = "Leigh"
	saveTickets(t, s, ticketSave("A", 1, fix, &base))
	stored := saveTickets(t, s, ticketSave("A", 1, fix, &base))
	request, _ := json.Marshal([]TicketSave{ticketSave("A", 1, fix, &base)})
	answer, _ := json.Marshal(stored)
	res, err := ResultOf("/api/tickets", request, answer)
	if err != nil || len(res.Conflicts) != 0 || res.Again != nil {
		t.Fatalf("the same change twice = %+v, %v; want no conflict", res, err)
	}
}

// Two computers enter the same new ticket: the first is kept, and the
// second sees what the first entered.
func TestTheSameNewTicketFromTwoComputers(t *testing.T) {
	s := newServerTestStore(t)
	blank := &TicketValues{Pref: "CALL"}
	saveTickets(t, s, ticketSave("A", 7, TicketValues{FirstName: "Ann", LastName: "Lee", Pref: "CALL"}, blank))
	stored := saveTickets(t, s, ticketSave("A", 7, TicketValues{FirstName: "Bob", LastName: "Ray", Pref: "CALL"}, blank))
	if stored[0].FirstName != "Ann" || stored[0].LastName != "Lee" {
		t.Fatalf("the second entry of ticket 7 overwrote the first: %+v", stored[0])
	}
}

// A save without base values, as an earlier tam-client sends it, replaces
// the fields as they come.
func TestASaveWithoutBaseValuesReplaces(t *testing.T) {
	s := newServerTestStore(t)
	saveTickets(t, s, ticketSave("A", 1, annLee, nil))
	other := TicketValues{FirstName: "Bob", LastName: "Ray", PhoneNumber: "1", Pref: "TEXT"}
	if stored := saveTickets(t, s, ticketSave("A", 1, other, nil)); stored[0].FirstName != "Bob" || stored[0].Pref != "TEXT" {
		t.Fatalf("a save without base values = %+v", stored[0])
	}
}

// Every change a server accepts gets a higher order number; a save that
// changes nothing keeps the old one. A client's copy keeps the numbers it
// had.
func TestTheServerNumbersEveryChange(t *testing.T) {
	s := newServerTestStore(t)
	first := saveTickets(t, s, ticketSave("A", 1, annLee, nil))[0].Rev
	base := annLee
	fix := annLee
	fix.LastName = "Leigh"
	second := saveTickets(t, s, ticketSave("A", 1, fix, &base))[0].Rev
	if first <= 0 || second <= first {
		t.Fatalf("order numbers %d then %d, want rising", first, second)
	}
	again := saveTickets(t, s, ticketSave("A", 1, fix, nil))[0].Rev
	if again != second {
		t.Fatalf("a save that changes nothing moved the order number from %d to %d", second, again)
	}
	ev, err := s.Event()
	if err != nil || ev == nil || ev.LastRev != second || len(ev.Event) != 32 {
		t.Fatalf("event = %+v (%v), want last order number %d", ev, err, second)
	}

	client := newTestStore(t)
	if ev, err := client.Event(); err != nil || ev != nil {
		t.Fatalf("a client's database has the event %+v (%v)", ev, err)
	}
	must(t, client.UpsertTickets([]Ticket{{Prefix: "A", TID: 1, FirstName: "Ann", LastName: "Lee", Pref: "CALL", Rev: 9}}))
	cbase := annLee
	cbase.PhoneNumber = ""
	mine := cbase
	mine.PhoneNumber = "2"
	if stored := saveTickets(t, client, ticketSave("A", 1, mine, &cbase)); stored[0].Rev != 9 || stored[0].PhoneNumber != "2" {
		t.Fatalf("a client's own save = %+v, want the phone changed and order number 9 kept", stored[0])
	}
}

// The Baskets form and the Drawing form change their own fields: a winner
// drawn meanwhile survives a description saved from an older page, and
// a winner from a page loaded before another winner was entered does not
// replace it.
func TestBasketFormsKeepToTheirFields(t *testing.T) {
	s := newServerTestStore(t)
	if _, err := s.SaveBaskets([]BasketSave{{Basket: Basket{Prefix: "A", BID: 1, Description: "Wine", Donors: "Smiths"}}}); err != nil {
		t.Fatal(err)
	}
	loadedMeta := &BasketValues{Description: "Wine", Donors: "Smiths"}
	loadedWinner := &DrawingValues{WinningTicket: 0}

	if _, err := s.SaveWinning([]DrawingSave{{Basket: Basket{Prefix: "A", BID: 1, WinningTicket: 12}, Base: loadedWinner}}); err != nil {
		t.Fatal(err)
	}
	stored, err := s.SaveBaskets([]BasketSave{{Basket: Basket{Prefix: "A", BID: 1, Description: "Red wine", Donors: "Smiths"}, Base: loadedMeta}})
	if err != nil {
		t.Fatal(err)
	}
	if stored[0].Description != "Red wine" || stored[0].WinningTicket != 12 {
		t.Fatalf("after the description and the winner = %+v", stored[0])
	}

	late, err := s.SaveWinning([]DrawingSave{{Basket: Basket{Prefix: "A", BID: 1, WinningTicket: 34}, Base: loadedWinner}})
	if err != nil {
		t.Fatal(err)
	}
	if late[0].WinningTicket != 12 {
		t.Fatalf("a winner from an older page replaced winner 12 with %d", late[0].WinningTicket)
	}

	// Clearing the winner deliberately, from what is shown now, works.
	cleared, err := s.SaveWinning([]DrawingSave{{Basket: Basket{Prefix: "A", BID: 1, WinningTicket: 0}, Base: &DrawingValues{WinningTicket: 12}}})
	if err != nil || cleared[0].WinningTicket != 0 {
		t.Fatalf("clearing winner 12 = %+v, %v", cleared, err)
	}
}

func TestPrefixSavesKeepNewerSettings(t *testing.T) {
	s := newServerTestStore(t)
	if _, err := s.SavePrefixes([]PrefixSave{{Prefix: Prefix{Prefix: "A", Color: "red", Weight: 1}}}); err != nil {
		t.Fatal(err)
	}
	loaded := &PrefixValues{Color: "red", Weight: 1}
	if _, err := s.SavePrefixes([]PrefixSave{{Prefix: Prefix{Prefix: "A", Color: "blue", Weight: 1}, Base: loaded}}); err != nil {
		t.Fatal(err)
	}
	stored, err := s.SavePrefixes([]PrefixSave{{Prefix: Prefix{Prefix: "A", Color: "green", Weight: 5}, Base: loaded}})
	if err != nil {
		t.Fatal(err)
	}
	if stored[0].Color != "blue" || stored[0].Weight != 5 {
		t.Fatalf("prefix after an older page's save = %+v, want blue kept and weight 5", stored[0])
	}
}

// ResultOf refuses an answer that does not list the saved rows in order:
// a Wi-Fi login page that answers 200 must not count as delivery.
func TestAnAnswerMustListTheSavedRows(t *testing.T) {
	request, _ := json.Marshal([]TicketSave{ticketSave("A", 1, annLee, nil), ticketSave("A", 2, annLee, nil)})
	for _, answer := range []string{
		`<html><body>Sign in to the venue Wi-Fi</body></html>`,
		`{"message":"ok"}`,
		`[]`,
		`[{"prefix":"A","t_id":1}]`,
		`[{"prefix":"A","t_id":2},{"prefix":"A","t_id":1}]`,
		`[{"prefix":"B","t_id":1},{"prefix":"A","t_id":2}]`,
	} {
		if _, err := ResultOf("/api/tickets", request, []byte(answer)); !errors.Is(err, ErrNotAnAnswer) {
			t.Errorf("answer %s = %v, want ErrNotAnAnswer", answer, err)
		}
	}
	echo := `[{"prefix":"A","t_id":1,"first_name":"Ann"},{"prefix":"A","t_id":2}]`
	if _, err := ResultOf("/api/tickets", request, []byte(echo)); err != nil {
		t.Errorf("an answer listing the rows = %v", err)
	}
}

// The save a client queues after its own copy refused a change leaves that
// change out, so the server is not asked for it either.
func TestTheQueuedSaveLeavesOutRefusedChanges(t *testing.T) {
	base := annLee
	mine := annLee
	mine.PhoneNumber, mine.LastName = "555-0777", "Leigh"
	// The client's copy holds a newer phone number than the volunteer saw.
	stored := Ticket{Prefix: "A", TID: 1, FirstName: "Ann", LastName: "Leigh", PhoneNumber: "555-0888", Pref: "CALL"}
	request, _ := json.Marshal([]TicketSave{ticketSave("A", 1, mine, &base)})
	answer, _ := json.Marshal([]Ticket{stored})
	res, err := ResultOf("/api/tickets", request, answer)
	if err != nil {
		t.Fatal(err)
	}
	var kept []TicketSave
	if err := json.Unmarshal(res.Kept, &kept); err != nil || len(kept) != 1 {
		t.Fatalf("kept = %s (%v)", res.Kept, err)
	}
	if kept[0].PhoneNumber != "555-0001" || kept[0].LastName != "Leigh" || kept[0].Base.PhoneNumber != "555-0001" {
		t.Fatalf("the queued save is %+v base %+v; want the last name change only", kept[0].Ticket, *kept[0].Base)
	}
}
