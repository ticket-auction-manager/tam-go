package sync

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/remote"
	"ticket-auction-manager/tam-go/internal/store"
	"ticket-auction-manager/tam-go/internal/version"
)

// fakeServer is a tam-server stand-in whose mood can be changed mid-test.
type fakeServer struct {
	mu         sync.Mutex
	mode       string // "up", "down" (503), "nokey" (401 on everything)
	requests   []string
	heartbeats []http.Header // the headers of every GET /api
	ts         *httptest.Server
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{mode: "up"}
	f.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		mode := f.mode
		f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
		if r.URL.Path == "/api" {
			f.heartbeats = append(f.heartbeats, r.Header.Clone())
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case mode == "down":
			w.WriteHeader(503)
			w.Write([]byte(`{"detail":"down"}`))
		case mode == "nokey" && r.URL.Path != "/api":
			w.WriteHeader(401)
			w.Write([]byte(`{"detail":"Invalid Key"}`))
		case r.URL.Path == "/api":
			authed := mode != "nokey"
			json.NewEncoder(w).Encode(map[string]any{"whoami": "TAM Server", "authenticated": authed, "healthy": true})
		case r.URL.Path == "/api/backuprestore":
			w.Write([]byte(`{"prefixes":[{"prefix":"S","color":"red","weight":1}],"tickets":[{"prefix":"S","t_id":1,"first_name":"Sam","last_name":"Server","phone_number":"1","pref":"CALL"}],"baskets":[]}`))
		case r.URL.Path == "/api/bad":
			w.WriteHeader(400)
			w.Write([]byte(`{"detail":"nope"}`))
		case r.Method == http.MethodPost:
			// A server answers a save with the rows as it stored them: here,
			// as they were sent.
			body, _ := io.ReadAll(r.Body)
			w.Write(body)
		default:
			w.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(f.ts.Close)
	return f
}

func (f *fakeServer) set(mode string) {
	f.mu.Lock()
	f.mode = mode
	f.mu.Unlock()
}

func (f *fakeServer) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// lastHeartbeat returns the headers of the last GET /api.
func (f *fakeServer) lastHeartbeat() http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.heartbeats) == 0 {
		return nil
	}
	return f.heartbeats[len(f.heartbeats)-1]
}

