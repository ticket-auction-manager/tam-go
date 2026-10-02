package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
)

// switchServer stands in front of a real tam-server handler that a test can
// swap (a replacement server at the same address), make unwell (503 on
// saves) or put behind a Wi-Fi login page (200 and HTML on saves).
type switchServer struct {
	mu     sync.Mutex
	inner  http.Handler
	mode   string // "", "busy", "portal"
	ts     *httptest.Server
	stores []*store.Store
}

func newSwitchServer(t *testing.T) *switchServer {
	t.Helper()
	ss := &switchServer{}
	ss.replace(t)
	ss.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ss.mu.Lock()
		inner, mode := ss.inner, ss.mode
		ss.mu.Unlock()
		switch {
		case mode == "busy" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusServiceUnavailable)
		case mode == "portal" && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html><body>Sign in to the venue Wi-Fi</body></html>"))
		default:
			inner.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(ss.ts.Close)
	return ss
}

// replaceKeeping is replace with the client's access key copied into the
// new server's database, as when a server's data folder is swapped for
// another that holds the key: the client does not pair again.
func (ss *switchServer) replaceKeeping(t *testing.T, key string) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "remote.db")
	sqldb, err := db.Open(path)
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
	if _, err := sqldb.Exec(`INSERT INTO auth_keys (auth_key, description) VALUES (?, 'client')`, key); err != nil {
		t.Fatal(err)
	}
	st := store.New(sqldb)
	ss.mu.Lock()
	ss.inner = server.NewHandler(st, server.FixedPassword("secret"), server.WithInfo(server.Info{Name: "front-desk"}))
	ss.stores = append(ss.stores, st)
	ss.mu.Unlock()
	return st
}

// replace puts a new server with a database of its own behind the address:
// a new event, or a server set up again after its disk was lost.
func (ss *switchServer) replace(t *testing.T) *store.Store {
	t.Helper()
	st := newServerStore(t)
	ss.mu.Lock()
	ss.inner = server.NewHandler(st, server.FixedPassword("secret"), server.WithInfo(server.Info{Name: "front-desk"}))
	ss.stores = append(ss.stores, st)
	ss.mu.Unlock()
	return st
}

func (ss *switchServer) set(mode string) {
	ss.mu.Lock()
	ss.mode = mode
	ss.mu.Unlock()
}

// st is the database of the server behind the address now.
func (ss *switchServer) st() *store.Store {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.stores[len(ss.stores)-1]
}

// loadedAnn is ticket A 1 as every computer loaded it.
var loadedAnn = store.TicketValues{FirstName: "Ann", LastName: "Lee", PhoneNumber: "555-0001", Pref: "CALL"}

// ticketSaved is ticket A 1 as the tickets form saves it: the values on the
// page and the values the volunteer started from.
func ticketSaved(v store.TicketValues, base *store.TicketValues) []store.TicketSave {
	return []store.TicketSave{{Ticket: store.Ticket{Prefix: "A", TID: 1, FirstName: v.FirstName, LastName: v.LastName, PhoneNumber: v.PhoneNumber, Pref: v.Pref}, Base: base}}
}

// pairedWithAnn pairs a new client with a new server and enters ticket A 1
// through it.
func pairedWithAnn(t *testing.T) (*fixture, *switchServer) {
	t.Helper()
	f := newFixture(t)
	ss := newSwitchServer(t)
	f.pairTo(ss.ts.URL)
	f.h.sync.Tick()
	if code, body := f.do("POST", "/api/tickets", ticketSaved(loadedAnn, nil), nil); code != 200 {
		t.Fatalf("enter ticket A 1 = %d %s", code, body)
	}
	return f, ss
}

func phoneOf(t *testing.T, st *store.Store) string {
	t.Helper()
	tk, err := st.Ticket("A", 1)
	if err != nil || tk == nil {
		t.Fatalf("ticket A 1 = %+v, %v", tk, err)
	}
	return tk.PhoneNumber
}

