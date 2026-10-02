package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
	tamsync "ticket-auction-manager/tam-go/internal/sync"
)

func newStore(t *testing.T, name string) *store.Store {
	t.Helper()
	return openStoreAt(t, filepath.Join(t.TempDir(), name))
}

// openStoreAt is a client store over the database at path.
func openStoreAt(t *testing.T, path string) *store.Store {
	t.Helper()
	sqldb, err := db.Open(path)
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
	return store.New(sqldb)
}

// newServerStore is a store with the server's schema, for the remote side.
func newServerStore(t *testing.T) *store.Store {
	t.Helper()
	sqldb, err := db.Open(filepath.Join(t.TempDir(), "remote.db"))
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
	return store.New(sqldb)
}

// testTimings make the syncer react within a test's patience.
var testTimings = tamsync.Timings{Heartbeat: 0, PingTimeout: time.Second, OfflineAfter: 20 * time.Millisecond, Backoff: []time.Duration{time.Millisecond}}

func pending(t *testing.T, st *store.Store) (int, int) {
	t.Helper()
	p, f, err := st.OutboxCounts()
	if err != nil {
		t.Fatal(err)
	}
	return p, f
}

var testDist = fstest.MapFS{
	"index.html":          {Data: []byte("<!doctype html><title>TAM</title><div id=app></div>")},
	"_app/immutable/x.js": {Data: []byte("console.log('x')")},
	"robots.txt":          {Data: []byte("User-agent: *")},
	"favicon.ico":         {Data: []byte("\x00\x00\x01\x00icon")},
}

type fixture struct {
	t        *testing.T
	url      string
	st       *store.Store
	dbPath   string
	settings string
	h        *handler
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tam-local.db")
	st := openStoreAt(t, dbPath)
	settings := filepath.Join(dir, "settings.json")
	h := newHandler(st, settings, testDist, WithTimings(testTimings))
	ts := newTestServer(t, h.routes(testDist))
	return &fixture{t: t, url: ts.URL, st: st, dbPath: dbPath, settings: settings, h: h}
}

// exec runs a statement on the client's database from outside its store,
// as a copy of the data folder put back would change it.
func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	sqldb, err := db.Open(f.dbPath)
	if err != nil {
		f.t.Fatal(err)
	}
	defer sqldb.Close()
	if _, err := sqldb.Exec(query, args...); err != nil {
		f.t.Fatal(err)
	}
}

// clientName is the name this client gives its saves (see store.NextSave).
func (f *fixture) clientName() string {
	f.t.Helper()
	sqldb, err := db.Open(f.dbPath)
	if err != nil {
		f.t.Fatal(err)
	}
	defer sqldb.Close()
	var name string
	if err := sqldb.QueryRow(`SELECT client FROM save_order WHERE id = 1`).Scan(&name); err != nil {
		f.t.Fatal(err)
	}
	return name
}

// newTestServer serves h on a local port until the test ends. Its
// connections close with a reset rather than lingering in TIME_WAIT: the
// fuzz targets start two servers per input, and on Windows the lingering
// sockets use up the local ports within a minute, failing the next dial.
func newTestServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	ts := httptest.NewUnstartedServer(h)
	ts.Listener = resetOnClose{ts.Listener}
	ts.Start()
	t.Cleanup(ts.Close)
	return ts
}

// resetOnClose sets SO_LINGER to zero on the connections it accepts.
type resetOnClose struct{ net.Listener }

func (l resetOnClose) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetLinger(0)
	}
	return c, err
}

func (f *fixture) do(method, path string, body any, headers map[string]string) (int, []byte) {
	f.t.Helper()
	var rdr io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rdr = strings.NewReader(b)
		default:
			data, _ := json.Marshal(b)
			rdr = bytes.NewReader(data)
		}
	}
	req, err := http.NewRequest(method, f.url+path, rdr)
	if err != nil {
		f.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

func decode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return v
}

