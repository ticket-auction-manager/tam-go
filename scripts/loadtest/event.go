package main

import (
	"fmt"
	"math/rand/v2"
	"net/url"
	"strings"
	"sync"

	"ticket-auction-manager/tam-go/internal/store"
)

// key names a ticket or a basket: its prefix and id.
type key struct {
	prefix string
	id     int
}

func (k key) String() string { return fmt.Sprintf("%s %d", k.prefix, k.id) }

// sheet is a range of ids of one prefix: what one page of the tickets,
// baskets or drawing form opens and saves at once.
type sheet struct {
	prefix   string
	from, to int
}

func (s sheet) path(form string) string {
	return fmt.Sprintf("/api/%s/%s/%d/%d", form, url.PathEscape(s.prefix), s.from, s.to)
}

func (s sheet) size() int { return s.to - s.from + 1 }

// deal hands sheets out like a stack of paper: sheet i goes to client
// (i+offset) mod n, so every client gets sheets of every prefix and
// neighbouring sheets are entered at the same time.
func deal(sheets []sheet, n, offset int) [][]sheet {
	out := make([][]sheet, n)
	for i, s := range sheets {
		j := (i + offset) % n
		out[j] = append(out[j], s)
	}
	return out
}

var (
	firstNames = []string{"Ann", "Ben", "Carla", "Dev", "Elena", "Frank", "Grace", "Hugo", "Iris", "Jamal",
		"Kim", "Luis", "Mona", "Nate", "Olga", "Paul", "Quinn", "Rosa", "Sam", "Tara", "Uma", "Vic", "Wen",
		"Yara", "Zed", "Abby", "Bruno", "Chloe", "Dmitri", "Esther", "Farah", "Gus", "Hana", "Ivan", "June",
		"Kofi", "Lena", "Marco", "Nia", "Omar"}
	// A few names carry an apostrophe or an accent, as real ones do.
	lastNames = []string{"Adler", "Brooks", "Castillo", "Dunn", "Evans", "Fischer", "Gomez", "Hart", "Ibsen",
		"Jones", "Kaur", "Lopez", "Meyer", "Novak", "Ortiz", "Patel", "Quist", "Reyes", "Silva", "Tran",
		"Underwood", "Varga", "Walsh", "Young", "Zhou", "O'Brien", "Nguyen", "Kowalski", "Müller", "Haddad",
		"Okafor", "Ramírez", "Sato", "Bianchi", "Dubois", "Larsen", "Costa", "Murphy", "Schmidt", "Ivanova"}
	items = []string{"Weekend at the lake cabin", "Handmade quilt, queen size", "Wine tasting for six",
		"Kids' bike and helmet", "Tool chest with hand tools", "Spa day for two", "Season of fresh vegetables",
		"Grill and accessories", "Cordless drill set", "Coffee for a year", "Family photo session",
		"Firehouse dinner for eight", "Garden furniture set", "Concert tickets, front row", "Signed team jersey",
		"Pizza party for twelve", "Smart TV, 55 inch", "Fishing trip with a guide", "Local honey gift basket",
		"Golf lessons", "Pottery class", "Car detailing", "Movie night bundle", "Board game collection"}
	donors = []string{"The Fischer family", "Riverside Quilters", "Hilltop Vineyard", "Pedal Shop",
		"Hart Hardware", "Serenity Spa", "Meyer Farm", "Castillo's Appliances", "Corner Roasters",
		"Rosa Reyes Photography", "Riverside Fire Company", "Walsh Nursery", "Brooks Music Hall",
		"Riverside Rovers", "Nonna's Kitchen", "Lake Guides Co."}
	prefs = []string{"CALL", "CALL", "TEXT"}
)

func pick[T any](rng *rand.Rand, xs []T) T { return xs[rng.IntN(len(xs))] }

func phoneNumber(rng *rand.Rand) string {
	return fmt.Sprintf("555-%03d-%04d", 100+rng.IntN(900), rng.IntN(10000))
}

// corrected returns the ticket with a different phone number, the usual fix.
func corrected(t store.Ticket, rng *rand.Rand) store.Ticket {
	for old := t.PhoneNumber; t.PhoneNumber == old; {
		t.PhoneNumber = phoneNumber(rng)
	}
	return t
}