// A save made on a client while it was cut off from the server, and sent
// after another computer changed the same field, does not overwrite the
// newer value: the newer value stays on the server and comes into the
// client's copy, the late change waits in the failed list with both values,
// and Retry applies it when the volunteer decides so.
func TestALateQueuedSaveDoesNotOverwriteANewerValue(t *testing.T) {
	f, ss := pairedWithAnn(t)
	ss.set("busy")
	mine := loadedAnn
	mine.PhoneNumber = "555-0777"
	if code, body := f.do("POST", "/api/tickets", ticketSaved(mine, &loadedAnn), nil); code != 200 {
		t.Fatalf("save while the server is unwell = %d %s", code, body)
	}
	if p, _ := pending(t, f.st); p != 1 {
		t.Fatalf("pending = %d, want the save queued", p)
	}

	// Another computer corrects the same phone number meanwhile.
	newer := loadedAnn
	newer.PhoneNumber = "555-0888"
	if _, err := ss.st().SaveTickets(ticketSaved(newer, &loadedAnn)); err != nil {
		t.Fatal(err)
	}

	ss.set("")
	f.h.sync.Reset()
	f.h.sync.Tick()
	if got := phoneOf(t, ss.st()); got != "555-0888" {
		t.Fatalf("the late save changed the server's phone to %q", got)
	}
	if got := phoneOf(t, f.st); got != "555-0888" {
		t.Fatalf("the client's copy has %q, want the server's newer 555-0888", got)
	}
	if p, fl := pending(t, f.st); p != 0 || fl != 1 {
		t.Fatalf("pending %d failed %d, want the refused change in the failed list", p, fl)
	}
	code, body := f.do("GET", "/api/outbox/failed", nil, nil)
	if code != 200 || !strings.Contains(string(body), "another computer changed it") || !strings.Contains(string(body), "555-0777") || !strings.Contains(string(body), "555-0888") {
		t.Fatalf("failed list = %d %s", code, body)
	}

	if code, body := f.do("POST", "/api/outbox/retry", `{}`, nil); code != 200 {
		t.Fatalf("retry = %d %s", code, body)
	}
	f.h.sync.Tick()
	if got := phoneOf(t, ss.st()); got != "555-0777" {
		t.Fatalf("after Retry the server has %q, want the volunteer's 555-0777", got)
	}
	if p, fl := pending(t, f.st); p != 0 || fl != 0 {
		t.Fatalf("after Retry: pending %d failed %d", p, fl)
	}
}

// A page that saves a change over a value another computer changed since
// the page loaded gets the newer value back, and the client's copy holds it.
func TestAnOnlineSaveOverANewerValueShowsTheNewerValue(t *testing.T) {
	f, ss := pairedWithAnn(t)
	newer := loadedAnn
	newer.PhoneNumber = "555-0888"
	if _, err := ss.st().SaveTickets(ticketSaved(newer, &loadedAnn)); err != nil {
		t.Fatal(err)
	}
	mine := loadedAnn
	mine.PhoneNumber, mine.LastName = "555-0777", "Leigh"
	code, body := f.do("POST", "/api/tickets", ticketSaved(mine, &loadedAnn), nil)
	if code != 200 {
		t.Fatalf("save = %d %s", code, body)
	}
	answer := decode[[]store.Ticket](t, body)
	if len(answer) != 1 || answer[0].PhoneNumber != "555-0888" || answer[0].LastName != "Leigh" {
		t.Fatalf("answer = %+v, want the newer phone kept and the last name changed", answer)
	}
	if got := phoneOf(t, f.st); got != "555-0888" {
		t.Fatalf("the client's copy has %q", got)
	}
	if p, fl := pending(t, f.st); p != 0 || fl != 0 {
		t.Fatalf("pending %d failed %d after an online save", p, fl)
	}
}

// A Wi-Fi login page answers anything with 200. It must not take a save:
// the save stays queued and reaches the server once the way is clear.
func TestALoginPageCannotTakeASave(t *testing.T) {
	f, ss := pairedWithAnn(t)
	ss.set("portal")
	mine := loadedAnn
	mine.PhoneNumber = "555-0777"
	code, body := f.do("POST", "/api/tickets", ticketSaved(mine, &loadedAnn), nil)
	if code != 200 {
		t.Fatalf("save behind the login page = %d %s", code, body)
	}
	if p, _ := pending(t, f.st); p != 1 {
		t.Fatalf("pending = %d, want the save queued rather than taken by the login page", p)
	}
	f.h.sync.Reset()
	f.h.sync.Tick()
	if p, _ := pending(t, f.st); p != 1 {
		t.Fatalf("pending = %d after a replay into the login page, want it still queued", p)
	}
	ss.set("")
	f.h.sync.Reset()
	f.h.sync.Tick()
	if got := phoneOf(t, ss.st()); got != "555-0777" {
		t.Fatalf("after the login page was gone the server has %q", got)
	}
	if p, fl := pending(t, f.st); p != 0 || fl != 0 {
		t.Fatalf("pending %d failed %d", p, fl)
	}
}