// remoteFixture wires a real server handler as the remote and points the
// client's settings at it.
func remoteFixture(t *testing.T, f *fixture) (*store.Store, *httptest.Server) {
	t.Helper()
	rst := newServerStore(t)
	rs := newTestServer(t, server.NewHandler(rst, server.FixedPassword("secret")))
	k, err := rst.CreateKey("client")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(rs.URL)
	s := config.Defaults()
	s.RemoteServer, s.RemotePort, s.RemoteKey = u.Hostname(), u.Port(), k.AuthKey
	if err := config.Save(f.settings, s); err != nil {
		t.Fatal(err)
	}
	return rst, rs
}

func TestSPA(t *testing.T) {
	f := newFixture(t)
	if code, _ := f.do("GET", "/", nil, nil); code != 302 {
		t.Fatalf("/ = %d, want 302", code)
	}
	// A prefix may be named 5.00: a page's address can look like a file's.
	for _, p := range []string{"/web/", "/web/tickets/CALL/", "/web/settings/prefixes/", "/web/tickets/5.00", "/web/drawing/5.00/"} {
		code, body := f.do("GET", p, nil, nil)
		if code != 200 || !strings.Contains(string(body), "<div id=app>") {
			t.Fatalf("%s = %d %q, want the app shell", p, code, body)
		}
	}
	if code, body := f.do("GET", "/web/_app/immutable/x.js", nil, nil); code != 200 || !strings.Contains(string(body), "console.log") {
		t.Fatalf("asset = %d %q", code, body)
	}
	res, err := http.Get(f.url + "/web/_app/immutable/x.js")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("hashed bundle files must be cacheable, got Cache-Control %q", cc)
	}
	if code, body := f.do("GET", "/api/nope", nil, nil); code != 404 || !strings.Contains(string(body), `"detail"`) {
		t.Fatalf("unknown api path = %d %q", code, body)
	}
	if code, _ := f.do("GET", "/web/_app/immutable/missing.js", nil, nil); code != 404 {
		t.Fatalf("missing asset = %d, want 404", code)
	}
	if code, _ := f.do("GET", "/web/_app/", nil, nil); code != 200 {
		t.Fatalf("directory path falls back to the app = %d", code)
	}
}