// share is part i of total split into n nearly equal parts.
func share(total, n, i int) int {
	s := total / n
	if i < total%n {
		s++
	}
	return s
}

// event is the made-up event: what is written on the ticket stubs and the
// basket cards, and what the server has to hold at the end.
type event struct {
	prefixes     []store.Prefix
	tickets      map[string]int // tickets per prefix, ids 1 to n
	baskets      map[string]int // baskets per prefix, ids 1 to n
	ticketSheets []sheet
	basketSheets []sheet
	stub         map[key]store.Ticket // a ticket as written on its stub
	card         map[key]store.Basket // a basket as written on its card
	winner       map[key]int          // the winning ticket drawn for each basket

	mu      sync.Mutex
	ticket  map[key]store.Ticket // every ticket as last saved
	tWriter map[key]int          // the client that saved it
	basket  map[key]store.Basket // every basket as last saved, with its winner
	bWriter map[key]int          // the client that saved the description
	wWriter map[key]int          // the client that saved the winner
}

func newEvent(o options) *event {
	rng := rand.New(rand.NewPCG(o.seed, 1))
	ev := &event{
		tickets: map[string]int{}, baskets: map[string]int{},
		stub: map[key]store.Ticket{}, card: map[key]store.Basket{}, winner: map[key]int{},
		ticket: map[key]store.Ticket{}, tWriter: map[key]int{},
		basket: map[key]store.Basket{}, bWriter: map[key]int{}, wWriter: map[key]int{},
	}
	for i := 0; i < o.prefixes; i++ {
		name := string(rune('A' + i))
		ev.prefixes = append(ev.prefixes, store.Prefix{Prefix: name, Color: store.Colors[(i+1)%len(store.Colors)], Weight: i + 1})
		ev.tickets[name] = share(o.tickets, o.prefixes, i)
		ev.baskets[name] = share(o.baskets, o.prefixes, i)
	}
	// People buy several tickets each, often of more than one prefix.
	buyers := make([]store.Ticket, max(1, o.tickets/6))
	for i := range buyers {
		buyers[i] = store.Ticket{FirstName: pick(rng, firstNames), LastName: pick(rng, lastNames),
			PhoneNumber: phoneNumber(rng), Pref: pick(rng, prefs)}
	}
	for _, p := range ev.prefixes {
		for id := 1; id <= ev.tickets[p.Prefix]; id++ {
			t := pick(rng, buyers)
			t.Prefix, t.TID = p.Prefix, id
			ev.stub[key{p.Prefix, id}] = t
		}
		for id := 1; id <= ev.baskets[p.Prefix]; id++ {
			ev.card[key{p.Prefix, id}] = store.Basket{Prefix: p.Prefix, BID: id, Description: pick(rng, items), Donors: pick(rng, donors)}
			ev.winner[key{p.Prefix, id}] = 1 + rng.IntN(ev.tickets[p.Prefix])
		}
		for from := 1; from <= ev.tickets[p.Prefix]; from += o.page {
			ev.ticketSheets = append(ev.ticketSheets, sheet{p.Prefix, from, min(from+o.page-1, ev.tickets[p.Prefix])})
		}
		for from := 1; from <= ev.baskets[p.Prefix]; from += o.page {
			ev.basketSheets = append(ev.basketSheets, sheet{p.Prefix, from, min(from+o.page-1, ev.baskets[p.Prefix])})
		}
	}
	return ev
}

// stubs returns the tickets of a sheet as written on their stubs.
func (ev *event) stubs(s sheet) []store.Ticket {
	out := make([]store.Ticket, 0, s.size())
	for id := s.from; id <= s.to; id++ {
		out = append(out, ev.stub[key{s.prefix, id}])
	}
	return out
}

// cards returns the baskets of a sheet as written on their cards.
func (ev *event) cards(s sheet) []store.Basket {
	out := make([]store.Basket, 0, s.size())
	for id := s.from; id <= s.to; id++ {
		out = append(out, ev.card[key{s.prefix, id}])
	}
	return out
}

// savedTickets notes tickets a client has saved.
func (ev *event) savedTickets(client int, ts []store.Ticket) {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	for _, t := range ts {
		k := key{t.Prefix, t.TID}
		ev.ticket[k], ev.tWriter[k] = t, client
	}
}

