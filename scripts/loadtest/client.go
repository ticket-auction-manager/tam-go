package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"ticket-auction-manager/tam-go/internal/store"
)

// client is one venue client: a tam-client, its Wi-Fi (the relay it
// reaches the server through) and the volunteer in front of its browser.
type client struct {
	n     int
	prog  *program
	relay *relay

	rngMu sync.Mutex
	rng   *rand.Rand // behind rngMu: the entry loop and the catch-up goroutine both draw

	// act is held for every request: one volunteer works one click at a
	// time. A crash takes it, so the client dies between two clicks.
	act sync.Mutex

	mu          sync.Mutex
	offline     []sheet     // sheets whose save was queued, once entry has moved past them
	queuedAt    []time.Time // when a save was answered as queued
	reconnected time.Time   // when it saw the server again after the outage
	backlog     int         // saves it still had queued at that moment
	away        []window    // when its saves may rightly be queued
	outage      int         // the index in away of the server outage's window

	// dropNow asks the entry loop to take this client's Wi-Fi down after it
	// opens its next sheet; dropped is closed when it has.
	dropNow atomic.Bool
	dropped chan struct{}
}

// window is a stretch of time; to is zero while it lasts.
type window struct{ from, to time.Time }

// openAway notes that from now on this client's saves may rightly be
// queued, until closeAway with the index it returns.
func (l *client) openAway(from time.Time) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.away = append(l.away, window{from: from})
	return len(l.away) - 1
}

func (l *client) closeAway(i int, to time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.away[i].to = to
}

// queuedRightly reports whether a save queued at at fell in a window.
// Callers hold l.mu.
func (l *client) queuedRightly(at time.Time) bool {
	for _, w := range l.away {
		if !at.Before(w.from) && (w.to.IsZero() || at.Before(w.to)) {
			return true
		}
	}
	return false
}

// intN, fix and lastName draw from the client's random source.
func (l *client) intN(n int) int {
	l.rngMu.Lock()
	defer l.rngMu.Unlock()
	return l.rng.IntN(n)
}

func (l *client) fix(t store.Ticket) store.Ticket {
	l.rngMu.Lock()
	defer l.rngMu.Unlock()
	return corrected(t, l.rng)
}

func (l *client) lastName() string {
	l.rngMu.Lock()
	defer l.rngMu.Unlock()
	return pick(l.rng, lastNames)
}

// pairing is what the Settings page sends to pair this client with the
// server, through its Wi-Fi.
func (l *client) pairing(t *test) map[string]any {
	return map[string]any{"host": "127.0.0.1", "port": l.relay.port(), "tls": t.tls(), "password": t.password}
}

// clientStatus is what GET /api/status answers, the status bar's source.
type clientStatus struct {
	Mode    string `json:"mode"`
	State   string `json:"state"`
	Pending int    `json:"pending"`
	Failed  int    `json:"failed"`
}

// call sends one request to the client's tam-client, as its pages do, and
// records it in ph under op (not at all when ph is nil). into, when not
// nil, receives the JSON answer. It reports whether a save was queued.
func (l *client) call(ph *phase, op, method, path string, body, into any, rows int) (queued bool, err error) {
	l.act.Lock()
	defer l.act.Unlock()
	return l.request(ph, op, method, path, body, into, rows)
}

// request is call for a caller that holds act already.
func (l *client) request(ph *phase, op, method, path string, body, into any, rows int) (queued bool, err error) {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return false, err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, l.prog.url+path, rd)
	if err != nil {
		return false, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	res, err := web.Do(req)
	var data []byte
	if err == nil {
		data, err = io.ReadAll(res.Body)
		res.Body.Close()
		if err == nil && res.StatusCode != http.StatusOK {
			err = fmt.Errorf("%s answered %d to %s %s: %s", l.prog.name, res.StatusCode, method, path, bytes.TrimSpace(data))
		}
		queued = err == nil && res.Header.Get("X-TAM-Queued") != ""
	}
	took := time.Since(start)
	if err == nil && into != nil {
		if jerr := json.Unmarshal(data, into); jerr != nil {
			err = fmt.Errorf("%s: %s %s: %w", l.prog.name, method, path, jerr)
		}
		valuesOnly(into)
	}
	if queued {
		l.mu.Lock()
		l.queuedAt = append(l.queuedAt, start)
		l.mu.Unlock()
	}
	if ph != nil {
		ph.rec.add(op, took, err, rows, queued)
	}
	return queued, err
}