// TestIconAtTheRoot: a browser asks for /favicon.ico on pages that name no
// icon, such as the API's own answers; it gets the web app's icon.
func TestIconAtTheRoot(t *testing.T) {
	f := newFixture(t)
	res, err := http.Get(f.url + "/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/x-icon" || string(body) != string(testDist["favicon.ico"].Data) {
		t.Fatalf("/favicon.ico = %d %q %q", res.StatusCode, res.Header.Get("Content-Type"), body)
	}
}

func TestStandalonePrefixesAndTickets(t *testing.T) {
	f := newFixture(t)
	code, body := f.do("GET", "/api", nil, nil)
	if code != 200 || decode[map[string]any](t, body)["whoami"] != "TAM Client" {
		t.Fatalf("root = %d %s", code, body)
	}

	code, body = f.do("POST", "/api/prefixes", []store.Prefix{{Prefix: "A&B", Color: "blue", Weight: 1}}, nil)
	if code != 200 {
		t.Fatalf("post prefixes: %d %s", code, body)
	}
	if code, _ = f.do("DELETE", "/api/prefixes?p=A%26B", nil, nil); code != 200 {
		t.Fatalf("delete encoded: %d", code)
	}
	if code, _ = f.do("DELETE", "/api/prefixes?p=A%26B", nil, nil); code != 404 {
		t.Fatalf("delete missing: %d, want 404", code)
	}

	_, body = f.do("GET", "/api/tickets/A/7", nil, nil)
	ph := decode[store.Ticket](t, body)
	if ph.Prefix != "A" || ph.TID != 7 || ph.Pref != "CALL" {
		t.Fatalf("placeholder = %+v", ph)
	}

	f.do("POST", "/api/tickets", []store.Ticket{{Prefix: "A", TID: 2, FirstName: "Amy", Pref: "TEXT"}}, nil)
	_, body = f.do("GET", "/api/tickets/A/3/1", nil, nil)
	rng := decode[[]store.Ticket](t, body)
	if len(rng) != 3 || rng[0].TID != 1 || rng[1].FirstName != "Amy" || rng[2].Pref != "CALL" {
		t.Fatalf("range = %+v", rng)
	}
	_, body = f.do("GET", "/api/tickets/A/1/1000", nil, nil)
	if rng = decode[[]store.Ticket](t, body); len(rng) != 301 {
		t.Fatalf("range cap = %d rows, want 301", len(rng))
	}

	if code, _ = f.do("POST", "/api/tickets", `[{"prefix":"A","t_id":3,"pref":"CALL"},{"prefix":"A","t_id":1.5,"pref":"CALL"}]`, nil); code != 400 {
		t.Fatalf("invalid ticket batch: %d", code)
	}
	if one, _ := f.st.Ticket("A", 3); one != nil {
		t.Fatal("an invalid batch must not write any row")
	}

	_, body = f.do("GET", "/api/drawing/A/1/2", nil, nil)
	if d := decode[[]store.DrawingLine](t, body); len(d) != 2 || d[0].BID != 1 {
		t.Fatalf("drawing placeholders = %+v", d)
	}
	f.do("POST", "/api/drawing", `[{"prefix":"A","b_id":1,"winning_ticket":2,"description":"","changed":true}]`, nil)
	_, body = f.do("GET", "/api/drawing/A/1", nil, nil)
	if d := decode[store.DrawingLine](t, body); d.WinningTicket != 2 || d.FirstName != "Amy" {
		t.Fatalf("drawing line = %+v", d)
	}
	_, body = f.do("GET", "/api/baskets/A/1", nil, nil)
	if b := decode[store.Basket](t, body); b.WinningTicket != 2 {
		t.Fatalf("basket = %+v", b)
	}
	_, body = f.do("GET", "/api/reports/counts", nil, nil)
	if c := decode[[]store.ReportCountLine](t, body); len(c) != 2 {
		t.Fatalf("counts = %+v", c)
	}
	_, body = f.do("GET", "/api/search/tickets?first_name=am", nil, nil)
	if s := decode[[]store.Ticket](t, body); len(s) != 1 {
		t.Fatalf("search = %+v", s)
	}
}

// maxID is the largest id: the largest whole number a browser holds exactly.
const maxID = 1<<53 - 1

// TestRangeSpanningEveryIDIsCut: a range over every id is cut to the page
// size, starting at its lower end, like any other range that is too wide;
// an id outside 0 to maxID is refused.
func TestRangeSpanningEveryIDIsCut(t *testing.T) {
	f := newFixture(t)
	code, body := f.do("GET", fmt.Sprintf("/api/tickets/A/0/%d", maxID), nil, nil)
	if code != 200 {
		t.Fatalf("range from 0 to the largest id = %d %s", code, body)
	}
	rng := decode[[]store.Ticket](t, body)
	if len(rng) != rangeLimit+1 || rng[0].TID != 0 || rng[rangeLimit].TID != rangeLimit {
		t.Fatalf("range from 0 to the largest id has %d rows, want 0 to %d", len(rng), rangeLimit)
	}
	for _, path := range []string{"/api/tickets/A/-1/5", fmt.Sprintf("/api/tickets/A/0/%d", maxID+1), fmt.Sprintf("/api/tickets/A/0/%d", math.MaxInt)} {
		if code, body := f.do("GET", path, nil, nil); code != 400 {
			t.Fatalf("GET %s = %d %s, want 400", path, code, body)
		}
	}
}

// TestRangeEndingAtTheLargestID: a ticket may have the largest id, and the
// range that ends there lists it and stops.
func TestRangeEndingAtTheLargestID(t *testing.T) {
	f := newFixture(t)
	if code, body := f.do("POST", "/api/tickets", []store.Ticket{{Prefix: "A", TID: maxID, FirstName: "Last", Pref: "CALL"}}, nil); code != 200 {
		t.Fatalf("save = %d %s", code, body)
	}
	_, body := f.do("GET", fmt.Sprintf("/api/tickets/A/%d/%d", maxID-2, maxID), nil, nil)
	rng := decode[[]store.Ticket](t, body)
	if len(rng) != 3 || rng[0].TID != maxID-2 || rng[2].TID != maxID || rng[2].FirstName != "Last" {
		t.Fatalf("range up to the largest id = %+v, want two placeholders and the saved ticket", rng)
	}
}

func TestSettings(t *testing.T) {
	f := newFixture(t)
	_, body := f.do("GET", "/api/settings", nil, nil)
	if s := decode[config.Settings](t, body); s != config.Defaults() {
		t.Fatalf("defaults = %+v", s)
	}

	code, body := f.do("POST", "/api/settings", `{"venue_name":" Hall ","remote_port":" 8443 "}`, nil)
	s := decode[config.Settings](t, body)
	if code != 200 || s.VenueName != "Hall" || s.RemotePort != "8443" || s.DefaultPref != "CALL" {
		t.Fatalf("partial save = %d %+v", code, s)
	}

	before, _ := os.ReadFile(f.settings)
	for _, bad := range []string{`nope`, `{"remote_port":"abc"}`, `{"default_pref":"PHONE"}`, `{"colour":"x"}`, `{"remote_tls":"yes"}`, `{"remote_server":"http://tam.lan"}`, `{"remote_server":"tam.lan/api"}`} {
		if code, _ := f.do("POST", "/api/settings", bad, nil); code != 400 {
			t.Errorf("POST %s = %d, want 400", bad, code)
		}
	}
	after, _ := os.ReadFile(f.settings)
	if !bytes.Equal(before, after) {
		t.Fatal("rejected saves must not touch the file")
	}

	// A hand edit is picked up; a broken file keeps the last good settings.
	edited := s
	edited.VenueName = "Edited by hand"
	config.Save(f.settings, edited)
	_, body = f.do("GET", "/api/settings", nil, nil)
	if decode[config.Settings](t, body).VenueName != "Edited by hand" {
		t.Fatalf("hand edit not picked up: %s", body)
	}
	os.WriteFile(f.settings, []byte(`{"venue_name": "Typo",}`), 0o644)
	code, body = f.do("GET", "/api/settings", nil, nil)
	if code != 200 || decode[config.Settings](t, body).VenueName != "Edited by hand" {
		t.Fatalf("malformed file should keep the last good settings: %d %s", code, body)
	}
}

func TestCrossSiteWritesRefused(t *testing.T) {
	f := newFixture(t)
	cross := map[string]string{"Sec-Fetch-Site": "cross-site"}
	if code, _ := f.do("POST", "/api/settings", `{"venue_name":"X"}`, cross); code != 403 {
		t.Fatalf("cross-site POST = %d, want 403", code)
	}
	if code, _ := f.do("DELETE", "/api/prefixes?p=A", nil, cross); code != 403 {
		t.Fatalf("cross-site DELETE = %d, want 403", code)
	}
	if code, _ := f.do("GET", "/api/settings", nil, cross); code != 200 {
		t.Fatalf("cross-site GET = %d, want 200", code)
	}
	if code, _ := f.do("POST", "/api/settings", `{"venue_name":"X"}`, map[string]string{"Sec-Fetch-Site": "same-origin"}); code != 200 {
		t.Fatalf("same-origin POST = %d, want 200", code)
	}
}

func TestRemoteMode(t *testing.T) {
	f := newFixture(t)
	rst, rs := remoteFixture(t, f)

	_, body := f.do("GET", "/api", nil, nil)
	root := decode[map[string]any](t, body)
	if root["whoami"] != "TAM Server" || root["authenticated"] != true || root["healthy"] != true {
		t.Fatalf("root = %v", root)
	}

	code, body := f.do("POST", "/api/tickets", []store.Ticket{{Prefix: "A", TID: 1, FirstName: "Rem", Pref: "CALL"}}, nil)
	if code != 200 {
		t.Fatalf("post tickets: %d %s", code, body)
	}
	if rt, _ := rst.Ticket("A", 1); rt == nil || rt.FirstName != "Rem" {
		t.Fatalf("remote store not written: %+v", rt)
	}
	if lt, _ := f.st.Ticket("A", 1); lt == nil || lt.FirstName != "Rem" {
		t.Fatalf("local mirror not written: %+v", lt)
	}

	_, body = f.do("GET", "/api/tickets/A/1/2", nil, nil)
	if rng := decode[[]store.Ticket](t, body); len(rng) != 2 || rng[0].FirstName != "Rem" || rng[1].Pref != "CALL" {
		t.Fatalf("remote range = %+v", rng)
	}
	_, body = f.do("GET", "/api/tickets/A/1", nil, nil)
	if one := decode[store.Ticket](t, body); one.FirstName != "Rem" {
		t.Fatalf("remote single = %+v", one)
	}

	f.do("POST", "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}}, nil)
	if ps, _ := rst.ListPrefixes(); len(ps) != 1 {
		t.Fatalf("remote prefixes = %v", ps)
	}
	if code, _ = f.do("DELETE", "/api/prefixes?p=A", nil, nil); code != 200 {
		t.Fatalf("remote delete = %d", code)
	}
	if ps, _ := rst.ListPrefixes(); len(ps) != 0 {
		t.Fatalf("remote prefix not deleted: %v", ps)
	}
	if ps, _ := f.st.ListPrefixes(); len(ps) != 0 {
		t.Fatalf("local mirror not deleted: %v", ps)
	}

	// Key management is proxied, translating TAM-PWD to TAM-PW.
	if code, _ = f.do("GET", "/api/auth", nil, map[string]string{"TAM-PWD": "wrong"}); code != 401 {
		t.Fatalf("auth with wrong password = %d, want 401", code)
	}
	code, body = f.do("POST", "/api/auth", `{"description":"tablet"}`, map[string]string{"TAM-PWD": "secret"})
	if code != 200 {
		t.Fatalf("create key = %d %s", code, body)
	}
	created := decode[store.AuthKey](t, body)
	_, body = f.do("GET", "/api/auth", nil, map[string]string{"TAM-PWD": "secret"})
	if keys := decode[[]store.AuthKey](t, body); len(keys) != 2 {
		t.Fatalf("keys = %v", keys)
	}
	if code, _ = f.do("DELETE", "/api/auth?key_to_del="+created.AuthKey, nil, map[string]string{"TAM-PWD": "secret"}); code != 200 {
		t.Fatalf("delete key = %d", code)
	}

	// A prefix that exists only locally is still removed locally when the
	// server answers 404.
	f.st.UpsertPrefixes([]store.Prefix{{Prefix: "LOCALONLY", Color: "red", Weight: 1}})
	if code, _ = f.do("DELETE", "/api/prefixes?p=LOCALONLY", nil, nil); code != 200 {
		t.Fatalf("delete of a local-only prefix = %d, want 200", code)
	}
	if ps, _ := f.st.ListPrefixes(); len(ps) != 0 {
		t.Fatalf("local-only prefix must be gone: %v", ps)
	}
	if code, _ = f.do("DELETE", "/api/prefixes?p=NOWHERE", nil, nil); code != 404 {
		t.Fatalf("delete of a prefix nobody has = %d, want 404", code)
	}

	// Push and remote backup.
	f.st.UpsertBaskets([]store.Basket{{Prefix: "A", BID: 5, Description: "Local basket"}})
	code, body = f.do("POST", "/api/backuprestore/push/baskets", `{}`, nil)
	if code != 200 || !strings.Contains(string(body), "Baskets pushed") {
		t.Fatalf("push = %d %s", code, body)
	}
	if rb, _ := rst.Basket("A", 5); rb == nil {
		t.Fatal("push did not reach the remote store")
	}
	if code, _ = f.do("POST", "/api/backuprestore/push/keys", `{}`, nil); code != 400 {
		t.Fatalf("push bad target = %d", code)
	}
	if code, _ = f.do("POST", "/api/backuprestore/push/baskets", nil, nil); code != 400 {
		t.Fatalf("push without a JSON body (a plain form post) = %d, want 400", code)
	}
	_, body = f.do("GET", "/api/backuprestore/remote", nil, nil)
	if bf := decode[store.BackupFile](t, body); len(bf.Baskets) != 1 || len(bf.Tickets) != 1 {
		t.Fatalf("remote export = %+v", bf)
	}
	code, _ = f.do("POST", "/api/backuprestore/remote", store.BackupFile{Prefixes: []store.Prefix{{Prefix: "Z", Color: "red"}}}, nil)
	if code != 200 {
		t.Fatalf("remote import = %d", code)
	}
	if ps, _ := rst.ListPrefixes(); len(ps) != 1 || ps[0].Prefix != "Z" {
		t.Fatalf("remote import result = %v", ps)
	}

	// A rejected key: the save is kept here and queued, the status says the
	// key was refused, and nothing is lost.
	s, _ := config.Load(f.settings)
	goodKey := s.RemoteKey
	s.RemoteKey = "WRONG"
	config.Save(f.settings, s)
	code, body = f.do("POST", "/api/tickets", []store.Ticket{{Prefix: "A", TID: 9, FirstName: "No", Pref: "CALL"}}, nil)
	if code != 200 {
		t.Fatalf("save with a rejected key = %d %s, want 200 (queued)", code, body)
	}
	if lt, _ := f.st.Ticket("A", 9); lt == nil {
		t.Fatal("a queued save must be kept in the mirror")
	}
	if p, _ := pending(t, f.st); p != 1 || f.h.sync.State() != tamsync.Unauthenticated {
		t.Fatalf("pending = %d state = %q, want 1 and unauthenticated", p, f.h.sync.State())
	}
	if rt, _ := rst.Ticket("A", 9); rt != nil {
		t.Fatal("the server must not have the save yet")
	}
	_, body = f.do("GET", "/api", nil, nil)
	if root = decode[map[string]any](t, body); root["authenticated"] != false || root["healthy"] != true {
		t.Fatalf("root with bad key = %v", root)
	}

	// The key is fixed: the next tick replays the queue.
	s.RemoteKey = goodKey
	config.Save(f.settings, s)
	f.h.sync.Reset()
	f.h.sync.Tick()
	if p, _ := pending(t, f.st); p != 0 {
		t.Fatalf("pending after the key was fixed = %d, want 0", p)
	}
	if rt, _ := rst.Ticket("A", 9); rt == nil || rt.FirstName != "No" {
		t.Fatalf("replayed save missing on the server: %+v", rt)
	}

	// Server down: reads come from the mirror, root reports unhealthy, and
	// saves are queued.
	rs.Close()
	f.h.sync.Tick()
	if f.h.sync.State() != tamsync.Reconnecting {
		t.Fatalf("state with the server down = %q, want reconnecting", f.h.sync.State())
	}
	_, body = f.do("GET", "/api", nil, nil)
	if root = decode[map[string]any](t, body); root["healthy"] != false {
		t.Fatalf("root with server down = %v", root)
	}
	_, body = f.do("GET", "/api/tickets/A/1/2", nil, nil)
	if rng := decode[[]store.Ticket](t, body); len(rng) != 2 || rng[0].FirstName != "Rem" {
		t.Fatalf("range with server down = %+v, want the mirror's rows", rng)
	}
	if code, _ = f.do("POST", "/api/tickets", []store.Ticket{{Prefix: "A", TID: 10, FirstName: "Off", Pref: "CALL"}}, nil); code != 200 {
		t.Fatalf("write with server down = %d, want 200 (queued)", code)
	}
	if p, _ := pending(t, f.st); p != 1 {
		t.Fatalf("pending with server down = %d, want 1", p)
	}
	if lt, _ := f.st.Ticket("A", 10); lt == nil || lt.FirstName != "Off" {
		t.Fatal("an offline save must land in the mirror")
	}
}

// TestPushSendsEveryList pins the wire shape the original server requires:
// all three lists present, never null.
func TestPushSendsEveryList(t *testing.T) {
	f := newFixture(t)
	var got map[string]json.RawMessage
	rs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/backuprestore" {
			json.NewDecoder(r.Body).Decode(&got)
			httpx.WriteJSON(w, 200, map[string]string{"message": "ok"})
			return
		}
		httpx.WriteJSON(w, 200, map[string]any{"whoami": "TAM Server", "authenticated": true, "healthy": true})
	}))
	defer rs.Close()
	u, _ := url.Parse(rs.URL)
	s := config.Defaults()
	s.RemoteServer, s.RemotePort, s.RemoteKey = u.Hostname(), u.Port(), "K"
	config.Save(f.settings, s)
	f.st.UpsertBaskets([]store.Basket{{Prefix: "A", BID: 1, Description: "B"}})

	if code, body := f.do("POST", "/api/backuprestore/push/baskets", `{}`, nil); code != 200 {
		t.Fatalf("push = %d %s", code, body)
	}
	for _, k := range []string{"prefixes", "baskets", "tickets"} {
		v, ok := got[k]
		if !ok || strings.TrimSpace(string(v)) == "null" {
			t.Fatalf("push body must contain a %s list, got %s", k, got[k])
		}
	}
	if !strings.Contains(string(got["baskets"]), `"description":"B"`) {
		t.Fatalf("pushed baskets = %s", got["baskets"])
	}
}