// A server set up again at the same address, holding another event, gets
// nothing of the client's earlier event unasked: the client keeps its copy
// in a file, sets its waiting saves aside and takes the new event's data.
func TestAServerOfAnotherEventGetsNothingUnasked(t *testing.T) {
	f, ss := pairedWithAnn(t)
	ss.set("busy")
	if code, body := f.do("POST", "/api/tickets", []store.TicketSave{{Ticket: store.Ticket{Prefix: "A", TID: 2, FirstName: "Queued", Pref: "CALL"}}}, nil); code != 200 {
		t.Fatalf("queued save = %d %s", code, body)
	}

	fresh := ss.replace(t)
	if _, err := fresh.SaveTickets([]store.TicketSave{{Ticket: store.Ticket{Prefix: "B", TID: 1, FirstName: "New", Pref: "CALL"}}}); err != nil {
		t.Fatal(err)
	}
	ss.set("")
	f.pairTo(ss.ts.URL)
	f.h.sync.Reset()
	f.h.sync.Tick()

	if got, _ := fresh.Ticket("A", 1); got != nil {
		t.Fatalf("the new event's server got the earlier event's ticket %+v", got)
	}
	if got, _ := fresh.Ticket("A", 2); got != nil {
		t.Fatalf("the new event's server got the earlier event's queued save %+v", got)
	}
	if got, _ := f.st.Ticket("A", 1); got != nil {
		t.Fatalf("the client's copy still holds the earlier event's ticket %+v", got)
	}
	if got, _ := f.st.Ticket("B", 1); got == nil || got.FirstName != "New" {
		t.Fatalf("the client's copy lacks the new event's ticket: %+v", got)
	}
	if p, fl := pending(t, f.st); p != 0 || fl != 1 {
		t.Fatalf("pending %d failed %d, want the earlier event's save set aside", p, fl)
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(f.settings), "before-*.json"))
	if len(files) == 0 {
		t.Fatal("the earlier event's copy was not kept in a file")
	}
	data, err := os.ReadFile(files[0])
	if err != nil || !strings.Contains(string(data), `"Ann"`) {
		t.Fatalf("the kept file %s holds %s (%v)", files[0], data, err)
	}
}

// The same, when the server changes event under the client without a new
// pairing (its data folder swapped for one that holds the client's key):
// the client sees the other event at the next heartbeat.
func TestAnEventChangeWithoutPairingSetsTheCopyAside(t *testing.T) {
	f, ss := pairedWithAnn(t)
	ss.set("busy")
	if code, body := f.do("POST", "/api/tickets", []store.TicketSave{{Ticket: store.Ticket{Prefix: "A", TID: 2, FirstName: "Queued", Pref: "CALL"}}}, nil); code != 200 {
		t.Fatalf("queued save = %d %s", code, body)
	}
	fresh := ss.replaceKeeping(t, f.h.settings().RemoteKey)
	if _, err := fresh.SaveTickets([]store.TicketSave{{Ticket: store.Ticket{Prefix: "B", TID: 1, FirstName: "New", Pref: "CALL"}}}); err != nil {
		t.Fatal(err)
	}
	ss.set("")
	f.h.sync.Reset()
	f.h.sync.Tick()
	f.h.sync.Tick()
	if got, _ := fresh.Ticket("A", 2); got != nil {
		t.Fatalf("the other event's server got the queued save %+v", got)
	}
	if got, _ := f.st.Ticket("A", 1); got != nil {
		t.Fatalf("the client's copy still holds the earlier event's ticket %+v", got)
	}
	if got, _ := f.st.Ticket("B", 1); got == nil {
		t.Fatal("the client did not pull the new event's data")
	}
	if p, fl := pending(t, f.st); p != 0 || fl != 1 {
		t.Fatalf("pending %d failed %d, want the queued save set aside", p, fl)
	}
	ev, _ := fresh.Event()
	if mine, _ := f.st.MirrorEvent(); mine != ev.Event {
		t.Fatalf("the client's copy belongs to %q, the server holds %q", mine, ev.Event)
	}
	if files, _ := filepath.Glob(filepath.Join(filepath.Dir(f.settings), "before-event-*.json")); len(files) != 1 {
		t.Fatalf("kept files = %v, want one before-event file", files)
	}
}