// savedCards notes baskets a client has saved from the baskets form, which
// sets the description and the donors.
func (ev *event) savedCards(client int, bs []store.Basket) {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	for _, b := range bs {
		k := key{b.Prefix, b.BID}
		cur := ev.basket[k]
		cur.Prefix, cur.BID, cur.Description, cur.Donors = b.Prefix, b.BID, b.Description, b.Donors
		ev.basket[k], ev.bWriter[k] = cur, client
	}
}

// savedWinners notes drawing lines a client has saved, which set the
// winning ticket.
func (ev *event) savedWinners(client int, ls []store.DrawingLine) {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	for _, l := range ls {
		k := key{l.Prefix, l.BID}
		cur := ev.basket[k]
		cur.Prefix, cur.BID, cur.WinningTicket = l.Prefix, l.BID, l.WinningTicket
		ev.basket[k], ev.wWriter[k] = cur, client
	}
}

// saved returns the tickets of a sheet as last saved.
func (ev *event) saved(s sheet) []store.Ticket {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	out := make([]store.Ticket, 0, s.size())
	for id := s.from; id <= s.to; id++ {
		out = append(out, ev.ticket[key{s.prefix, id}])
	}
	return out
}

// ticketNow returns a ticket as last saved and the client that saved it.
func (ev *event) ticketNow(k key) (store.Ticket, int, bool) {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	t, ok := ev.ticket[k]
	return t, ev.tWriter[k], ok
}

// stale counts the rows of an opened sheet that do not show what was last
// saved, and describes the first. Rows never saved are left alone: a blank
// row is right for them.
func (ev *event) stale(rows []store.Ticket) (int, string) { return ev.staleFor(rows, 0) }

// staleFor is stale for the rows the given client saved last; 0 is any.
func (ev *event) staleFor(rows []store.Ticket, client int) (int, string) {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	n, first := 0, ""
	for _, r := range rows {
		k := key{r.Prefix, r.TID}
		want, ok := ev.ticket[k]
		if !ok || r == want || client != 0 && ev.tWriter[k] != client {
			continue
		}
		n++
		if first == "" {
			first = fmt.Sprintf("%s %d showed %s, saved %s", r.Prefix, r.TID, describe(r), describe(want))
		}
	}
	return n, first
}

// describe shows the parts of a ticket a volunteer types.
func describe(t store.Ticket) string {
	if t.FirstName == "" && t.LastName == "" && t.PhoneNumber == "" {
		return "a blank row"
	}
	return fmt.Sprintf("%q", t.FirstName+" "+t.LastName+" "+t.PhoneNumber+" "+t.Pref)
}

// counts is the counts report the saved tickets call for, by prefix, with
// the total row "Total" for all of them.
func (ev *event) counts() map[string]store.ReportCountLine {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	out := map[string]store.ReportCountLine{}
	buyers := map[string]map[string]bool{}
	for k, t := range ev.ticket {
		for _, p := range []string{k.prefix, "Total"} {
			if buyers[p] == nil {
				buyers[p] = map[string]bool{}
			}
			// A buyer is a first name, last name and phone number together.
			buyers[p][t.FirstName+"\x00"+t.LastName+"\x00"+t.PhoneNumber] = true
			line := out[p]
			line.Prefix, line.TotalBuys, line.UniqueBuyers = p, line.TotalBuys+1, len(buyers[p])
			line.IsTotal = p == "Total"
			out[p] = line
		}
	}
	return out
}

// buyerOf returns the ticket that won a basket, as last saved.
func (ev *event) buyerOf(prefix string, winning int) store.Ticket {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	return ev.ticket[key{prefix, winning}]
}

// searchCount is how many saved tickets a search by last name finds. The
// server matches anywhere in the name, ignoring the case of ASCII letters
// only.
func (ev *event) searchCount(last string) int {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	n := 0
	for _, t := range ev.ticket {
		if containsLike(t.LastName, last) {
			n++
		}
	}
	return n
}

func containsLike(s, fragment string) bool {
	fold := func(s string) string {
		return strings.Map(func(r rune) rune {
			if 'A' <= r && r <= 'Z' {
				return r + 'a' - 'A'
			}
			return r
		}, s)
	}
	return strings.Contains(fold(s), fold(fragment))
}