// TestRemoteClientIsReused pins the connection pool across requests.
func TestRemoteClientIsReused(t *testing.T) {
	h := &handler{}
	s := config.Defaults()
	s.RemoteServer, s.RemotePort, s.RemoteKey = "tam.lan", "8000", "K1"
	if h.remote(s) == nil {
		t.Fatal("remote mode should return a client")
	}
	first := h.rc
	s.RemoteKey = "K2"
	h.remote(s)
	if h.rc != first {
		t.Fatal("a changed key must not rebuild the connection pool")
	}
	s.RemotePort = "8443"
	h.remote(s)
	if h.rc == first {
		t.Fatal("a changed server address must rebuild the connection pool")
	}
	s.RemoteServer = ""
	if h.remote(s) != nil {
		t.Fatal("standalone mode must return nil")
	}
}

func TestShutdownRoute(t *testing.T) {
	plain := newFixture(t)
	if code, _ := plain.do("POST", "/api/shutdown", `{}`, nil); code != 501 {
		t.Fatalf("without a hook = %d, want 501", code)
	}

	called := make(chan struct{}, 1)
	st := newStore(t, "local.db")
	settings := filepath.Join(t.TempDir(), "settings.json")
	ts := httptest.NewServer(NewHandler(st, settings, testDist, WithShutdown(func() { called <- struct{}{} })))
	t.Cleanup(ts.Close)
	f := &fixture{t: t, url: ts.URL, st: st, settings: settings}

	if code, _ := f.do("POST", "/api/shutdown", nil, nil); code != 400 {
		t.Fatalf("a plain form post must not stop the app: %d, want 400", code)
	}
	if code, _ := f.do("POST", "/api/shutdown", `{}`, map[string]string{"Sec-Fetch-Site": "cross-site"}); code != 403 {
		t.Fatalf("cross-site = %d, want 403", code)
	}
	select {
	case <-called:
		t.Fatal("rejected requests must not stop the app")
	default:
	}
	code, body := f.do("POST", "/api/shutdown", `{}`, nil)
	if code != 200 || !strings.Contains(string(body), "shutting down") {
		t.Fatalf("shutdown = %d %s", code, body)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("the shutdown hook was not called")
	}
}

