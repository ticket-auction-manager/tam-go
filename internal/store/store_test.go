package store

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/db"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	sqldb, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	if err := db.Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	return New(sqldb)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestPrefixes(t *testing.T) {
	s := newTestStore(t)
	must(t, s.UpsertPrefixes([]Prefix{{"B", "blue", 2, 0}, {"A", "red", 1, 0}}))

	got, err := s.ListPrefixes()
	must(t, err)
	want := []Prefix{{"A", "red", 1, 0}, {"B", "blue", 2, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListPrefixes = %v, want %v", got, want)
	}

	must(t, s.UpsertPrefixes([]Prefix{{"A", "green", 1, 0}}))
	got, _ = s.ListPrefixes()
	if got[0].Color != "green" {
		t.Fatalf("upsert did not update colour: %v", got)
	}

	deleted, err := s.DeletePrefix("A")
	must(t, err)
	if deleted == nil || deleted.Color != "green" || deleted.Weight != 1 {
		t.Fatalf("DeletePrefix should return the deleted row, got %+v", deleted)
	}
	deleted, err = s.DeletePrefix("A")
	must(t, err)
	if deleted != nil {
		t.Fatalf("second DeletePrefix should return nil, got %+v", deleted)
	}

	empty := newTestStore(t)
	list, err := empty.ListPrefixes()
	must(t, err)
	if list == nil || len(list) != 0 {
		t.Fatalf("empty list should be a non-nil empty slice, got %#v", list)
	}
}

func TestTickets(t *testing.T) {
	s := newTestStore(t)
	must(t, s.UpsertTickets([]Ticket{
		{"A", 3, "Cara", "Lee", "555-0003", "TEXT", 0},
		{"A", 1, "Joan", "Smith", "555-0001", "CALL", 0},
		{"B", 1, "Bob", "Jones", "555-0100", "CALL", 0},
		{"A", 2, "Jo", "Kim", "555-0002", "CALL", 0},
	}))

	all, err := s.AllTickets()
	must(t, err)
	if len(all) != 4 || all[0].TID != 1 || all[0].Prefix != "A" || all[3].Prefix != "B" {
		t.Fatalf("AllTickets order wrong: %v", all)
	}

	byPrefix, _ := s.TicketsByPrefix("A")
	if len(byPrefix) != 3 || byPrefix[2].TID != 3 {
		t.Fatalf("TicketsByPrefix = %v", byPrefix)
	}

	one, err := s.Ticket("A", 2)
	must(t, err)
	if one == nil || one.FirstName != "Jo" {
		t.Fatalf("Ticket(A,2) = %v", one)
	}
	missing, err := s.Ticket("A", 9)
	must(t, err)
	if missing != nil {
		t.Fatalf("Ticket(A,9) should be nil, got %v", missing)
	}

	rng, _ := s.TicketRange("A", 2, 3)
	if len(rng) != 2 || rng[0].TID != 2 || rng[1].TID != 3 {
		t.Fatalf("TicketRange = %v", rng)
	}

	must(t, s.UpsertTickets([]Ticket{{"A", 1, "Joan", "Smith-Ng", "555-0001", "TEXT", 0}}))
	one, _ = s.Ticket("A", 1)
	if one.LastName != "Smith-Ng" || one.Pref != "TEXT" {
		t.Fatalf("upsert did not update: %v", one)
	}

	found, err := s.SearchTickets("jo", "", "")
	must(t, err)
	if len(found) != 2 {
		t.Fatalf("SearchTickets(jo) = %v, want Joan and Jo", found)
	}
	found, _ = s.SearchTickets("", "", "0100")
	if len(found) != 1 || found[0].Prefix != "B" {
		t.Fatalf("SearchTickets(phone) = %v", found)
	}
	found, _ = s.SearchTickets("", "", "")
	if len(found) != 4 {
		t.Fatalf("empty search should match everything, got %d", len(found))
	}

	// LIKE wildcards typed by a user are literal characters.
	must(t, s.UpsertTickets([]Ticket{{"C", 1, "50%", "a_b", "x", "CALL", 0}}))
	if found, _ = s.SearchTickets("%", "", ""); len(found) != 1 || found[0].FirstName != "50%" {
		t.Fatalf("search for a literal percent = %v", found)
	}
	if found, _ = s.SearchTickets("", "_", ""); len(found) != 1 || found[0].LastName != "a_b" {
		t.Fatalf("search for a literal underscore = %v", found)
	}
	if found, _ = s.SearchTickets("", "a\\b", ""); len(found) != 0 {
		t.Fatalf("search for a literal backslash = %v", found)
	}
}

