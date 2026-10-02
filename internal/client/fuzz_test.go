package client

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/store"
)

// The fuzz targets below double as ordinary tests: go test runs every seed
// and every input saved under testdata/fuzz. Each input gets a client of its
// own, and a server of its own when it runs paired (newFixture and
// remoteFixture), so an input that fails fails again when it runs alone.

// FuzzTicketRoundTrip: a ticket that tam-client accepts, paired with a
// server or standalone, is in every store the save reaches (the client's
// own copy, and the server's, directly or through the client's queue) as it
// was sent: prefix and preference trimmed, the text as a JSON decoder reads
// it. Every route the pages read it through returns it: the list of all
// tickets, and the single ticket, the range and the prefix's list with the
// prefix escaped as the web app escapes it, and as tam-client escapes it for
// the server. A search for its own name and phone number finds it, and a
// pull leaves the client's copy equal to the server's. A batch the client
// refuses answers 4xx with a detail and changes nothing anywhere.
func FuzzTicketRoundTrip(f *testing.F) {
	type seed struct {
		prefix                   string
		id                       int64
		first, last, phone, pref string
		form, grow               uint8
		raw                      bool
		batch                    uint8
	}
	for _, s := range []seed{
		{"A", 1, "Ann", "Lee", "555-0001", "CALL", 0, 0, false, 0},
		{"A", 1, "Ann", "Lee", "555-0001", "CALL", 0, 0, false, 4},
		{"A/B", 2, "Slash", "", "", "TEXT", 1, 0, false, 0},
		{"A/B", 2, "Slash", "", "", "TEXT", 1, 0, false, 4},
		{"50% off", 3, "", "", "", " call ", 2, 0, false, 1},
		{"a#b?c&d+e;f=g", 4, "Q", "", "", "", 3, 0, false, 0},
		{"a:b@c$d,e", 4, "", "", "", "", 0, 0, false, 0},
		{"~!*'()", 5, "", "", "", "", 0, 0, false, 4},
		{"A+B C", 6, "", "", "", "", 0, 0, false, 3},
		{"  padded  ", 0, "  keep  ", "", "", "", 0, 0, false, 5},
		{"日本", 7, "Zoë", "Ó Briain", "☎ 555", "TEXT", 3, 0, false, 7},
		{"🎟\U0000FE0F", 8, "🎉", "", "", "", 0, 0, false, 0},
		{`A\B`, 9, `back\slash`, "", "", "", 0, 0, false, 0},
		{`"q'uote`, 10, `"`, "'", "", "", 0, 0, false, 0},
		{"A\x00B", 11, "a\x00b", "\x00", "5\x00", "CALL", 0, 0, false, 0},
		{"A\x00B", 11, "a\x00b", "\x00", "5\x00", "CALL", 0, 0, false, 4},
		{"\U00002028\U00002029\U0000200B", 12, "\U0000202E", "\U0000FEFF", "", "", 0, 0, false, 0},
		{"%2F", 13, "", "", "", "", 0, 0, false, 0},
		{"%", 14, "%", "_", `\`, "", 0, 0, false, 4},
		{".", 1, "Dot", "", "", "", 0, 0, false, 0},
		{"..", 1, "Dots", "", "", "", 0, 0, false, 4},
		{"...", 1, "Three", "", "", "", 0, 0, false, 0},
		{"A", 0, "Zero", "", "", "", 0, 0, false, 0},
		{"A", -1, "Negative", "", "", "", 0, 0, false, 0},
		{"A", math.MaxInt64, "Largest", "", "", "", 0, 0, false, 0},
		{"A", math.MaxInt64, "Largest", "", "", "", 0, 0, false, 4},
		{"A", 1<<53 + 1, "Rounded", "", "", "", 2, 0, false, 0},
		{"A", 1<<53 + 3, "Too big for a float", "", "", "", 2, 0, false, 0},
		{"", 1, "No prefix", "", "", "", 0, 0, false, 0},
		{"   ", 1, "Blank prefix", "", "", "", 0, 0, false, 4},
		{"\xff\xfe", 1, "\xff", "\xed\xa0\x80", "\xe2\x82", "CALL", 0, 0, true, 0},
		{"\xff\xfe", 1, "\xff", "\xed\xa0\x80", "\xe2\x82", "CALL", 0, 0, false, 0},
		{"L", 1, "x", "y", "z", "CALL", 0, 255, false, 0},
		{"L", 1, "x", "y", "z", "CALL", 0, 255, false, 4},
		{"A", 1, "Ann", "Lee", "5", "CALL", 0, 0, false, 2},
		{"A", 1, "Ann", "Lee", "5", "CALL", 0, 0, false, 6},
		{"A", 7, "Ann", "Lee", "5", "CALL", 0, 0, false, 3},
		{"A", 1, "Ann", "Lee", "555-0001", "CALL", 0, 0, false, 8},
		{"A/B", 2, "Slash", "", "", "TEXT", 1, 0, false, 9},
		{"A", 1, "Ann", "Lee", "5", "CALL", 0, 0, false, 10},
		{"A", 7, "Ann", "Lee", "5", "CALL", 0, 0, false, 11},
		{"A\x00B", 11, "a\x00b", "\x00", "5\x00", "CALL", 0, 0, false, 8},
		{"..", 1, "Dots", "", "", "", 0, 0, false, 8},
		{"\xff\xfe", 1, "\xff", "\xed\xa0\x80", "\xe2\x82", "CALL", 0, 0, true, 8},
		{"L", 1, "x", "y", "z", "CALL", 0, 255, false, 8},
	} {
		f.Add(s.prefix, s.id, s.first, s.last, s.phone, s.pref, s.form, s.grow, s.raw, s.batch)
	}
	f.Fuzz(func(t *testing.T, prefix string, id int64, first, last, phone, pref string, form, grow uint8, raw bool, batch uint8) {
		if grow > 0 {
			n := int(grow) * 235
			prefix, first, last, phone, pref = lengthen(prefix, n), lengthen(first, n), lengthen(last, n), lengthen(phone, n), lengthen(pref, n)
		}
		fx := newFixture(t)
		// Bit 2 of batch leaves the client standalone. Bit 3 has the server
		// refuse the client's key during the save, so a save it accepts waits
		// in the client's queue and goes to the server once the key is good
		// again.
		sts := stores(t, fx, batch&4 != 0)
		queued := len(sts) == 2 && batch&8 != 0

		spelled, tid, idOK := jsonID(id, form)
		want := store.Ticket{
			Prefix: strings.TrimSpace(decoded(prefix)), TID: int(tid),
			FirstName: decoded(first), LastName: decoded(last), PhoneNumber: decoded(phone),
			Pref: strings.TrimSpace(decoded(pref)),
		}
		accepted := want.Prefix != "" && idOK && want.TID >= 0
		items := []string{ticketJSON(prefix, spelled, first, last, phone, pref, raw)}
		// A neighbour in every store, which the save must leave alone.
		nb := store.Ticket{Prefix: "A", TID: 99, FirstName: "Neighbour", Pref: "CALL"}
		if want.Prefix == nb.Prefix && want.TID == nb.TID {
			nb.TID = 98
		}
		saveTickets(t, sts, []store.Ticket{nb})
		switch batch % 4 {
		case 1:
			// The same ticket earlier in the batch, its prefix padded: the
			// later one wins.
			items = slices.Insert(items, 0, ticketJSON(" "+prefix+" ", spelled, "Earlier", "", "", "CALL", raw))
		case 2:
			// An invalid ticket after it: the whole batch is refused.
			items = append(items, `{"prefix":"A","t_id":-1,"pref":"CALL"}`)
			accepted = false
		case 3:
			// The ticket is saved already.
			if accepted {
				saveTickets(t, sts, []store.Ticket{{Prefix: want.Prefix, TID: want.TID, FirstName: "Before", Pref: "TEXT"}})
			}
		}
		before := takeSnapshot(t, fx, sts)
		settings, err := config.Load(fx.settings)
		if err != nil {
			t.Fatal(err)
		}
		if queued {
			refusedKey := settings
			refusedKey.RemoteKey = "REFUSED"
			if err := config.Save(fx.settings, refusedKey); err != nil {
				t.Fatal(err)
			}
		}

		body := "[" + strings.Join(items, ",") + "]"
		code, resp := fx.do("POST", "/api/tickets", body, nil)
		if code != 200 {
			if accepted {
				t.Fatalf("POST /api/tickets %s = %d %s, want it accepted as %+v", clip(body), code, clip(resp), want)
			}
			refused(t, "POST /api/tickets "+clip(body), code, resp)
			unchanged(t, fx, sts, before)
			return
		}
		if !accepted {
			t.Fatalf("POST /api/tickets %s = 200 %s, want it refused", clip(body), clip(resp))
		}
		if saved := decode[[]store.Ticket](t, resp); len(saved) != len(items) || valT(saved[len(saved)-1]) != want {
			t.Fatalf("POST /api/tickets answered %+v, want %d rows ending with %+v", saved, len(items), want)
		}
		if queued {
			if p, fl := pending(t, fx.st); p != 1 || fl != 0 {
				t.Fatalf("a save while the server refused the key left pending %d, failed %d; want it queued", p, fl)
			}
			if err := config.Save(fx.settings, settings); err != nil {
				t.Fatal(err)
			}
			fx.h.sync.Reset()
			fx.h.sync.Tick()
		}
		notQueued(t, fx)
		for i, st := range sts {
			if got, err := st.Ticket(want.Prefix, want.TID); err != nil || got == nil || valT(*got) != want {
				t.Fatalf("%s has %+v (%v), want %+v", side(i), got, err, want)
			}
			if got, err := st.Ticket(nb.Prefix, nb.TID); err != nil || got == nil || valT(*got) != nb {
				t.Fatalf("the save changed the neighbour in %s to %+v (%v)", side(i), got, err)
			}
		}
		if all := get[[]store.Ticket](t, fx, "/api/tickets"); !slices.Contains(valsT(all), want) {
			t.Fatalf("GET /api/tickets lacks %+v", want)
		}
		if inPath(want.Prefix) {
			ids := strconv.Itoa(want.TID)
			for _, esc := range escapes {
				seg := esc.fn(want.Prefix)
				if one := get[store.Ticket](t, fx, "/api/tickets/"+seg+"/"+ids); valT(one) != want {
					t.Fatalf("the single ticket with the prefix escaped by %s is %+v, want %+v", esc.name, one, want)
				}
				if rng := get[[]store.Ticket](t, fx, "/api/tickets/"+seg+"/"+ids+"/"+ids); len(rng) != 1 || valT(rng[0]) != want {
					t.Fatalf("the range with the prefix escaped by %s is %+v, want only %+v", esc.name, rng, want)
				}
				list := get[[]store.Ticket](t, fx, "/api/tickets/"+seg)
				for _, row := range list {
					if row.Prefix != want.Prefix {
						t.Fatalf("the list of %s escaped by %s holds a ticket of %s", clip(want.Prefix), esc.name, clip(row.Prefix))
					}
				}
				if !slices.Contains(valsT(list), want) {
					t.Fatalf("the list of the prefix escaped by %s lacks %+v", esc.name, want)
				}
			}
		}
		q := url.Values{"first_name": {want.FirstName}, "last_name": {want.LastName}, "phone_number": {want.PhoneNumber}}
		if found := get[[]store.Ticket](t, fx, "/api/search/tickets?"+q.Encode()); !slices.Contains(valsT(found), want) {
			t.Fatalf("a search for the ticket's own fields found %d tickets, not %+v", len(found), want)
		}
		if len(sts) == 2 {
			// A pull copies the server's data into the client's copy, which
			// then holds what the server holds, a row only the server had
			// included.
			only := store.Ticket{Prefix: "Z", TID: 1, FirstName: "Server only", Pref: "CALL"}
			if want.Prefix == only.Prefix && want.TID == only.TID {
				only.TID = 2
			}
			saveTickets(t, sts[1:], []store.Ticket{only})
			fx.h.sync.Reset()
			fx.h.sync.Tick()
			client, err := sts[0].Export()
			if err != nil {
				t.Fatal(err)
			}
			server, err := sts[1].Export()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(client, server) {
				t.Fatalf("after a pull the client's copy holds %+v, the server %+v", client.Tickets, server.Tickets)
			}
		}
	})
}

// ticketJSON writes one ticket as the tickets page sends it, with the id
// already spelled.
func ticketJSON(prefix, id, first, last, phone, pref string, raw bool) string {
	return `{"prefix":` + jsonText(prefix, raw) + `,"t_id":` + id + `,"first_name":` + jsonText(first, raw) +
		`,"last_name":` + jsonText(last, raw) + `,"phone_number":` + jsonText(phone, raw) +
		`,"pref":` + jsonText(pref, raw) + `,"changed":true}`
}

// FuzzBasketRoundTrip: a basket that tam-client accepts, paired with a
// server or standalone, is in every store the save reaches as it was sent,
// whether it came from the baskets form (which never changes the winning
// ticket of a basket that exists) or from the drawing form (which changes
// only the winning ticket), and the pages read it back through every route:
// the list of all baskets, the single basket, the range, the prefix's list,
// the drawing line and the by-basket report. A refused save changes nothing
// anywhere.
func FuzzBasketRoundTrip(f *testing.F) {
	type seed struct {
		prefix              string
		id                  int64
		description, donors string
		winning             int64
		form, grow          uint8
		raw                 bool
		mode                uint8
	}
	for _, s := range []seed{
		{"A", 1, "Wine", "The Smiths", 0, 0, 0, false, 0},
		{"A", 1, "Wine", "The Smiths", 0, 0, 0, false, 4},
		{"A", 1, "Red wine", "The Smiths", 7, 1, 0, false, 1},
		{"A", 2, "", "", 12, 2, 0, false, 2},
		{"A", 2, "Spa", "", 12, 3, 0, false, 3},
		{"A", 2, "Spa", "", 12, 3, 0, false, 7},
		{"A/B", 3, "Slash", "Donor & Co", 1, 0, 0, false, 0},
		{"A/B", 3, "Slash", "Donor & Co", 1, 0, 0, false, 5},
		{"50%", 4, "100% cotton", "_", 0, 0, 0, false, 2},
		{"日本", 5, "お茶", "Zoë", 3, 0, 0, false, 1},
		{"A\x00B", 6, "a\x00b", "\x00", 0, 0, 0, false, 0},
		{".", 1, "Dot", "", 0, 0, 0, false, 0},
		{"A", 0, "Zero", "", 0, 0, 0, false, 0},
		{"A", -1, "Negative", "", 0, 0, 0, false, 0},
		{"A", 1, "Negative winner", "", -1, 0, 0, false, 2},
		{"A", math.MaxInt64, "Largest", "", math.MaxInt64, 0, 0, false, 0},
		{"A", math.MaxInt64, "Largest", "", math.MaxInt64, 0, 0, false, 6},
		{" ", 1, "Blank prefix", "", 0, 0, 0, false, 0},
		{"\xff", 1, "\xfe", "\xed\xa0\x80", 0, 0, 0, true, 0},
		{"L", 1, "x", "y", 0, 0, 255, false, 1},
	} {
		f.Add(s.prefix, s.id, s.description, s.donors, s.winning, s.form, s.grow, s.raw, s.mode)
	}
	f.Fuzz(func(t *testing.T, prefix string, id int64, description, donors string, winning int64, form, grow uint8, raw bool, mode uint8) {
		if grow > 0 {
			n := int(grow) * 235
			prefix, description, donors = lengthen(prefix, n), lengthen(description, n), lengthen(donors, n)
		}
		fx := newFixture(t)
		// Bit 2 of mode leaves the client standalone.
		sts := stores(t, fx, mode&4 != 0)

		spelledID, bid, idOK := jsonID(id, form)
		spelledWin, win, winOK := jsonID(winning, form)
		want := store.Basket{
			Prefix: strings.TrimSpace(decoded(prefix)), BID: int(bid),
			Description: decoded(description), Donors: decoded(donors), WinningTicket: int(win),
		}
		accepted := want.Prefix != "" && idOK && winOK && want.BID >= 0 && want.WinningTicket >= 0
		// A neighbour in every store, which the save must leave alone.
		nb := store.Basket{Prefix: "A", BID: 99, Description: "Neighbour", Donors: "Next door", WinningTicket: 3}
		if want.Prefix == nb.Prefix && want.BID == nb.BID {
			nb.BID = 98
		}
		saveBaskets(t, sts, []store.Basket{nb})
		route := "/api/baskets"
		if mode%4 >= 2 {
			route = "/api/drawing"
		}
		switch mode % 4 {
		case 1:
			// The baskets form keeps the winning ticket of a basket that exists.
			if accepted {
				saveBaskets(t, sts, []store.Basket{{Prefix: want.Prefix, BID: want.BID, Description: "Before", Donors: "Old", WinningTicket: 5}})
			}
			want.WinningTicket = 5
		case 2:
			// The drawing form creates the basket with nothing but the winner.
			want.Description, want.Donors = "", ""
		case 3:
			// The drawing form changes only the winning ticket.
			if accepted {
				saveBaskets(t, sts, []store.Basket{{Prefix: want.Prefix, BID: want.BID, Description: "Before", Donors: "Old", WinningTicket: 5}})
			}
			want.Description, want.Donors = "Before", "Old"
		}
		before := takeSnapshot(t, fx, sts)

		body := `[{"prefix":` + jsonText(prefix, raw) + `,"b_id":` + spelledID + `,"description":` + jsonText(description, raw) +
			`,"donors":` + jsonText(donors, raw) + `,"winning_ticket":` + spelledWin + `,"changed":true}]`
		code, resp := fx.do("POST", route, body, nil)
		if code != 200 {
			if accepted {
				t.Fatalf("POST %s %s = %d %s, want it accepted", route, clip(body), code, clip(resp))
			}
			refused(t, "POST "+route+" "+clip(body), code, resp)
			unchanged(t, fx, sts, before)
			return
		}
		if !accepted {
			t.Fatalf("POST %s %s = 200 %s, want it refused", route, clip(body), clip(resp))
		}
		notQueued(t, fx)
		for i, st := range sts {
			if got, err := st.Basket(want.Prefix, want.BID); err != nil || got == nil || valB(*got) != want {
				t.Fatalf("after POST %s %s has %+v (%v), want %+v", route, side(i), got, err, want)
			}
			if got, err := st.Basket(nb.Prefix, nb.BID); err != nil || got == nil || valB(*got) != nb {
				t.Fatalf("POST %s changed the neighbour in %s to %+v (%v)", route, side(i), got, err)
			}
		}
		if all := get[[]store.Basket](t, fx, "/api/baskets"); !slices.Contains(valsB(all), want) {
			t.Fatalf("GET /api/baskets lacks %+v", want)
		}
		if !inPath(want.Prefix) {
			return
		}
		ids := strconv.Itoa(want.BID)
		line := store.DrawingLine{Prefix: want.Prefix, BID: want.BID, Description: want.Description, WinningTicket: want.WinningTicket}
		for _, esc := range escapes {
			seg := esc.fn(want.Prefix)
			if one := get[store.Basket](t, fx, "/api/baskets/"+seg+"/"+ids); valB(one) != want {
				t.Fatalf("the single basket with the prefix escaped by %s is %+v, want %+v", esc.name, one, want)
			}
			if rng := get[[]store.Basket](t, fx, "/api/baskets/"+seg+"/"+ids+"/"+ids); len(rng) != 1 || valB(rng[0]) != want {
				t.Fatalf("the range with the prefix escaped by %s is %+v, want only %+v", esc.name, rng, want)
			}
			if list := get[[]store.Basket](t, fx, "/api/baskets/"+seg); !slices.Contains(valsB(list), want) {
				t.Fatalf("the list of the prefix escaped by %s lacks %+v", esc.name, want)
			}
			if got := get[store.DrawingLine](t, fx, "/api/drawing/"+seg+"/"+ids); valD(got) != line {
				t.Fatalf("the drawing line with the prefix escaped by %s is %+v, want %+v", esc.name, got, line)
			}
			report := get[[]store.ReportByBasketLine](t, fx, "/api/reports/bybasket/"+seg)
			if !slices.ContainsFunc(report, func(l store.ReportByBasketLine) bool {
				return l.Prefix == want.Prefix && l.BID == want.BID && l.Description == want.Description && l.Donors == want.Donors && l.WinningTicket == want.WinningTicket
			}) {
				t.Fatalf("the by-basket report with the prefix escaped by %s lacks %+v", esc.name, want)
			}
		}
	})
}

// FuzzPrefixRoundTrip: a prefix that tam-client accepts, paired with a
// server or standalone, is in every store the save reaches and in the list
// the pages read, as it was sent (the name trimmed), and its name works
// wherever the pages put it: a ticket saved under it comes back through the
// single ticket, range and list routes with the name escaped as the web app
// escapes it (and as tam-client escapes it for the server), the baskets,
// drawing and report routes answer for it, and a delete with the name in
// the query removes it everywhere. A refused prefix changes nothing
// anywhere.
func FuzzPrefixRoundTrip(f *testing.F) {
	type seed struct {
		name       string
		pick       uint8
		color      string
		weight     int64
		form       uint8
		raw, exist bool
	}
	for _, s := range []seed{
		{"CALL", 6, "", 1, 0, false, false},
		{"CALL", 14, "", 1, 0, false, false},
		{" A ", 1, "", 0, 1, false, true},
		{"A&B", 3, "", 2, 2, false, false},
		{"50%", 0, "", 3, 3, false, false},
		{"C+", 2, "", 4, 0, false, false},
		{"a b", 12, "", 5, 0, false, false},
		{"a#b?c=d;e", 5, "", 6, 0, false, false},
		{"a#b?c=d;e", 13, "", 6, 0, false, false},
		{"a:b@c$d,e", 5, "", 6, 0, false, false},
		{"~!*'()", 5, "", 6, 0, false, false},
		{`"q'`, 5, "", 6, 0, false, false},
		{"日本", 0, "", 7, 0, false, true},
		{"🎟\U0000FE0F", 1, "", 8, 0, false, false},
		{strings.Repeat("€", 100), 1, "", 8, 0, false, false},
		{strings.Repeat("L", 101), 1, "", 8, 0, false, false},
		{"%2F", 1, "", 9, 0, false, false},
		{"%2e", 9, "", 9, 0, false, false},
		{"...", 1, "", 9, 0, false, false},
		{".", 1, "", 9, 0, false, false},
		{"..", 9, "", 9, 0, false, false},
		{"A/B", 1, "", 9, 0, false, false},
		{`A\B`, 1, "", 9, 0, false, false},
		{"A\tB", 1, "", 9, 0, false, false},
		{"", 1, "", 9, 0, false, false},
		{"A", 7, "chartreuse", 1, 0, false, false},
		{"A", 15, "Red", 1, 0, false, false},
		{"A", 1, "", -1, 0, false, false},
		{"A", 1, "", math.MaxInt64, 0, false, false},
		{"A", 1, "", 1 << 53, 2, false, false},
		{"\xff\xfe", 7, "\xff", 1, 0, true, false},
	} {
		f.Add(s.name, s.pick, s.color, s.weight, s.form, s.raw, s.exist)
	}
	f.Fuzz(func(t *testing.T, name string, pick uint8, color string, weight int64, form uint8, raw, exist bool) {
		// Most inputs take a colour from the palette, so that the name is
		// what decides.
		if int(pick%8) < len(store.Colors) {
			color = store.Colors[pick%8]
		}
		fx := newFixture(t)
		// Bit 3 of pick leaves the client standalone.
		sts := stores(t, fx, pick&8 != 0)

		spelled, w, weightOK := jsonID(weight, form)
		clean, nameErr := store.ValidatePrefixName(decoded(name))
		want := store.Prefix{Prefix: clean, Color: decoded(color), Weight: int(w)}
		accepted := nameErr == nil && slices.Contains(store.Colors, want.Color) && weightOK && want.Weight >= 0
		if exist && nameErr == nil {
			for _, st := range sts {
				if err := st.UpsertPrefixes([]store.Prefix{{Prefix: clean, Color: "white", Weight: 99}}); err != nil {
					t.Fatal(err)
				}
			}
		}
		before := takeSnapshot(t, fx, sts)

		body := `[{"prefix":` + jsonText(name, raw) + `,"color":` + jsonText(color, raw) + `,"weight":` + spelled + `}]`
		code, resp := fx.do("POST", "/api/prefixes", body, nil)
		if code != 200 {
			if accepted {
				t.Fatalf("POST /api/prefixes %s = %d %s, want it accepted as %+v", clip(body), code, clip(resp), want)
			}
			refused(t, "POST /api/prefixes "+clip(body), code, resp)
			unchanged(t, fx, sts, before)
			return
		}
		if !accepted {
			t.Fatalf("POST /api/prefixes %s = 200 %s, want it refused", clip(body), clip(resp))
		}
		notQueued(t, fx)
		if saved := decode[[]store.Prefix](t, resp); len(saved) != 1 || valP(saved[0]) != want {
			t.Fatalf("POST /api/prefixes answered %+v, want %+v", saved, want)
		}
		for i, st := range sts {
			if ps, err := st.ListPrefixes(); err != nil || !slices.Contains(valsP(ps), want) {
				t.Fatalf("%s lists %+v (%v), want %+v among them", side(i), ps, err, want)
			}
		}
		if ps := get[[]store.Prefix](t, fx, "/api/prefixes"); !slices.Contains(valsP(ps), want) {
			t.Fatalf("GET /api/prefixes = %+v, want %+v among them", ps, want)
		}

		tk := store.Ticket{Prefix: clean, TID: 1, FirstName: "Path", Pref: "CALL"}
		if code, resp := fx.do("POST", "/api/tickets", []store.Ticket{tk}, nil); code != 200 {
			t.Fatalf("a ticket under the accepted prefix %q: %d %s", clean, code, clip(resp))
		}
		for _, esc := range escapes {
			seg := esc.fn(clean)
			if one := get[store.Ticket](t, fx, "/api/tickets/"+seg+"/1"); valT(one) != tk {
				t.Fatalf("the ticket under %q escaped by %s is %+v, want %+v", clean, esc.name, one, tk)
			}
			rng := get[[]store.Ticket](t, fx, "/api/tickets/"+seg+"/1/3")
			if len(rng) != 3 || valT(rng[0]) != tk || rng[1].Prefix != clean || rng[1].TID != 2 || rng[2].Prefix != clean || rng[2].TID != 3 {
				t.Fatalf("the range under %q escaped by %s is %+v, want the ticket and two placeholders", clean, esc.name, rng)
			}
			if list := get[[]store.Ticket](t, fx, "/api/tickets/"+seg); len(list) != 1 || valT(list[0]) != tk {
				t.Fatalf("the tickets of %q escaped by %s are %+v, want only %+v", clean, esc.name, list, tk)
			}
			if bs := get[[]store.Basket](t, fx, "/api/baskets/"+seg+"/1/2"); len(bs) != 2 || bs[0].Prefix != clean || bs[1].Prefix != clean {
				t.Fatalf("the baskets of %q escaped by %s are %+v, want two placeholders", clean, esc.name, bs)
			}
			if ds := get[[]store.DrawingLine](t, fx, "/api/drawing/"+seg+"/1/2"); len(ds) != 2 || ds[0].Prefix != clean || ds[1].Prefix != clean {
				t.Fatalf("the drawing of %q escaped by %s is %+v, want two placeholders", clean, esc.name, ds)
			}
			get[[]store.ReportByNameLine](t, fx, "/api/reports/byname/"+seg)
			get[[]store.ReportByBasketLine](t, fx, "/api/reports/bybasket/"+seg)
		}

		code, resp = fx.do("DELETE", "/api/prefixes?p="+encodeURIComponent(clean), nil, nil)
		if code != 200 || valP(decode[store.Prefix](t, resp)) != want {
			t.Fatalf("DELETE of %q = %d %s, want 200 with %+v", clean, code, clip(resp), want)
		}
		for i, st := range sts {
			if ps, err := st.ListPrefixes(); err != nil || slices.ContainsFunc(ps, func(p store.Prefix) bool { return p.Prefix == clean }) {
				t.Fatalf("after the delete %s lists %+v (%v)", side(i), ps, err)
			}
		}
	})
}

// clientPostRoutes are the client's POST routes; the push route takes its
// target from the input.
var clientPostRoutes = []string{
	"/api/shutdown", "/api/settings", "/api/pair", "/api/unpair", "/api/outbox/retry", "/api/outbox/discard",
	"/api/auth", "/api/prefixes", "/api/tickets", "/api/baskets", "/api/drawing", "/api/search/tickets",
	"/api/backuprestore/local", "/api/backuprestore/remote", "/api/backuprestore/push/",
}

// FuzzClientPostRoutes: no body and no Content-Type makes a POST route of
// tam-client answer 500 or panic, in remote mode or standalone. The only
// 500s are the ones the original answered on purpose while no server is
// set, which TestStandaloneAuthAndPush pins. Every refusal carries a
// {"detail": ...} body and every success is JSON.
func FuzzClientPostRoutes(f *testing.F) {
	type seed struct {
		route      uint8
		target, ct string
		body       string
		remote     bool
		flags      uint8
	}
	const js = "application/json"
	for _, s := range []seed{
		{0, "", js, `{}`, true, 0},
		{1, "", js, `{"venue_name":" Hall ","remote_port":"8443"}`, true, 0},
		{1, "", js, `{"remote_server":"tam.lan","remote_port":"0"}`, false, 0},
		{1, "", js, `{"remote_port":null}`, true, 0},
		{2, "", js, `{"host":"tam.lan","port":"8000","password":"secret"}`, false, 0},
		{2, "", js, `{"host":"tam.lan","port":"8000","tls":true,"password":"secret"}`, true, 0},
		{2, "", js, `{"host":"tam.lan","password":"wrong"}`, true, 0},
		{2, "", js, `{"host":"","password":"secret"}`, true, 0},
		{3, "", js, `{"password":"secret"}`, true, 0},
		{3, "", js, `{}`, false, 0},
		{4, "", js, `{}`, true, 0},
		{5, "", js, `null`, true, 0},
		{6, "", js, `{"description":"tablet"}`, true, 1},
		{6, "", js, `{"description":"tablet"}`, false, 1},
		{6, "", js, `{"description":5}`, true, 1},
		{7, "", js, `[{"prefix":"A","color":"red","weight":"1"}]`, true, 0},
		{7, "", js, `[{"prefix":".","color":"red","weight":1}]`, false, 0},
		{8, "", js, `[{"prefix":"A","t_id":"1","first_name":"Ann","pref":"CALL","changed":true}]`, true, 0},
		{8, "", js, `[{"prefix":"A","t_id":1.5}]`, false, 0},
		{9, "", js, `[{"prefix":"A","b_id":1,"description":"Wine"}]`, true, 0},
		{10, "", js, `[{"prefix":"A","b_id":1,"winning_ticket":"7","last_name":"","changed":true}]`, true, 0},
		{11, "", js, `[{"prefix":"A","t_id":2,"first_name":"Bo"}]`, true, 0},
		{12, "", js, `{"prefixes":[{"prefix":"OLD","color":"gray","weight":1}],"baskets":[],"tickets":[]}`, false, 0},
		{13, "", js, `{"prefixes":[],"baskets":[{"prefix":"A","b_id":1,"winning_ticket":7}],"tickets":[]}`, true, 0},
		{13, "", js, `{}`, false, 0},
		{14, "tickets", js, `{}`, true, 0},
		{14, "baskets", js, `{}`, false, 0},
		{14, "keys", js, `{}`, true, 0},
		{14, "..", js, `{}`, true, 0},
		{14, "a/b", js, `{}`, true, 0},
		{8, "", "application/json; charset=utf-8", `[]`, true, 0},
		{8, "", "APPLICATION/JSON", `null`, true, 0},
		{8, "", "text/plain", `[]`, true, 0},
		{8, "", "", `[]`, true, 0},
		{8, "", "application/json;;", `[]`, true, 0},
		{8, "", "multipart/form-data; boundary=x", "--x\r\n", true, 0},
		{8, "", js, `[] []`, true, 0},
		{8, "", js, "\xef\xbb\xbf[]", true, 0},
		{8, "", js, `[{"prefix":"A","t_id":1e400}]`, true, 0},
		{8, "", js, `[null]`, true, 0},
		{8, "", js, `{bad`, true, 0},
		{8, "", js, ``, true, 0},
		{7, "", js, `[{"prefix":"A","color":"red","weight":1}]`, true, 4},
		{7, "", js, `[{"prefix":"A","color":"red","weight":1}]`, true, 2},
	} {
		f.Add(s.route, s.target, s.ct, []byte(s.body), s.remote, s.flags)
	}
	f.Fuzz(func(t *testing.T, route uint8, target, contentType string, body []byte, remote bool, flags uint8) {
		fx := newFixture(t)
		_, rs := remoteFixture(t, fx)
		if !remote {
			if err := config.Save(fx.settings, config.Defaults()); err != nil {
				t.Fatal(err)
			}
		}
		path := clientPostRoutes[int(route)%len(clientPostRoutes)]
		switch path {
		case "/api/backuprestore/push/":
			path += url.PathEscape(target)
		case "/api/pair":
			body = localPairing(body, rs.URL)
		}
		headers := map[string]string{"Content-Type": headerValue(contentType)}
		if flags&1 != 0 {
			headers["TAM-PWD"] = "secret"
		}
		switch flags >> 1 & 3 {
		case 1:
			headers["Sec-Fetch-Site"] = "cross-site"
		case 2:
			headers["Sec-Fetch-Site"] = "same-origin"
		}
		code, resp := fx.do("POST", path, string(body), headers)
		what := fmt.Sprintf("POST %s (remote %v, Content-Type %q) %s", clip(path), remote, headers["Content-Type"], clip(body))
		if code == 500 {
			if want := standaloneRefusal(remote, path); want == "" || !detailIs(resp, want) {
				t.Fatalf("%s = 500 %s", what, clip(resp))
			}
		}
		switch {
		case code >= 400:
			var doc map[string]json.RawMessage
			if json.Unmarshal(resp, &doc) != nil || doc["detail"] == nil {
				t.Fatalf("%s = %d %s, want a {\"detail\": ...} body", what, code, clip(resp))
			}
		case code/100 == 2:
			if !json.Valid(resp) {
				t.Fatalf("%s = %d %s, want JSON", what, code, clip(resp))
			}
		}
	})
}

// standaloneRefusal returns the detail of the 500 the original answered on
// purpose for path while no server is set, or "" when there is none.
func standaloneRefusal(remote bool, path string) string {
	switch {
	case remote:
		return ""
	case path == "/api/auth":
		return "Not configured."
	case path == "/api/backuprestore/remote",
		slices.Contains([]string{"prefixes", "tickets", "baskets"}, strings.TrimPrefix(path, "/api/backuprestore/push/")):
		return "Server not set."
	}
	return ""
}

func detailIs(body []byte, want string) bool {
	var doc struct {
		Detail string `json:"detail"`
	}
	return json.Unmarshal(body, &doc) == nil && doc.Detail == want
}

// localPairing points a pairing request at the test server. The handler
// dials whatever host the body names, and the fuzzer must not reach out to
// the network; a body that names no host goes as it came.
func localPairing(body []byte, serverURL string) []byte {
	var req pairRequest
	if json.Unmarshal(body, &req) != nil || strings.TrimSpace(req.Host) == "" {
		return body
	}
	u, _ := url.Parse(serverURL)
	req.Host, req.Port = u.Hostname(), u.Port()
	out, _ := json.Marshal(req)
	return out
}

// headerValue drops the bytes net/http refuses to send in a header value.
func headerValue(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 0x20 && c != 0x7f || c == '\t' {
			b = append(b, c)
		}
	}
	return string(b)
}