// peek reads the client's status without recording it.
func (l *client) peek() (clientStatus, error) {
	var st clientStatus
	_, err := l.call(nil, "", http.MethodGet, "/api/status", nil, &st, 0)
	return st, err
}

// statusBar polls the status every three seconds, as the bar on every page
// does, until stop is closed.
func (l *client) statusBar(t *test, stop <-chan struct{}) {
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			var st clientStatus
			l.call(t.current(), "status bar", http.MethodGet, "/api/status", nil, &st, 0)
		}
	}
}

// enterTickets works through the client's ticket sheets, one every pace:
// open the sheet, type it, save it; now and then fix a typo at once or
// open the sheet again to look at it.
func (l *client) enterTickets(t *test, ph *phase, sheets []sheet, pace time.Duration, saved *atomic.Int64) {
	next := time.Now()
	for _, s := range sheets {
		var shown []store.Ticket
		if _, err := l.call(ph, "open ticket sheet", http.MethodGet, s.path("tickets"), nil, &shown, 0); err == nil && len(shown) != s.size() {
			t.problems.add("sheet size", 1, "%s: ticket sheet %s %d-%d showed %d rows", l.prog.name, s.prefix, s.from, s.to, len(shown))
		}
		// A client whose Wi-Fi is to drop loses it now, with the sheet typed
		// and not saved yet: the save then hangs on the lost link.
		if l.dropNow.CompareAndSwap(true, false) {
			l.relay.setDown(true)
			close(l.dropped)
		}
		rows := t.ev.stubs(s)
		began := time.Now()
		queued, err := l.call(ph, "save ticket sheet", http.MethodPost, "/api/tickets", rows, nil, len(rows))
		slow := time.Since(began) > 4*time.Second
		saved.Add(1)
		if err == nil {
			t.ev.savedTickets(l.n, rows)
		}
		// A save that hung before coming back queued looks as if it failed,
		// so the volunteer types a row of it again. The first attempt may
		// still be on its way, held up in the network.
		if queued && slow {
			t.stalled.Add(1)
			again := []store.Ticket{l.fix(rows[l.intN(len(rows))])}
			if _, err := l.call(ph, "type a row again after a slow save", http.MethodPost, "/api/tickets", again, nil, 1); err == nil {
				t.ev.savedTickets(l.n, again)
			}
		}
		if l.intN(6) == 0 {
			fix := []store.Ticket{l.fix(rows[l.intN(len(rows))])}
			if _, err := l.call(ph, "fix a typo", http.MethodPost, "/api/tickets", fix, nil, 1); err == nil {
				t.ev.savedTickets(l.n, fix)
			}
		}
		if l.intN(4) == 0 {
			l.look(t, ph, "open ticket sheet", s)
		}
		if queued {
			l.mu.Lock()
			l.offline = append(l.offline, s)
			l.mu.Unlock()
		}
		next = next.Add(pace)
		time.Sleep(time.Until(next))
	}
}

// look opens a sheet and notes every row that does not show what was last
// saved.
func (l *client) look(t *test, ph *phase, op string, s sheet) {
	var shown []store.Ticket
	if _, err := l.call(ph, op, http.MethodGet, s.path("tickets"), nil, &shown, 0); err != nil {
		return
	}
	if n, first := t.ev.stale(shown); n > 0 {
		t.problems.add("stale sheet", n, "%s: %s", l.prog.name, first)
	}
}