// TestSearchComparesWholeFields: SQLite's LIKE stops reading a field or a
// pattern at a NUL character. A ticket whose name held one was missed by a
// search for the text after it, and a fragment holding one matched every
// ticket.
func TestSearchComparesWholeFields(t *testing.T) {
	s := newTestStore(t)
	must(t, s.UpsertTickets([]Ticket{{"A", 1, "Ann\x00e", "Lee", "5", "CALL", 0}, {"A", 2, "Bob", "Lee", "5", "CALL", 0}}))
	for _, fragment := range []string{"e", "\x00", "N\x00E"} {
		found, err := s.SearchTickets(fragment, "", "")
		if err != nil || len(found) != 1 || found[0].TID != 1 {
			t.Errorf("search for %q = %v, %v; want ticket A/1 alone", fragment, found, err)
		}
	}
}

// TestSearchWithALongFragment: SQLite refuses LIKE patterns over 50,000
// bytes, which turned a long search into an internal error. A long fragment
// is text like any other.
func TestSearchWithALongFragment(t *testing.T) {
	s := newTestStore(t)
	long := strings.Repeat("x", 60000)
	must(t, s.UpsertTickets([]Ticket{{"A", 1, long, "", "", "CALL", 0}, {"A", 2, "x", "", "", "CALL", 0}}))
	found, err := s.SearchTickets(long, "", "")
	if err != nil || len(found) != 1 || found[0].TID != 1 {
		t.Fatalf("search for 60,000 bytes = %d rows, %v; want ticket A/1 alone", len(found), err)
	}
}

func TestValidation(t *testing.T) {
	long := strings.Repeat("L", 101)
	for _, bad := range []Prefix{
		{"", "red", 1, 0}, {"   ", "red", 1, 0}, {"A/B", "red", 1, 0}, {`A\B`, "red", 1, 0}, {"A\tB", "red", 1, 0},
		{long, "red", 1, 0}, {"A", "chartreuse", 1, 0}, {"A", "red", -1, 0},
	} {
		if err := ValidatePrefixes([]Prefix{bad}); err == nil {
			t.Errorf("ValidatePrefixes(%+v) should fail", bad)
		}
	}
	ok := []Prefix{{" A ", "red", 0, 0}}
	if err := ValidatePrefixes(ok); err != nil || ok[0].Prefix != "A" {
		t.Fatalf("ValidatePrefixes trims: %v %+v", err, ok)
	}

	// Tickets only need a non-empty prefix, so an original database with an
	// unusual prefix stays writable.
	if err := ValidateTickets([]Ticket{{"A/B", 1, "", "", "", "CALL", 0}}); err != nil {
		t.Errorf("ticket under an existing slash prefix must be accepted: %v", err)
	}
	if err := ValidateTickets([]Ticket{{" ", 1, "", "", "", "CALL", 0}}); err == nil {
		t.Error("ticket with a blank prefix should fail")
	}
	if err := ValidateTickets([]Ticket{{"A", -1, "", "", "", "CALL", 0}}); err == nil || !strings.Contains(err.Error(), "ticket 1 (A/-1)") {
		t.Errorf("ticket with a negative id should fail naming the row, got %v", err)
	}
	anyPref := []Ticket{{"A", 1, "", "", "", " call ", 0}}
	if err := ValidateTickets(anyPref); err != nil || anyPref[0].Pref != "call" {
		t.Fatalf("pref is free text like the original: %v %+v", err, anyPref)
	}
	if err := ValidateBaskets([]Basket{{"A", 1, "", "", -1, 0, 0}}); err == nil {
		t.Error("basket with a negative winning ticket should fail")
	}

	bf := BackupFile{Prefixes: []Prefix{{"OLD", "gray", 1, 0}}, Tickets: []Ticket{{"OLD", 1, "", "", "", "call", 0}}}
	if err := ValidateBackup(&bf); err != nil {
		t.Fatalf("a backup from the original app must restore: %v", err)
	}
	if bf.Prefixes[0].Color != "white" || bf.Baskets == nil {
		t.Fatalf("ValidateBackup should normalise colours and nil lists: %+v", bf)
	}
	if err := ValidateBackup(&BackupFile{Prefixes: []Prefix{{"", "red", 1, 0}}}); err == nil {
		t.Error("a backup with an empty prefix name should fail")
	}
}

