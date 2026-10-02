package store

import (
	"errors"
	"testing"
)

// A client's copy pushed to the server adds what the server lacks and
// updates only rows changed later than the server's; an older copy of a
// row never undoes a newer change.
func TestPushMergesOnlyNewerRows(t *testing.T) {
	s := newServerTestStore(t)
	saveTickets(t, s, ticketSave("A", 1, annLee, nil))
	base := annLee
	fix := annLee
	fix.PhoneNumber = "555-0888"
	newer := saveTickets(t, s, ticketSave("A", 1, fix, &base))[0]
	ev, _ := s.Event()

	copyOf := BackupFile{Event: ev.Event, Prefixes: []Prefix{}, Baskets: []Basket{},
		Tickets: []Ticket{
			{Prefix: "A", TID: 1, FirstName: "Ann", LastName: "Lee", PhoneNumber: "555-0001", Pref: "CALL", Rev: newer.Rev - 1}, // older
			{Prefix: "A", TID: 2, FirstName: "Bob", Pref: "CALL", Rev: 1},                                                       // missing on the server
		}}
	res, err := s.MergeNewer(copyOf)
	if err != nil {
		t.Fatal(err)
	}
	if res != (MergeResult{Added: 1, Kept: 1}) {
		t.Fatalf("merge = %+v, want one added and one kept", res)
	}
	if got, _ := s.Ticket("A", 1); got.PhoneNumber != "555-0888" {
		t.Fatalf("an older copy changed the phone back to %q", got.PhoneNumber)
	}
	if got, _ := s.Ticket("A", 2); got == nil || got.FirstName != "Bob" {
		t.Fatalf("the missing ticket was not added: %+v", got)
	}

	copyOf.Tickets = []Ticket{{Prefix: "A", TID: 1, FirstName: "Ann", LastName: "Leigh", PhoneNumber: "555-0888", Pref: "CALL", Rev: newer.Rev + 5}}
	if res, err := s.MergeNewer(copyOf); err != nil || res.Updated != 1 {
		t.Fatalf("a newer copy = %+v, %v", res, err)
	}
	if got, _ := s.Ticket("A", 1); got.LastName != "Leigh" {
		t.Fatalf("a newer copy was not taken: %+v", got)
	}
	// Changes made after the merge are newer than every copy merged.
	after := saveTickets(t, s, ticketSave("A", 3, annLee, nil))[0]
	if after.Rev <= newer.Rev+5 {
		t.Fatalf("a change after the merge got order number %d, not above the copy's %d", after.Rev, newer.Rev+5)
	}
}

// A basket's description and its winning ticket are merged apart.
func TestPushMergesBasketPartsApart(t *testing.T) {
	s := newServerTestStore(t)
	if _, err := s.SaveBaskets([]BasketSave{{Basket: Basket{Prefix: "A", BID: 1, Description: "Wine"}}}); err != nil {
		t.Fatal(err)
	}
	drawn, err := s.SaveWinning([]DrawingSave{{Basket: Basket{Prefix: "A", BID: 1, WinningTicket: 12}, Base: &DrawingValues{}}})
	if err != nil {
		t.Fatal(err)
	}
	ev, _ := s.Event()
	// A copy with a newer description but a winner from before the drawing.
	cp := NewBackupFile()
	cp.Event = ev.Event
	cp.Baskets = []Basket{{Prefix: "A", BID: 1, Description: "Red wine", WinningTicket: 0, Rev: drawn[0].WinRev + 1, WinRev: 0}}
	if _, err := s.MergeNewer(cp); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Basket("A", 1)
	if got.Description != "Red wine" || got.WinningTicket != 12 {
		t.Fatalf("after the merge the basket is %+v, want the newer description and winner 12 kept", got)
	}
}

// A copy older than a prefix's deletion does not bring it back; saving the
// prefix again does.
func TestPushDoesNotBringBackADeletedPrefix(t *testing.T) {
	s := newServerTestStore(t)
	stored, err := s.SavePrefixes([]PrefixSave{{Prefix: Prefix{Prefix: "OLD", Color: "red", Weight: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if gone, err := s.DeletePrefix("OLD"); err != nil || gone == nil {
		t.Fatalf("delete = %+v, %v", gone, err)
	}
	ev, _ := s.Event()
	cp := NewBackupFile()
	cp.Event = ev.Event
	cp.Prefixes = []Prefix{stored[0]}
	if res, err := s.MergeNewer(cp); err != nil || res.Kept != 1 {
		t.Fatalf("merging the deleted prefix = %+v, %v; want it kept deleted", res, err)
	}
	if p, _ := s.PrefixByName("OLD"); p != nil {
		t.Fatalf("an older copy brought back the deleted prefix: %+v", p)
	}
	if _, err := s.SavePrefixes([]PrefixSave{{Prefix: Prefix{Prefix: "OLD", Color: "blue", Weight: 1}}}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.PrefixByName("OLD"); p == nil || p.Color != "blue" {
		t.Fatalf("saving the prefix again = %+v", p)
	}
}

// A copy of another event is refused by a server with data of its own; an
// empty server takes the copy's event: it is being rebuilt from a client.
func TestPushChecksTheEvent(t *testing.T) {
	s := newServerTestStore(t)
	cp := NewBackupFile()
	cp.Event = "0123456789abcdef0123456789abcdef"
	cp.Tickets = []Ticket{{Prefix: "A", TID: 1, FirstName: "Ann", Rev: 40}}
	res, err := s.MergeNewer(cp)
	if err != nil || res.Added != 1 {
		t.Fatalf("rebuilding an empty server = %+v, %v", res, err)
	}
	ev, _ := s.Event()
	if ev.Event != cp.Event || ev.LastRev < 40 {
		t.Fatalf("the rebuilt server holds %+v, want the copy's event and numbers above 40", ev)
	}

	other := NewBackupFile()
	other.Event = "fedcba9876543210fedcba9876543210"
	other.Tickets = []Ticket{{Prefix: "A", TID: 9, FirstName: "Eve"}}
	if _, err := s.MergeNewer(other); !errors.Is(err, ErrOtherEvent) {
		t.Fatalf("a copy of another event = %v, want ErrOtherEvent", err)
	}
	if got, _ := s.Ticket("A", 9); got != nil {
		t.Fatalf("a refused copy wrote %+v", got)
	}
}

// A restore keeps the file's order numbers and event, so a client's copy
// with changes made after the backup is still newer than the restored rows.
func TestRestoreKeepsOrderNumbersAndEvent(t *testing.T) {
	s := newServerTestStore(t)
	bf := NewBackupFile()
	bf.Event = "0123456789abcdef0123456789abcdef"
	bf.Tickets = []Ticket{{Prefix: "A", TID: 1, FirstName: "Ann", Rev: 50}}
	must(t, s.Import(bf))
	ev, _ := s.Event()
	got, _ := s.Ticket("A", 1)
	if ev.Event != bf.Event || ev.LastRev < 50 || got.Rev != 50 {
		t.Fatalf("after the restore: event %+v, ticket %+v", ev, got)
	}
	// A backup from before order numbers existed restores too.
	old := NewBackupFile()
	old.Tickets = []Ticket{{Prefix: "B", TID: 1, FirstName: "Bob"}}
	must(t, s.Import(old))
	if ev2, _ := s.Event(); ev2.Event != bf.Event {
		t.Fatalf("a file without an event changed the event to %q", ev2.Event)
	}
	if got, _ := s.Ticket("B", 1); got == nil || got.FirstName != "Bob" {
		t.Fatalf("restoring an older backup = %+v", got)
	}
}