// revisit waits for the server to come back, then goes back to the sheets
// this client saved while it was away, the moment the client shows it is
// connected again: open the sheet, correct a row, save.
func (l *client) revisit(t *test, ph *phase, back <-chan struct{}) {
	<-back
	deadline := time.Now().Add(t.o.settle)
	for {
		st, err := l.peek()
		if err == nil && st.State == "connected" {
			l.mu.Lock()
			l.reconnected, l.backlog = time.Now(), st.Pending
			l.mu.Unlock()
			break
		}
		if time.Now().After(deadline) {
			t.problems.add("reconnect", 1, "%s did not show the server as connected within %s of its restart", l.prog.name, t.o.settle)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	l.mu.Lock()
	sheets := append([]sheet(nil), l.offline...)
	l.mu.Unlock()
	for _, s := range sheets[:min(len(sheets), 5)] {
		l.look(t, ph, "open a sheet saved offline", s)
		k := key{s.prefix, s.from + l.intN(s.size())}
		cur, _, ok := t.ev.ticketNow(k)
		if !ok {
			continue
		}
		fix := []store.Ticket{l.fix(cur)}
		if _, err := l.call(ph, "correct a sheet saved offline", http.MethodPost, "/api/tickets", fix, nil, 1); err == nil {
			t.ev.savedTickets(l.n, fix)
		}
	}
}

// crash kills the client's tam-client between two clicks, like a flat
// battery, leaves it off for down and starts it again. It returns the
// saves it had queued before and after.
func (l *client) crash(down time.Duration) (before, after int, err error) {
	l.act.Lock()
	defer l.act.Unlock()
	var st clientStatus
	if _, err := l.request(nil, "", http.MethodGet, "/api/status", nil, &st, 0); err != nil {
		return 0, 0, err
	}
	before = st.Pending
	l.prog.kill()
	time.Sleep(down)
	if err := l.prog.start(); err != nil {
		return before, 0, err
	}
	st = clientStatus{}
	if _, err := l.request(nil, "", http.MethodGet, "/api/status", nil, &st, 0); err != nil {
		return before, 0, err
	}
	return before, st.Pending, nil
}

// saveInBatches saves tickets a sheet at a time.
func (l *client) saveInBatches(t *test, ph *phase, op string, ts []store.Ticket) {
	for len(ts) > 0 {
		n := min(len(ts), t.o.page)
		if _, err := l.call(ph, op, http.MethodPost, "/api/tickets", ts[:n], nil, n); err == nil {
			t.ev.savedTickets(l.n, ts[:n])
		}
		ts = ts[n:]
	}
}

// enterBaskets opens and saves the client's basket sheets as fast as it can.
func (l *client) enterBaskets(t *test, ph *phase, sheets []sheet) {
	for _, s := range sheets {
		var shown []store.Basket
		if _, err := l.call(ph, "open basket sheet", http.MethodGet, s.path("baskets"), nil, &shown, 0); err == nil && len(shown) != s.size() {
			t.problems.add("sheet size", 1, "%s: basket sheet %s %d-%d showed %d rows", l.prog.name, s.prefix, s.from, s.to, len(shown))
		}
		cards := t.ev.cards(s)
		if _, err := l.call(ph, "save basket sheet", http.MethodPost, "/api/baskets", cards, nil, len(cards)); err == nil {
			t.ev.savedCards(l.n, cards)
		}
	}
}

// draw opens the client's drawing sheets, types each winning ticket (the
// page looks the ticket up to show who won) and saves the sheet.
func (l *client) draw(t *test, ph *phase, sheets []sheet) {
	for _, s := range sheets {
		var lines []store.DrawingLine
		if _, err := l.call(ph, "open drawing sheet", http.MethodGet, s.path("drawing"), nil, &lines, 0); err != nil {
			continue
		}
		if len(lines) != s.size() {
			t.problems.add("sheet size", 1, "%s: drawing sheet %s %d-%d showed %d rows", l.prog.name, s.prefix, s.from, s.to, len(lines))
			continue
		}
		for i := range lines {
			k := key{lines[i].Prefix, lines[i].BID}
			lines[i].WinningTicket = t.ev.winner[k]
			var who store.Ticket
			path := fmt.Sprintf("/api/tickets/%s/%d", url.PathEscape(k.prefix), lines[i].WinningTicket)
			if _, err := l.call(ph, "look up the winner", http.MethodGet, path, nil, &who, 0); err == nil {
				if want := t.ev.buyerOf(k.prefix, lines[i].WinningTicket); who != want {
					t.problems.add("report", 1, "%s: winner lookup %s %d showed %s, saved %s", l.prog.name, k.prefix, lines[i].WinningTicket, describe(who), describe(want))
				}
			}
		}
		if _, err := l.call(ph, "save drawing sheet", http.MethodPost, "/api/drawing", lines, nil, len(lines)); err == nil {
			t.ev.savedWinners(l.n, lines)
		}
	}
}

// readReports reads what the office reads at the end: the counts, both
// winners reports and the drawing results of every prefix, and a few
// searches, and compares each with the data.
func (l *client) readReports(t *test, ph *phase) {
	var counts []store.ReportCountLine
	if _, err := l.call(ph, "counts report", http.MethodGet, "/api/reports/counts", nil, &counts, 0); err == nil {
		want := t.ev.counts()
		got := map[string]store.ReportCountLine{}
		for _, c := range counts {
			got[c.Prefix] = c
		}
		if len(got) != len(want) {
			t.problems.add("report", 1, "%s: the counts report has %d lines, the data %d", l.prog.name, len(got), len(want))
		}
		for p, w := range want {
			if got[p] != w {
				t.problems.add("report", 1, "%s: counts for %s are %+v, the data says %+v", l.prog.name, p, got[p], w)
			}
		}
	}
	for _, p := range t.ev.prefixes {
		name := url.PathEscape(p.Prefix)
		var byBasket []store.ReportByBasketLine
		if _, err := l.call(ph, "report by basket", http.MethodGet, "/api/reports/bybasket/"+name, nil, &byBasket, 0); err == nil {
			l.compareWinners(t, "report by basket "+p.Prefix, len(byBasket), t.ev.baskets[p.Prefix], func(i int) (string, int, store.Ticket) {
				r := byBasket[i]
				return r.Prefix, r.WinningTicket, store.Ticket{Prefix: r.Prefix, TID: r.WinningTicket, FirstName: r.FirstName, LastName: r.LastName, PhoneNumber: r.PhoneNumber, Pref: r.Pref}
			})
		}
		var byName []store.ReportByNameLine
		if _, err := l.call(ph, "report by name", http.MethodGet, "/api/reports/byname/"+name, nil, &byName, 0); err == nil {
			l.compareWinners(t, "report by name "+p.Prefix, len(byName), t.ev.baskets[p.Prefix], func(i int) (string, int, store.Ticket) {
				r := byName[i]
				return r.Prefix, r.WinningTicket, store.Ticket{Prefix: r.Prefix, TID: r.WinningTicket, FirstName: r.FirstName, LastName: r.LastName, PhoneNumber: r.PhoneNumber, Pref: r.Pref}
			})
		}
		var results []store.DrawingLine
		if _, err := l.call(ph, "drawing results", http.MethodGet, "/api/drawing/"+name, nil, &results, 0); err == nil {
			l.compareWinners(t, "drawing results "+p.Prefix, len(results), t.ev.baskets[p.Prefix], func(i int) (string, int, store.Ticket) {
				r := results[i]
				want := t.ev.buyerOf(r.Prefix, r.WinningTicket)
				return r.Prefix, r.WinningTicket, store.Ticket{Prefix: r.Prefix, TID: r.WinningTicket, FirstName: r.FirstName, LastName: r.LastName, PhoneNumber: r.PhoneNumber, Pref: want.Pref}
			})
		}
	}
	for i := 0; i < 3; i++ {
		last := l.lastName()
		var found []store.Ticket
		q := url.Values{"first_name": {""}, "last_name": {last}, "phone_number": {""}}
		if _, err := l.call(ph, "search by last name", http.MethodGet, "/api/search/tickets?"+q.Encode(), nil, &found, 0); err == nil {
			if want := t.ev.searchCount(last); len(found) != want {
				t.problems.add("report", 1, "%s: searching %q found %d tickets, the data has %d", l.prog.name, last, len(found), want)
			}
		}
	}
}

// compareWinners checks the lines of a winners report: one per basket, each
// naming the buyer of its winning ticket as last saved.
func (l *client) compareWinners(t *test, what string, n, want int, line func(i int) (prefix string, winning int, shown store.Ticket)) {
	if n != want {
		t.problems.add("report", 1, "%s: %s has %d lines, the prefix %d baskets", l.prog.name, what, n, want)
		return
	}
	for i := 0; i < n; i++ {
		prefix, winning, shown := line(i)
		if buyer := t.ev.buyerOf(prefix, winning); shown != buyer {
			t.problems.add("report", 1, "%s: %s names %s for ticket %d, saved %s", l.prog.name, what, describe(shown), winning, describe(buyer))
		}
	}
}