func TestStandaloneAuthAndPush(t *testing.T) {
	f := newFixture(t)
	if code, _ := f.do("GET", "/api/auth", nil, map[string]string{"TAM-PWD": "x"}); code != 500 {
		t.Fatalf("auth standalone = %d, want 500", code)
	}
	if code, _ := f.do("POST", "/api/backuprestore/push/tickets", `{}`, nil); code != 500 {
		t.Fatalf("push standalone = %d, want 500", code)
	}
	code, body := f.do("GET", "/api/backuprestore/remote", nil, nil)
	if code != 200 || strings.TrimSpace(string(body)) != "{}" {
		t.Fatalf("remote export standalone = %d %s", code, body)
	}
	f.do("POST", "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red"}}, nil)
	_, body = f.do("GET", "/api/backuprestore/local", nil, nil)
	bf := decode[store.BackupFile](t, body)
	if len(bf.Prefixes) != 1 || bf.Tickets == nil {
		t.Fatalf("local export = %+v", bf)
	}
	other := newFixture(t)
	if code, _ := other.do("POST", "/api/backuprestore/local", bf, nil); code != 200 {
		t.Fatalf("local import = %d", code)
	}
	if ps, _ := other.st.ListPrefixes(); len(ps) != 1 {
		t.Fatalf("local import result = %v", ps)
	}
}