// Push sends this client's copy, of which the server keeps only what is
// newer than its own; it waits while saves are still queued.
func TestPushKeepsTheServersNewerValues(t *testing.T) {
	f, ss := pairedWithAnn(t)
	f.h.sync.Tick() // pull: the copy holds the server's order numbers
	newer := loadedAnn
	newer.PhoneNumber = "555-0888"
	if _, err := ss.st().SaveTickets(ticketSaved(newer, &loadedAnn)); err != nil {
		t.Fatal(err)
	}
	code, body := f.do("POST", "/api/backuprestore/push/tickets", `{}`, nil)
	if code != 200 || !strings.Contains(string(body), "stayed") {
		t.Fatalf("push = %d %s", code, body)
	}
	if got := phoneOf(t, ss.st()); got != "555-0888" {
		t.Fatalf("the push changed the server's newer phone back to %q", got)
	}

	ss.set("busy")
	if code, body := f.do("POST", "/api/tickets", []store.TicketSave{{Ticket: store.Ticket{Prefix: "A", TID: 3, FirstName: "Queued", Pref: "CALL"}}}, nil); code != 200 {
		t.Fatalf("queued save = %d %s", code, body)
	}
	ss.set("")
	code, body = f.do("POST", "/api/backuprestore/push/tickets", `{}`, nil)
	var doc map[string]string
	json.Unmarshal(body, &doc)
	if code != 409 || !strings.Contains(doc["detail"], "waiting") {
		t.Fatalf("push with a save queued = %d %s, want 409", code, body)
	}
}