// TestDotPrefixNamesAreRefused: a path segment . or .. is resolved away by
// the browser and by Go's ServeMux before any handler sees it, so a prefix
// with that name could never be opened, whatever the escaping. Such a name
// is refused like one with a slash; dots within a name are fine.
func TestDotPrefixNamesAreRefused(t *testing.T) {
	for _, name := range []string{".", "..", " .. "} {
		if _, err := ValidatePrefixName(name); err == nil {
			t.Errorf("ValidatePrefixName(%q) should fail", name)
		}
	}
	for _, name := range []string{"...", ".A", "A.", "A.B"} {
		if got, err := ValidatePrefixName(name); err != nil || got != name {
			t.Errorf("ValidatePrefixName(%q) = %q, %v; want it accepted", name, got, err)
		}
	}
}

// TestPrefixNameLengthCountsCharacters: the limit is 100 characters, as the
// error and the README say, not 100 bytes, which refused names in scripts
// that take two to four bytes a character well before the limit.
func TestPrefixNameLengthCountsCharacters(t *testing.T) {
	for _, name := range []string{strings.Repeat("é", 100), strings.Repeat("€", 100), strings.Repeat("🎟", 100)} {
		if got, err := ValidatePrefixName(name); err != nil || got != name {
			t.Errorf("a name of 100 %q = %v; want it accepted", []rune(name)[0], err)
		}
		if _, err := ValidatePrefixName(name + "x"); err == nil {
			t.Errorf("a name of 101 characters (100 %q and x) should fail", []rune(name)[0])
		}
	}
}

func TestBasketsAndDrawing(t *testing.T) {
	s := newTestStore(t)
	must(t, s.UpsertTickets([]Ticket{{"A", 5, "Winnie", "Won", "555-0005", "CALL", 0}}))
	must(t, s.UpsertBaskets([]Basket{
		{"A", 1, "Wine", "The Smiths", 0, 0, 0},
		{"A", 2, "Spa day", "", 0, 0, 0},
	}))

	// The drawing form only sets winning tickets, and may name a basket that
	// does not exist yet.
	must(t, s.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 5}, {Prefix: "A", BID: 3, WinningTicket: 7}}))

	line, err := s.DrawingLine("A", 1)
	must(t, err)
	if line == nil || line.WinningTicket != 5 || line.LastName != "Won" || line.Description != "Wine" {
		t.Fatalf("DrawingLine(A,1) = %+v", line)
	}
	unclaimed, _ := s.DrawingLine("A", 2)
	if unclaimed == nil || unclaimed.LastName != "" || unclaimed.WinningTicket != 0 {
		t.Fatalf("DrawingLine(A,2) = %+v", unclaimed)
	}
	created, _ := s.Basket("A", 3)
	if created == nil || created.Description != "" || created.WinningTicket != 7 {
		t.Fatalf("basket created by the drawing form = %+v", created)
	}

	// Saving the baskets form again must not clobber a drawn winner.
	if _, err := s.SaveBaskets([]BasketSave{{Basket: Basket{Prefix: "A", BID: 1, Description: "Red wine", Donors: "The Smiths"}}}); err != nil {
		t.Fatal(err)
	}
	b, _ := s.Basket("A", 1)
	if b.Description != "Red wine" || b.WinningTicket != 5 {
		t.Fatalf("the Baskets form's save must keep the winning ticket: %+v", b)
	}

	rng, _ := s.DrawingRange("A", 1, 3)
	if len(rng) != 3 {
		t.Fatalf("DrawingRange = %v", rng)
	}
	all, _ := s.AllDrawing()
	byPrefix, _ := s.DrawingByPrefix("A")
	if len(all) != 3 || len(byPrefix) != 3 {
		t.Fatalf("AllDrawing=%d DrawingByPrefix=%d", len(all), len(byPrefix))
	}
	if missing, _ := s.Basket("Z", 1); missing != nil {
		t.Fatal("Basket(Z,1) should be nil")
	}
	brange, _ := s.BasketRange("A", 2, 3)
	if len(brange) != 2 {
		t.Fatalf("BasketRange = %v", brange)
	}
	if bp, _ := s.BasketsByPrefix("A"); len(bp) != 3 {
		t.Fatalf("BasketsByPrefix = %v", bp)
	}
	if ab, _ := s.AllBaskets(); len(ab) != 3 {
		t.Fatalf("AllBaskets = %v", ab)
	}
}