// recorder stands in for a server and records every write it receives.
type recorder struct {
	mu     sync.Mutex
	writes []recordedWrite
}

type recordedWrite struct {
	path string
	body []byte
}

func newRecorder(t *testing.T, f *fixture) *recorder {
	t.Helper()
	rec := &recorder{}
	rs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			rec.mu.Lock()
			rec.writes = append(rec.writes, recordedWrite{r.URL.Path, body})
			rec.mu.Unlock()
			httpx.WriteJSON(w, 200, map[string]string{"message": "ok"})
			return
		}
		httpx.WriteJSON(w, 200, map[string]any{"whoami": "TAM Server", "authenticated": true, "healthy": true})
	}))
	t.Cleanup(rs.Close)
	u, _ := url.Parse(rs.URL)
	s := config.Defaults()
	s.RemoteServer, s.RemotePort, s.RemoteKey = u.Hostname(), u.Port(), "K"
	if err := config.Save(f.settings, s); err != nil {
		t.Fatal(err)
	}
	return rec
}

func (rec *recorder) paths() []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var out []string
	for _, w := range rec.writes {
		out = append(out, w.path)
	}
	return out
}

func (rec *recorder) body(path string) []byte {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, w := range rec.writes {
		if w.path == path {
			return w.body
		}
	}
	return nil
}