func newSyncer(t *testing.T, serverURL string) (*Syncer, *store.Store) {
	t.Helper()
	sqldb, err := db.Open(filepath.Join(t.TempDir(), "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	if err := db.Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateClient(sqldb); err != nil {
		t.Fatal(err)
	}
	st := store.New(sqldb)
	settings := filepath.Join(t.TempDir(), "settings.json")
	s := config.Defaults()
	if serverURL != "" {
		u, _ := url.Parse(serverURL)
		s.RemoteServer, s.RemotePort, s.RemoteKey, s.RemoteName = u.Hostname(), u.Port(), "KEY", "fake"
	}
	if err := config.Save(settings, s); err != nil {
		t.Fatal(err)
	}
	cfg := config.Open(settings)
	client := func(s config.Settings) *remote.Client {
		if s.RemoteURL() == "" {
			return nil
		}
		return remote.New(s.RemoteURL(), s.RemoteKey, false)
	}
	tm := Timings{Heartbeat: 0, PingTimeout: time.Second, OfflineAfter: 20 * time.Millisecond, Backoff: []time.Duration{time.Millisecond}}
	return New(st, cfg, client, tm), st
}

func pendingFailed(t *testing.T, st *store.Store) (int, int) {
	t.Helper()
	p, f, err := st.OutboxCounts()
	if err != nil {
		t.Fatal(err)
	}
	return p, f
}

func TestStandaloneHasNoState(t *testing.T) {
	s, _ := newSyncer(t, "")
	s.Tick()
	if st := s.Status(); st.Mode != "standalone" || st.State != "" {
		t.Fatalf("status = %+v", st)
	}
	if !s.Online() {
		t.Fatal("standalone must count as online so nothing is queued")
	}
}

func TestDrainOrderFailedListAndPull(t *testing.T) {
	f := newFakeServer(t)
	s, st := newSyncer(t, f.ts.URL)
	for _, q := range [][2]string{{"POST", "/api/tickets"}, {"POST", "/api/bad"}, {"POST", "/api/baskets"}} {
		if err := s.Enqueue(q[0], q[1], []byte(`[]`)); err != nil {
			t.Fatal(err)
		}
	}
	s.Tick()
	if p, fl := pendingFailed(t, st); p != 0 || fl != 1 {
		t.Fatalf("after a tick with the server up: pending %d failed %d, want 0 and 1", p, fl)
	}
	seen := f.seen()
	// The replay is followed at once by a heartbeat saying nothing waits.
	want := []string{"GET /api", "POST /api/tickets", "POST /api/bad", "POST /api/baskets", "GET /api", "GET /api/backuprestore"}
	if len(seen) != len(want) {
		t.Fatalf("requests = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("requests = %v, want %v", seen, want)
		}
	}
	failed, _ := st.ListFailed()
	if len(failed) != 1 || failed[0].Path != "/api/bad" || failed[0].LastError != "400: nope" {
		t.Fatalf("failed list = %+v", failed)
	}
	// The pull copied the server's data into the mirror.
	ps, _ := st.ListPrefixes()
	ts, _ := st.AllTickets()
	if len(ps) != 1 || ps[0].Prefix != "S" || len(ts) != 1 || ts[0].LastName != "Server" {
		t.Fatalf("mirror after pull: prefixes %+v tickets %+v", ps, ts)
	}
	status := s.Status()
	if status.Mode != "remote" || status.State != Connected || status.ServerName != "fake" || status.Pending != 0 || status.Failed != 1 || status.LastOK == "" {
		t.Fatalf("status = %+v", status)
	}
}

func TestServerGoesAwayAndComesBack(t *testing.T) {
	f := newFakeServer(t)
	s, st := newSyncer(t, f.ts.URL)
	s.Tick()
	if s.State() != Connected {
		t.Fatalf("state = %q, want connected", s.State())
	}

	f.set("down")
	s.Tick()
	if s.State() != Reconnecting || s.Online() {
		t.Fatalf("state = %q online=%v, want reconnecting and not online", s.State(), s.Online())
	}
	if err := s.Enqueue("POST", "/api/tickets", []byte(`[{"prefix":"A","t_id":1}]`)); err != nil {
		t.Fatal(err)
	}
	before := len(f.seen())
	s.Tick()
	if p, _ := pendingFailed(t, st); p != 1 {
		t.Fatalf("a save must wait while the server is down; pending = %d", p)
	}
	time.Sleep(25 * time.Millisecond)
	s.Tick()
	if s.State() != Offline {
		t.Fatalf("state = %q, want offline after the grace period", s.State())
	}
	for _, r := range f.seen()[before:] {
		if r != "GET /api" {
			t.Fatalf("only heartbeats may reach a down server, saw %q", r)
		}
	}

	f.set("up")
	s.Reset()
	s.Tick()
	if s.State() != Connected {
		t.Fatalf("state = %q, want connected after the server returns", s.State())
	}
	if p, fl := pendingFailed(t, st); p != 0 || fl != 0 {
		t.Fatalf("outbox after reconnect: pending %d failed %d", p, fl)
	}
	seen := f.seen()
	last := seen[len(seen)-3:]
	if last[0] != "POST /api/tickets" || last[1] != "GET /api" || last[2] != "GET /api/backuprestore" {
		t.Fatalf("after reconnect the queue drains, a heartbeat says so, then the mirror is pulled; tail = %v", last)
	}
}

func TestRejectedKeyStopsReplay(t *testing.T) {
	f := newFakeServer(t)
	s, st := newSyncer(t, f.ts.URL)
	f.set("nokey")
	if err := s.Enqueue("POST", "/api/tickets", []byte(`[]`)); err != nil {
		t.Fatal(err)
	}
	s.Tick()
	if s.State() != Unauthenticated {
		t.Fatalf("state = %q, want unauthenticated", s.State())
	}
	if p, fl := pendingFailed(t, st); p != 1 || fl != 0 {
		t.Fatalf("a rejected key must keep the save waiting: pending %d failed %d", p, fl)
	}
	if s.Online() {
		t.Fatal("unauthenticated is not online")
	}
}

func TestUnreachableServer(t *testing.T) {
	// A port nothing listens on.
	ln := httptest.NewServer(http.NotFoundHandler())
	addr := ln.URL
	ln.Close()
	s, _ := newSyncer(t, addr)
	s.Tick()
	if s.State() != Reconnecting {
		t.Fatalf("state = %q, want reconnecting", s.State())
	}
}

// TestHeartbeatCarriesTheQueuedSaves: the server's admin page shows how
// many saves each client still has queued, so every heartbeat says so.
func TestHeartbeatCarriesTheQueuedSaves(t *testing.T) {
	f := newFakeServer(t)
	s, st := newSyncer(t, f.ts.URL)
	s.Tick()
	hb := f.lastHeartbeat()
	if hb == nil || hb.Get("X-TAM-Pending") != "0" || hb.Get("X-TAM-Client") != "tam-client/"+version.Version || hb.Get("TAM-KEY") != "KEY" {
		t.Fatalf("first heartbeat = %v, want X-TAM-Pending 0, X-TAM-Client and the key", hb)
	}

	f.set("down")
	s.Tick()
	for _, path := range []string{"/api/tickets", "/api/baskets"} {
		if err := s.Enqueue("POST", path, []byte(`[]`)); err != nil {
			t.Fatal(err)
		}
	}
	s.Tick()
	if p, _ := pendingFailed(t, st); p != 2 {
		t.Fatalf("pending = %d, want 2 while the server is down", p)
	}
	if got := f.lastHeartbeat().Get("X-TAM-Pending"); got != "2" {
		t.Fatalf("heartbeat with two queued saves said X-TAM-Pending %q, want 2", got)
	}

	// Once the server is back the queue drains, and the next heartbeat
	// reports an empty queue.
	f.set("up")
	s.Reset()
	s.Tick()
	if p, _ := pendingFailed(t, st); p != 0 {
		t.Fatalf("pending = %d after the server returned, want 0", p)
	}
	s.Tick()
	if got := f.lastHeartbeat().Get("X-TAM-Pending"); got != "0" {
		t.Fatalf("heartbeat after the drain said X-TAM-Pending %q, want 0", got)
	}
}

// TestPullWaitsForSavesQueuedAfterTheReplay: a page save can be queued in
// the moment between the replay finding nothing left to send and the pull
// starting. The server does not have that save yet, so the pull must wait
// until it has been sent instead of copying the server's older row over it.
func TestPullWaitsForSavesQueuedAfterTheReplay(t *testing.T) {
	f := newFakeServer(t)
	s, st := newSyncer(t, f.ts.URL)
	s.pulling = func() {
		s.pulling = nil
		row := []store.Ticket{{Prefix: "S", TID: 1, FirstName: "Sam", LastName: "Client", PhoneNumber: "2", Pref: "CALL"}}
		if err := st.UpsertTickets(row); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(row)
		if err := s.Enqueue("POST", "/api/tickets", body); err != nil {
			t.Fatal(err)
		}
	}
	s.Tick()
	if tk, _ := st.Ticket("S", 1); tk == nil || tk.LastName != "Client" {
		t.Fatalf("the client's copy has %+v, want its queued save (Client)", tk)
	}
	for _, r := range f.seen() {
		if r == "GET /api/backuprestore" {
			t.Fatalf("the pull downloaded while a save was still queued: %v", f.seen())
		}
	}

	// The next tick sends the save first (and says the queue is empty),
	// then pulls.
	s.Tick()
	if p, _ := pendingFailed(t, st); p != 0 {
		t.Fatalf("pending after the second tick = %d, want 0", p)
	}
	seen := f.seen()
	if n := len(seen); n < 3 || seen[n-3] != "POST /api/tickets" || seen[n-2] != "GET /api" || seen[n-1] != "GET /api/backuprestore" {
		t.Fatalf("requests = %v, want the queued save sent before the download", seen)
	}
}

// TestHeartbeatRightAfterTheReplay: the server's admin page shows how many
// saves each client still has queued, from its heartbeat. Once the replay
// has sent them the client says so at once, not at the next heartbeat, so
// the page never shows saves that are no longer waiting.
func TestHeartbeatRightAfterTheReplay(t *testing.T) {
	f := newFakeServer(t)
	s, st := newSyncer(t, f.ts.URL)
	s.t.Heartbeat = time.Hour // only the first tick pings on its own
	for _, path := range []string{"/api/tickets", "/api/baskets"} {
		if err := s.Enqueue("POST", path, []byte(`[]`)); err != nil {
			t.Fatal(err)
		}
	}
	s.Tick()
	if p, _ := pendingFailed(t, st); p != 0 {
		t.Fatalf("pending = %d after the replay, want 0", p)
	}
	queued := func() []string {
		f.mu.Lock()
		defer f.mu.Unlock()
		var out []string
		for _, h := range f.heartbeats {
			out = append(out, h.Get("X-TAM-Pending"))
		}
		return out
	}
	if got := queued(); len(got) != 2 || got[0] != "2" || got[1] != "0" {
		t.Fatalf("heartbeats said %v queued, want [2 0]: the queue, then nothing left", got)
	}
	// With nothing sent, a tick does not ping before the heartbeat is due.
	s.Tick()
	if got := queued(); len(got) != 2 {
		t.Fatalf("heartbeats said %v queued, want no heartbeat from a tick that sent nothing", got)
	}
}