func TestReports(t *testing.T) {
	s := newTestStore(t)
	must(t, s.UpsertTickets([]Ticket{
		{"A", 1, "Zed", "Young", "555-0001", "CALL", 0},
		{"A", 2, "Amy", "Adams", "555-0002", "TEXT", 0},
		{"A", 3, "Amy", "Adams", "555-0002", "TEXT", 0},
		{"B", 1, "Bea", "Brown", "555-0003", "CALL", 0},
	}))
	must(t, s.UpsertBaskets([]Basket{{"A", 1, "Wine", "", 1, 0, 0}, {"A", 2, "Spa", "", 2, 0, 0}, {"A", 3, "Books", "", 0, 0, 0}}))

	byName, err := s.ReportByName("A")
	must(t, err)
	if len(byName) != 3 {
		t.Fatalf("ReportByName = %v", byName)
	}
	// Undrawn basket sorts first (NULL name), then Adams, then Young.
	if byName[0].BID != 3 || byName[1].LastName != "Adams" || byName[2].LastName != "Young" {
		t.Fatalf("ReportByName order = %v", byName)
	}

	byBasket, _ := s.ReportByBasket("A")
	if len(byBasket) != 3 || byBasket[0].BID != 1 || byBasket[0].LastName != "Young" || byBasket[2].LastName != "" {
		t.Fatalf("ReportByBasket = %v", byBasket)
	}

	counts, _ := s.ReportCounts()
	got := map[string]ReportCountLine{}
	for _, c := range counts {
		got[c.Prefix] = c
	}
	if got["A"].TotalBuys != 3 || got["A"].UniqueBuyers != 2 || got["B"].TotalBuys != 1 {
		t.Fatalf("ReportCounts = %v", counts)
	}
	if got["Total"].TotalBuys != 4 || got["Total"].UniqueBuyers != 3 || !got["Total"].IsTotal {
		t.Fatalf("ReportCounts total = %v", got["Total"])
	}
}

// A buyer is a name and phone number together, not their letters run
// into one text; a prefix named Total is a row of its own.
func TestCountsTellBuyersAndTheTotalApart(t *testing.T) {
	s := newTestStore(t)
	must(t, s.UpsertTickets([]Ticket{
		{Prefix: "Total", TID: 1, FirstName: "Jo", LastName: "Ann", PhoneNumber: "5", Pref: "CALL"},
		{Prefix: "Total", TID: 2, FirstName: "Joa", LastName: "nn", PhoneNumber: "5", Pref: "CALL"},
		{Prefix: "Total", TID: 3, FirstName: "Jo", LastName: "Ann", PhoneNumber: "5", Pref: "CALL"},
	}))
	counts, err := s.ReportCounts()
	must(t, err)
	want := []ReportCountLine{
		{Prefix: "Total", UniqueBuyers: 2, TotalBuys: 3},
		{Prefix: "Total", IsTotal: true, UniqueBuyers: 2, TotalBuys: 3},
	}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("counts = %+v, want %+v", counts, want)
	}
}