// A report answered from the client's copy while the server is away, or
// while saves still wait for it, says so (X-TAM-Copy), so the page can warn
// that other computers' saves may be missing; one from the server does not.
func TestReportsFromTheCopySaySo(t *testing.T) {
	f, ss := pairedWithAnn(t)
	copyHeader := func() string {
		t.Helper()
		req, _ := http.NewRequest("GET", f.url+"/api/reports/counts", nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.Header.Get("X-TAM-Copy")
	}
	if got := copyHeader(); got != "" {
		t.Fatalf("a report from the server says X-TAM-Copy %q", got)
	}
	ss.set("busy")
	if code, body := f.do("POST", "/api/tickets", []store.TicketSave{{Ticket: store.Ticket{Prefix: "A", TID: 2, FirstName: "Queued", Pref: "CALL"}}}, nil); code != 200 {
		t.Fatalf("queued save = %d %s", code, body)
	}
	if got := copyHeader(); got != "1" {
		t.Fatalf("a report while a save waits says X-TAM-Copy %q, want 1", got)
	}
}

// Two saves of one ticket made while the server was away replay in order.
// The server's answer to the first must not put its older value back into
// the client's copy while the second is still on its way: a sheet opened
// meanwhile shows the later value.
func TestAReplayedAnswerDoesNotUndoALaterQueuedSave(t *testing.T) {
	f := newFixture(t)
	rst := newServerStore(t)
	inner := server.NewHandler(rst, server.FixedPassword("secret"), server.WithInfo(server.Info{Name: "front-desk"}))
	var (
		mu       sync.Mutex
		busy     bool
		posts    int
		held     = make(chan struct{})
		release  = make(chan struct{})
		holdOnce sync.Once
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		isBusy := busy
		if r.Method == http.MethodPost && r.URL.Path == "/api/tickets" && !isBusy {
			posts++
			if posts == 2 {
				mu.Unlock()
				holdOnce.Do(func() { close(held) })
				<-release
				inner.ServeHTTP(w, r)
				return
			}
		}
		mu.Unlock()
		if isBusy && r.Method == http.MethodPost {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	f.pairTo(ts.URL)
	f.h.sync.Tick()
	mu.Lock()
	busy = true
	mu.Unlock()

	first := loadedAnn
	first.PhoneNumber = "555-0101"
	if code, body := f.do("POST", "/api/tickets", ticketSaved(first, nil), nil); code != 200 {
		t.Fatalf("first save = %d %s", code, body)
	}
	second := first
	second.PhoneNumber = "555-0202"
	if code, body := f.do("POST", "/api/tickets", ticketSaved(second, &first), nil); code != 200 {
		t.Fatalf("second save = %d %s", code, body)
	}
	mu.Lock()
	busy = false
	mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		f.h.sync.Reset()
		f.h.sync.Tick()
	}()
	<-held // the first save was answered; the second is on its way
	if got := phoneOf(t, f.st); got != "555-0202" {
		t.Errorf("with the second save still on its way the client's copy shows %q, want the later 555-0202", got)
	}
	close(release)
	<-done
	if got := phoneOf(t, rst); got != "555-0202" {
		t.Fatalf("the server ended with %q", got)
	}
	if got := phoneOf(t, f.st); got != "555-0202" {
		t.Fatalf("the client's copy ended with %q", got)
	}
}

// A copy is emptied only once it is in a file: when the file cannot be
// written (a full disk, a folder gone), the client changes nothing, keeps
// its queue and its copy, and tries again; once the file is written the
// change goes ahead.
func TestAnEventChangeKeepsTheCopyWhenItCannotBeSaved(t *testing.T) {
	f, ss := pairedWithAnn(t)
	ss.set("busy")
	if code, body := f.do("POST", "/api/tickets", []store.TicketSave{{Ticket: store.Ticket{Prefix: "A", TID: 2, FirstName: "Queued", Pref: "CALL"}}}, nil); code != 200 {
		t.Fatalf("queued save = %d %s", code, body)
	}
	before, err := f.st.MirrorEvent()
	if err != nil || before == "" {
		t.Fatalf("the copy's event = %q, %v", before, err)
	}
	fresh := ss.replaceKeeping(t, f.h.settings().RemoteKey)
	ss.set("")
	dataDir := f.h.dataDir
	f.h.dataDir = filepath.Join(dataDir, "gone", "away")
	f.h.sync.Reset()
	f.h.sync.Tick()
	f.h.sync.Tick()
	if got, _ := f.st.Ticket("A", 1); got == nil {
		t.Fatal("the copy was emptied although it could not be saved to a file")
	}
	if p, fl := pending(t, f.st); p != 1 || fl != 0 {
		t.Fatalf("pending %d failed %d, want the queue left as it was", p, fl)
	}
	if mine, _ := f.st.MirrorEvent(); mine != before {
		t.Fatalf("the copy's event changed to %q without its file", mine)
	}
	if got, _ := fresh.Ticket("A", 2); got != nil {
		t.Fatalf("the other event's server got the queued save %+v", got)
	}

	f.h.dataDir = dataDir
	f.h.sync.Tick()
	f.h.sync.Tick()
	if got, _ := f.st.Ticket("A", 1); got != nil {
		t.Fatalf("once the file could be written the copy still holds %+v", got)
	}
	if files, _ := filepath.Glob(filepath.Join(dataDir, "before-event-*.json")); len(files) != 1 {
		t.Fatalf("kept files = %v", files)
	}
}

// Pairing with a server of another event empties this client's copy, so it
// does not pair while the copy cannot be saved to a file first.
func TestPairingWithAnotherEventKeepsTheCopyWhenItCannotBeSaved(t *testing.T) {
	f, ss := pairedWithAnn(t)
	f.h.sync.Tick()
	ss.replace(t)
	dataDir := f.h.dataDir
	f.h.dataDir = filepath.Join(dataDir, "gone", "away")
	code, body := f.do("POST", "/api/pair", map[string]any{"host": "127.0.0.1", "port": strings.TrimPrefix(ss.ts.URL, "http://127.0.0.1:"), "password": "secret"}, nil)
	if code != 500 || !strings.Contains(string(body), "did not pair") {
		t.Fatalf("pairing without a place for the file = %d %s, want 500", code, body)
	}
	if got, _ := f.st.Ticket("A", 1); got == nil {
		t.Fatal("the copy was emptied although it could not be saved to a file")
	}
	keys, err := ss.st().ListKeys()
	if err != nil || len(keys) != 0 {
		t.Fatalf("the refused pairing left keys on the server: %+v (%v)", keys, err)
	}
	f.h.dataDir = dataDir
	f.pairTo(ss.ts.URL)
	if got, _ := f.st.Ticket("A", 1); got != nil {
		t.Fatalf("after pairing with the file written the copy still holds %+v", got)
	}
}