// escapes are the ways a prefix is put in a path segment: encodeURIComponent
// by the web app, url.PathEscape by tam-client on its way to the server.
var escapes = []struct {
	name string
	fn   func(string) string
}{{"encodeURIComponent", encodeURIComponent}, {"url.PathEscape", url.PathEscape}}

// encodeURIComponent escapes s as the web app's encodeURIComponent does
// before it puts a prefix in a URL: every byte of the UTF-8 except
// A-Z a-z 0-9 - _ . ! ~ * ' ( ) becomes a %XX escape.
func encodeURIComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// inPath reports whether a prefix can be written in a URL path segment at
// all. A segment . or .. is resolved away by the browser and by Go's
// ServeMux before any handler sees it, whatever the escaping; prefix names
// are refused for that reason, but a ticket or basket may carry such a
// prefix, as a row from an original database may.
func inPath(prefix string) bool {
	return prefix != "." && prefix != ".."
}

// jsonText writes s as a JSON string. With raw, bytes that are not UTF-8 go
// into the body as they are, as a careless client could send them;
// otherwise encoding/json replaces them first.
func jsonText(s string, raw bool) string {
	if !raw {
		b, _ := json.Marshal(s)
		return string(b)
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c < 0x20:
			fmt.Fprintf(&b, `\u%04x`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// decoded is what a JSON decoder makes of s: each byte that is not part of
// valid UTF-8 becomes U+FFFD.
func decoded(s string) string {
	return string([]rune(s))
}

// jsonID spells an id: form 0 a JSON integer, 1 a string, 2 a float, 3 a
// string padded with spaces. It returns the spelling, the id a decoder
// reads from it, and whether the decoder takes it at all: only an integer
// from -(2^53-1) to 2^53-1, the whole numbers a browser holds exactly.
func jsonID(id int64, form uint8) (spelled string, value int64, ok bool) {
	digits := strconv.FormatInt(id, 10)
	switch form % 4 {
	case 1:
		return `"` + digits + `"`, 0, false
	case 2:
		return digits + ".0", 0, false
	case 3:
		return `" ` + digits + ` "`, 0, false
	}
	return digits, id, id >= -(1<<53-1) && id <= 1<<53-1
}

// valT, valB, valD and valP are a row without the order numbers the server
// stamps on it (see store.Event), and valsT, valsB and valsP a list of
// them: the fuzz targets compare the values a save leaves.
func valT(t store.Ticket) store.Ticket           { t.Rev = 0; return t }
func valB(b store.Basket) store.Basket           { b.Rev, b.WinRev = 0, 0; return b }
func valD(d store.DrawingLine) store.DrawingLine { d.WinRev = 0; return d }
func valP(p store.Prefix) store.Prefix           { p.Rev = 0; return p }
func valsT(ts []store.Ticket) []store.Ticket     { return mapRows(ts, valT) }
func valsB(bs []store.Basket) []store.Basket     { return mapRows(bs, valB) }
func valsP(ps []store.Prefix) []store.Prefix     { return mapRows(ps, valP) }
func mapRows[T any](rows []T, f func(T) T) []T {
	out := make([]T, len(rows))
	for i, r := range rows {
		out[i] = f(r)
	}
	return out
}

// lengthen repeats s until it is at least n bytes long, so the fuzzer
// reaches the sizes where limits live without typing that much. An empty s
// stays empty.
func lengthen(s string, n int) string {
	if s == "" || len(s) >= n {
		return s
	}
	return strings.Repeat(s, (n+len(s)-1)/len(s))
}

// clip shortens a long string for a failure message.
func clip[T string | []byte](s T) string {
	if len(s) > 200 {
		return fmt.Sprintf("%q...(%d bytes)", s[:200], len(s))
	}
	return fmt.Sprintf("%q", s)
}

// get reads path through the client and decodes the 200 answer.
func get[T any](t *testing.T, fx *fixture, path string) T {
	t.Helper()
	code, body := fx.do("GET", path, nil, nil)
	if code != 200 {
		t.Fatalf("GET %s = %d %s", clip(path), code, clip(body))
	}
	return decode[T](t, body)
}

// stores leaves the client standalone or pairs it with a server of its own,
// and returns the stores a save through the client reaches: its own copy
// and, when paired, the server's.
func stores(t *testing.T, fx *fixture, standalone bool) []*store.Store {
	t.Helper()
	if standalone {
		return []*store.Store{fx.st}
	}
	rst, _ := remoteFixture(t, fx)
	return []*store.Store{fx.st, rst}
}

// side names the store at index i of what stores returned.
func side(i int) string {
	if i == 0 {
		return "the client's copy"
	}
	return "the server"
}

// saveTickets writes tickets straight into every store.
func saveTickets(t *testing.T, sts []*store.Store, ts []store.Ticket) {
	t.Helper()
	for _, st := range sts {
		if err := st.UpsertTickets(ts); err != nil {
			t.Fatal(err)
		}
	}
}

// saveBaskets writes baskets straight into every store, winning tickets
// included.
func saveBaskets(t *testing.T, sts []*store.Store, bs []store.Basket) {
	t.Helper()
	bf := store.NewBackupFile()
	bf.Baskets = bs
	for _, st := range sts {
		if err := st.Import(bf); err != nil {
			t.Fatal(err)
		}
	}
}

// snapshot is what a refused save must leave alone: the data in every
// store, and the client's queue.
type snapshot struct {
	data            []store.BackupFile
	pending, failed int
}

func takeSnapshot(t *testing.T, fx *fixture, sts []*store.Store) snapshot {
	t.Helper()
	var s snapshot
	for _, st := range sts {
		bf, err := st.Export()
		if err != nil {
			t.Fatal(err)
		}
		s.data = append(s.data, bf)
	}
	s.pending, s.failed = pending(t, fx.st)
	return s
}

// unchanged fails the test when anything moved since before.
func unchanged(t *testing.T, fx *fixture, sts []*store.Store, before snapshot) {
	t.Helper()
	if after := takeSnapshot(t, fx, sts); !reflect.DeepEqual(after, before) {
		t.Fatalf("a refused save changed the data:\nbefore %+v\nafter  %+v", before, after)
	}
}

// refused checks the answer to a request that must be refused: a 4xx with
// the original's {"detail": ...} body.
func refused(t *testing.T, what string, code int, body []byte) {
	t.Helper()
	var doc map[string]json.RawMessage
	if code/100 != 4 || json.Unmarshal(body, &doc) != nil || doc["detail"] == nil {
		t.Fatalf("%s = %d %s, want a 4xx with a detail", what, code, clip(body))
	}
}

// notQueued fails the test when a save that should have reached every
// store at once waits in the client's queue instead.
func notQueued(t *testing.T, fx *fixture) {
	t.Helper()
	if p, fl := pending(t, fx.st); p != 0 || fl != 0 {
		t.Fatalf("the save was queued (pending %d, failed %d) instead of reaching every store", p, fl)
	}
}