func TestAuthKeys(t *testing.T) {
	s := newTestStore(t)
	k, err := s.CreateKey("client 1")
	must(t, err)
	if !regexp.MustCompile(`^[A-Z0-9]{32}$`).MatchString(k.AuthKey) {
		t.Fatalf("key format: %q", k.AuthKey)
	}
	k2, _ := s.CreateKey("client 2")
	if k2.AuthKey == k.AuthKey {
		t.Fatal("keys must be unique")
	}
	ok, _ := s.KeyExists(k.AuthKey)
	if !ok {
		t.Fatal("KeyExists should be true")
	}
	if ok, _ := s.KeyExists(""); ok {
		t.Fatal("empty key must never exist")
	}
	list, _ := s.ListKeys()
	if len(list) != 2 || list[0].Description != "client 1" {
		t.Fatalf("ListKeys = %v", list)
	}
	gone, err := s.DeleteKey(k.AuthKey)
	must(t, err)
	if gone == nil || gone.Description != "client 1" {
		t.Fatalf("DeleteKey should return the deleted row, got %+v", gone)
	}
	if ok, _ := s.KeyExists(k.AuthKey); ok {
		t.Fatal("deleted key still exists")
	}
	if gone, _ = s.DeleteKey(k.AuthKey); gone != nil {
		t.Fatalf("deleting a missing key should return nil, got %+v", gone)
	}
}

func TestKeyLastSeen(t *testing.T) {
	// Without the server migration there is no auth_key_activity: ListKeys
	// and DeleteKey still work and LastSeen stays empty, which keeps the
	// JSON the original's.
	plain := newTestStore(t)
	k, err := plain.CreateKey("client")
	must(t, err)
	list, err := plain.ListKeys()
	must(t, err)
	if len(list) != 1 || list[0].LastSeen != "" {
		t.Fatalf("ListKeys without the table = %+v", list)
	}
	if data, _ := json.Marshal(list[0]); strings.Contains(string(data), "last_seen") {
		t.Fatalf("an unused key must not carry last_seen on the wire: %s", data)
	}
	if err := plain.TouchKey(k.AuthKey); err == nil {
		t.Fatal("TouchKey without the table should fail")
	}
	if gone, err := plain.DeleteKey(k.AuthKey); err != nil || gone == nil {
		t.Fatalf("DeleteKey without the table = %+v, %v", gone, err)
	}

	s := newTestStore(t)
	must(t, db.MigrateServer(s.db))
	k, err = s.CreateKey("client")
	must(t, err)
	if list, _ = s.ListKeys(); list[0].LastSeen != "" {
		t.Fatalf("a new key has no last_seen, got %q", list[0].LastSeen)
	}
	before := time.Now().Add(-2 * time.Second)
	must(t, s.TouchKey(k.AuthKey))
	list, _ = s.ListKeys()
	seen, err := time.Parse(time.RFC3339, list[0].LastSeen)
	if err != nil || seen.Before(before) || seen.After(time.Now().Add(2*time.Second)) {
		t.Fatalf("last_seen after TouchKey = %q (%v)", list[0].LastSeen, err)
	}
	if data, _ := json.Marshal(list[0]); !strings.Contains(string(data), `"last_seen":"`+list[0].LastSeen+`"`) {
		t.Fatalf("last_seen missing on the wire: %s", data)
	}
	must(t, s.TouchKey("NOT A KEY")) // no row, no error

	// The time is kept beside auth_keys, which keeps the original's two
	// columns, and goes with the key.
	var rows int
	must(t, s.db.QueryRow(`SELECT COUNT(*) FROM auth_key_activity`).Scan(&rows))
	if rows != 1 {
		t.Fatalf("auth_key_activity has %d rows, want the used key's only", rows)
	}
	if _, err := s.db.Exec(`INSERT INTO auth_keys VALUES ('ORIGINAL', 'inserted as the original server does')`); err != nil {
		t.Fatalf("the original server's two-value insert: %v", err)
	}
	gone, err := s.DeleteKey(k.AuthKey)
	if err != nil || gone == nil || gone.AuthKey != k.AuthKey {
		t.Fatalf("DeleteKey = %+v, %v", gone, err)
	}
	must(t, s.db.QueryRow(`SELECT COUNT(*) FROM auth_key_activity`).Scan(&rows))
	if rows != 0 {
		t.Fatal("the last_seen of a deleted key is still kept")
	}
}