// A restore into a server is one request: the server's restore writes the
// winning tickets with the baskets.
func TestRestoreIntoServerIsOneRequest(t *testing.T) {
	f := newFixture(t)
	rec := newRecorder(t, f)
	bf := store.NewBackupFile()
	bf.Baskets = []store.Basket{{Prefix: "A", BID: 1, Description: "Wine", WinningTicket: 7}, {Prefix: "A", BID: 2, Description: "Spa"}}
	if code, body := f.do("POST", "/api/backuprestore/remote", bf, nil); code != 200 {
		t.Fatalf("restore = %d %s", code, body)
	}
	if got := rec.paths(); !reflect.DeepEqual(got, []string{"/api/backuprestore"}) {
		t.Fatalf("the server received %v, want the restore alone", got)
	}
	if !strings.Contains(string(rec.body("/api/backuprestore")), `"winning_ticket":7`) {
		t.Fatalf("restore body = %s", rec.body("/api/backuprestore"))
	}
}

func TestPushBasketsCarriesWinningTickets(t *testing.T) {
	f := newFixture(t)
	rec := newRecorder(t, f)
	if err := f.st.UpsertBaskets([]store.Basket{{Prefix: "A", BID: 1, Description: "Wine", WinningTicket: 7}}); err != nil {
		t.Fatal(err)
	}
	if code, body := f.do("POST", "/api/backuprestore/push/baskets", `{}`, nil); code != 200 {
		t.Fatalf("push = %d %s", code, body)
	}
	if got := rec.paths(); !reflect.DeepEqual(got, []string{"/api/backuprestore"}) {
		t.Fatalf("the server received %v, want the restore alone", got)
	}
	if !strings.Contains(string(rec.body("/api/backuprestore")), `"winning_ticket":7`) {
		t.Fatalf("push body = %s", rec.body("/api/backuprestore"))
	}
}