func TestKeyLastUpdate(t *testing.T) {
	// Without the server migration there is no auth_key_activity: ListKeys
	// still works and LastUpdate stays empty and off the wire.
	plain := newTestStore(t)
	k, err := plain.CreateKey("client")
	must(t, err)
	list, err := plain.ListKeys()
	must(t, err)
	if len(list) != 1 || list[0].LastUpdate != "" {
		t.Fatalf("ListKeys without the table = %+v", list)
	}
	if data, _ := json.Marshal(list[0]); strings.Contains(string(data), "last_update") {
		t.Fatalf("a key without the table must not carry last_update on the wire: %s", data)
	}
	if err := plain.MarkKeyUpdated(k.AuthKey); err == nil {
		t.Fatal("MarkKeyUpdated without the table should fail")
	}

	s := newTestStore(t)
	must(t, db.MigrateServer(s.db))
	k, err = s.CreateKey("client")
	must(t, err)
	if list, _ = s.ListKeys(); list[0].LastUpdate != "" {
		t.Fatalf("a new key has no last_update, got %q", list[0].LastUpdate)
	}
	// Being seen is not the same as having written.
	must(t, s.TouchKey(k.AuthKey))
	if list, _ = s.ListKeys(); list[0].LastUpdate != "" || list[0].LastSeen == "" {
		t.Fatalf("after TouchKey = %+v, want last_seen only", list[0])
	}
	before := time.Now().Add(-2 * time.Second)
	must(t, s.MarkKeyUpdated(k.AuthKey))
	list, _ = s.ListKeys()
	updated, err := time.Parse(time.RFC3339, list[0].LastUpdate)
	if err != nil || updated.Before(before) || updated.After(time.Now().Add(2*time.Second)) {
		t.Fatalf("last_update after MarkKeyUpdated = %q (%v)", list[0].LastUpdate, err)
	}
	if data, _ := json.Marshal(list[0]); !strings.Contains(string(data), `"last_update":"`+list[0].LastUpdate+`"`) {
		t.Fatalf("last_update missing on the wire: %s", data)
	}
	must(t, s.MarkKeyUpdated("NOT A KEY")) // no row, no error
}

func TestCounts(t *testing.T) {
	s := newTestStore(t)
	p, tk, b, err := s.Counts()
	must(t, err)
	if p != 0 || tk != 0 || b != 0 {
		t.Fatalf("Counts of an empty store = %d %d %d", p, tk, b)
	}
	must(t, s.UpsertPrefixes([]Prefix{{"A", "red", 1, 0}, {"B", "blue", 2, 0}}))
	must(t, s.UpsertTickets([]Ticket{{"A", 1, "", "", "", "CALL", 0}, {"A", 2, "", "", "", "CALL", 0}, {"B", 1, "", "", "", "CALL", 0}}))
	must(t, s.UpsertBaskets([]Basket{{"A", 1, "Wine", "", 0, 0, 0}}))
	p, tk, b, err = s.Counts()
	must(t, err)
	if p != 2 || tk != 3 || b != 1 {
		t.Fatalf("Counts = %d %d %d, want 2 3 1", p, tk, b)
	}
}

func TestBackupRoundTrip(t *testing.T) {
	src := newTestStore(t)
	must(t, src.UpsertPrefixes([]Prefix{{"A", "red", 1, 0}}))
	must(t, src.UpsertBaskets([]Basket{{"A", 1, "Wine", "Smiths", 0, 0, 0}}))
	must(t, src.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 2}}))
	must(t, src.UpsertTickets([]Ticket{{"A", 2, "Amy", "Adams", "555", "TEXT", 0}}))

	bf, err := src.Export()
	must(t, err)
	if len(bf.Prefixes) != 1 || len(bf.Baskets) != 1 || len(bf.Tickets) != 1 {
		t.Fatalf("Export = %+v", bf)
	}

	dst := newTestStore(t)
	must(t, dst.Import(bf))
	must(t, dst.Import(bf)) // restoring twice is harmless
	bf2, _ := dst.Export()
	if !reflect.DeepEqual(bf, bf2) {
		t.Fatalf("round trip mismatch:\n%+v\n%+v", bf, bf2)
	}
	if bf2.Baskets[0].WinningTicket != 2 {
		t.Fatal("restore must keep winning tickets")
	}

	empty := newTestStore(t)
	ebf, _ := empty.Export()
	if ebf.Prefixes == nil || ebf.Baskets == nil || ebf.Tickets == nil {
		t.Fatalf("Export of an empty store must use empty slices, got %+v", ebf)
	}
}
