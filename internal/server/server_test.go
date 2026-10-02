package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/presence"
	"ticket-auction-manager/tam-go/internal/store"
	"ticket-auction-manager/tam-go/internal/version"
)

type api struct {
	t     *testing.T
	url   string
	st    *store.Store
	sqldb *sql.DB
	key   string
	saves int64 // the number of the last save do numbered
}

func newAPI(t *testing.T, opts ...Option) *api {
	t.Helper()
	return newAPIWithPassword(t, FixedPassword("secret"), opts...)
}

func newAPIWithPassword(t *testing.T, pw Password, opts ...Option) *api {
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
	st := store.New(sqldb)
	k, err := st.CreateKey("test")
	if err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, NewHandler(st, pw, opts...))
	return &api{t: t, url: ts.URL, st: st, sqldb: sqldb, key: k.AuthKey}
}

// newTestServer serves h on a local port until the test ends. Its
// connections close with a reset rather than lingering in TIME_WAIT: the
// fuzz target starts a server per input, and on Windows the lingering
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

// do sends a request. body may be nil, a string (sent verbatim as JSON) or
// any value (marshalled). A keyed POST or DELETE without X-TAM-Save is
// numbered as the next save of client "test", as tam-client numbers its
// saves.
func (a *api) do(method, path string, body any, headers map[string]string) (int, []byte) {
	a.t.Helper()
	if (method == "POST" || method == "DELETE") && headers["TAM-KEY"] != "" && headers["X-TAM-Save"] == "" {
		a.saves++
		numbered := map[string]string{"X-TAM-Client-Name": "test", "X-TAM-Save": strconv.FormatInt(a.saves, 10)}
		for k, v := range headers {
			numbered[k] = v
		}
		headers = numbered
	}
	var rdr io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rdr = strings.NewReader(b)
		default:
			data, err := json.Marshal(b)
			if err != nil {
				a.t.Fatal(err)
			}
			rdr = bytes.NewReader(data)
		}
	}
	req, err := http.NewRequest(method, a.url+path, rdr)
	if err != nil {
		a.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

func (a *api) keyed(method, path string, body any) (int, []byte) {
	return a.do(method, path, body, map[string]string{"TAM-KEY": a.key})
}

func (a *api) pw(method, path string, body any) (int, []byte) {
	return a.do(method, path, body, map[string]string{"TAM-PW": "secret"})
}

func decode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return v
}

func TestKeyRequiredOnDataRoutes(t *testing.T) {
	a := newAPI(t)
	if code, body := a.do("GET", "/api/prefixes", nil, nil); code != 401 || !strings.Contains(string(body), "Invalid Key") {
		t.Fatalf("no key: %d %s", code, body)
	}
	if code, _ := a.do("GET", "/api/prefixes", nil, map[string]string{"TAM-KEY": "WRONG"}); code != 401 {
		t.Fatalf("bad key: %d", code)
	}
	if code, _ := a.keyed("GET", "/api/prefixes", nil); code != 200 {
		t.Fatalf("good key: %d", code)
	}
	if code, _ := a.do("POST", "/api/tickets", `[]`, nil); code != 401 {
		t.Fatalf("POST without key: %d", code)
	}
}

func TestPasswordProtectsKeyManagement(t *testing.T) {
	a := newAPI(t)
	if code, body := a.do("GET", "/api/auth", nil, nil); code != 401 || !strings.Contains(string(body), "Invalid Password") {
		t.Fatalf("no password: %d %s", code, body)
	}
	if code, _ := a.do("GET", "/api/auth", nil, map[string]string{"TAM-PW": "nope"}); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	if code, _ := a.do("GET", "/api/auth", nil, map[string]string{"TAM-KEY": a.key}); code != 401 {
		t.Fatalf("a data key must not open key management: %d", code)
	}
}

// settable is a Password that can be set later, like the admin package's,
// so a test can watch the API leave setup mode.
type settable struct{ pw Password }

func (s *settable) IsSet() bool             { return s.pw.IsSet() }
func (s *settable) Check(plain string) bool { return s.pw.Check(plain) }

func TestKeyManagementIs503WhilePasswordUnset(t *testing.T) {
	pw := &settable{pw: FixedPassword("")}
	a := newAPIWithPassword(t, pw)
	for _, method := range []string{"GET", "POST", "DELETE"} {
		var body any
		if method == "POST" {
			body = map[string]string{"description": "client"}
		}
		code, res := a.do(method, "/api/auth?key_to_del=X", body, map[string]string{"TAM-PW": ""})
		if code != 503 || strings.TrimSpace(string(res)) != `{"detail":"server password not set"}` {
			t.Fatalf("%s /api/auth while unset = %d %s", method, code, res)
		}
	}
	// Data routes and the root keep working; only pairing waits.
	if code, _ := a.keyed("GET", "/api/prefixes", nil); code != 200 {
		t.Fatalf("data route while unset = %d", code)
	}
	if code, _ := a.do("GET", "/api", nil, nil); code != 200 {
		t.Fatalf("root while unset = %d", code)
	}

	pw.pw = FixedPassword("later")
	if code, _ := a.do("GET", "/api/auth", nil, map[string]string{"TAM-PW": "later"}); code != 200 {
		t.Fatalf("after the password is set = %d", code)
	}
	if code, _ := a.do("GET", "/api/auth", nil, map[string]string{"TAM-PW": "secret"}); code != 401 {
		t.Fatalf("old password after the change = %d", code)
	}
}

func TestFixedPassword(t *testing.T) {
	pw := FixedPassword("secret")
	if !pw.IsSet() || !pw.Check("secret") || pw.Check("Secret") || pw.Check("") {
		t.Fatal("FixedPassword(secret) misbehaves")
	}
	if unset := FixedPassword(""); unset.IsSet() || unset.Check("") {
		t.Fatal("an empty FixedPassword must be unset and never match")
	}
}

func TestRootReportsNameAndVersion(t *testing.T) {
	a := newAPI(t)
	_, body := a.do("GET", "/api", nil, nil)
	root := decode[map[string]any](t, body)
	hostname, _ := os.Hostname()
	if root["name"] != hostname || root["version"] != version.Version || root["whoami"] != "TAM Server" || root["healthy"] != true || root["authenticated"] != false {
		t.Fatalf("root = %v", root)
	}
	ev, err := a.st.Event()
	if err != nil || ev == nil || root["event"] != ev.Event || len(ev.Event) != 32 {
		t.Fatalf("root names event %v, the server holds %+v (%v)", root["event"], ev, err)
	}
	if len(root) != 6 {
		t.Fatalf("root has %d fields, want whoami, authenticated, healthy, name, version and event: %v", len(root), root)
	}

	named := newAPI(t, WithInfo(Info{Name: "front-desk", Version: "9.9.9"}))
	_, body = named.do("GET", "/api/", nil, nil)
	if root = decode[map[string]any](t, body); root["name"] != "front-desk" || root["version"] != "9.9.9" {
		t.Fatalf("root with WithInfo = %v", root)
	}
}

func TestKeyRoutesTouchLastSeenOncePerInterval(t *testing.T) {
	a := newAPI(t)
	lastSeen := func() string {
		t.Helper()
		ks, err := a.st.ListKeys()
		if err != nil || len(ks) != 1 {
			t.Fatalf("ListKeys = %v %v", ks, err)
		}
		return ks[0].LastSeen
	}
	if lastSeen() != "" {
		t.Fatal("an unused key must have no last_seen")
	}
	if code, _ := a.do("GET", "/api", nil, nil); code != 200 {
		t.Fatalf("root = %d", code)
	}
	if lastSeen() != "" {
		t.Fatal("the root without a key must not touch last_seen")
	}

	// The client's heartbeat is GET /api with its key.
	a.do("GET", "/api", nil, map[string]string{"TAM-KEY": a.key})
	first := lastSeen()
	if seen, err := time.Parse(time.RFC3339, first); err != nil || time.Since(seen) > 5*time.Second {
		t.Fatalf("last_seen after the heartbeat = %q (%v)", first, err)
	}

	// Within the interval nothing is written, even when the stored value
	// changed underneath.
	if _, err := a.sqldb.Exec(`UPDATE auth_key_activity SET last_seen = '2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	a.keyed("POST", "/api/tickets", `[]`)
	if lastSeen() != "2000-01-01T00:00:00Z" {
		t.Fatalf("last_seen was written again within the interval: %q", lastSeen())
	}

	// A wrong key touches nothing.
	a.do("GET", "/api/prefixes", nil, map[string]string{"TAM-KEY": "WRONG"})
	if lastSeen() != "2000-01-01T00:00:00Z" {
		t.Fatal("a rejected key must not touch last_seen")
	}

	old := touchEvery
	touchEvery = 0
	defer func() { touchEvery = old }()
	a.keyed("GET", "/api/baskets", nil)
	if seen, err := time.Parse(time.RFC3339, lastSeen()); err != nil || time.Since(seen) > 5*time.Second {
		t.Fatalf("last_seen after the interval = %q (%v)", lastSeen(), err)
	}
	if code, body := a.pw("GET", "/api/auth", nil); code != 200 || !strings.Contains(string(body), `"last_seen":"`) {
		t.Fatalf("GET /api/auth should carry last_seen once a key was used: %d %s", code, body)
	}
}

func TestKeyLifecycle(t *testing.T) {
	a := newAPI(t)
	code, body := a.pw("POST", "/api/auth", map[string]string{"description": "client"})
	if code != 200 {
		t.Fatalf("create key: %d %s", code, body)
	}
	k := decode[store.AuthKey](t, body)
	if len(k.AuthKey) != 32 || k.Description != "client" {
		t.Fatalf("created key = %+v", k)
	}

	code, body = a.do("GET", "/api", nil, map[string]string{"TAM-KEY": k.AuthKey})
	root := decode[map[string]any](t, body)
	if code != 200 || root["whoami"] != "TAM Server" || root["authenticated"] != true || root["healthy"] != true {
		t.Fatalf("root with key = %d %v", code, root)
	}
	_, body = a.do("GET", "/api", nil, nil)
	if root = decode[map[string]any](t, body); root["authenticated"] != false {
		t.Fatalf("root without key = %v", root)
	}

	_, body = a.pw("GET", "/api/auth", nil)
	if list := decode[[]store.AuthKey](t, body); len(list) != 2 {
		t.Fatalf("ListKeys = %v", list)
	}

	code, body = a.pw("DELETE", "/api/auth?key_to_del="+k.AuthKey, nil)
	if code != 200 || decode[store.AuthKey](t, body).Description != "client" {
		t.Fatalf("delete key should echo the deleted row: %d %s", code, body)
	}
	if code, _ = a.pw("DELETE", "/api/auth?key_to_del="+k.AuthKey, nil); code != 404 {
		t.Fatalf("delete missing key: %d, want 404", code)
	}
	if code, _ = a.do("GET", "/api/prefixes", nil, map[string]string{"TAM-KEY": k.AuthKey}); code != 401 {
		t.Fatalf("deleted key still works: %d", code)
	}
}

func TestPrefixes(t *testing.T) {
	a := newAPI(t)
	code, body := a.keyed("POST", "/api/prefixes", []store.Prefix{{Prefix: "B", Color: "blue", Weight: 2}, {Prefix: " A ", Color: "red", Weight: 1}})
	if code != 200 {
		t.Fatalf("post: %d %s", code, body)
	}
	_, body = a.keyed("GET", "/api/prefixes", nil)
	ps := decode[[]store.Prefix](t, body)
	if len(ps) != 2 || ps[0].Prefix != "A" || ps[1].Prefix != "B" {
		t.Fatalf("list = %v", ps)
	}

	// Nothing is written when any item is invalid.
	for _, bad := range []string{
		`[{"prefix":"X","color":"red","weight":"heavy"}]`,
		`[{"prefix":"","color":"red","weight":1}]`,
		`[{"prefix":"   ","color":"red","weight":1}]`,
		`[{"prefix":"X","color":"chartreuse","weight":1}]`,
		`[{"prefix":"X","color":"red","weight":-1}]`,
		`{bad`,
	} {
		if code, _ := a.keyed("POST", "/api/prefixes", bad); code != 400 {
			t.Errorf("POST %s: %d, want 400", bad, code)
		}
	}
	_, body = a.keyed("GET", "/api/prefixes", nil)
	if ps = decode[[]store.Prefix](t, body); len(ps) != 2 {
		t.Fatalf("invalid posts must not write rows: %v", ps)
	}

	if code, _ = a.do("POST", "/api/prefixes", `[]`, map[string]string{"TAM-KEY": a.key, "Content-Type": "text/plain"}); code != 400 {
		t.Fatalf("text/plain body: %d, want 400", code)
	}

	code, body = a.keyed("DELETE", "/api/prefixes?p=A", nil)
	if code != 200 || decode[store.Prefix](t, body).Color != "red" {
		t.Fatalf("delete should echo the deleted row: %d %s", code, body)
	}
	if code, _ = a.keyed("DELETE", "/api/prefixes?p=A", nil); code != 404 {
		t.Fatalf("delete missing: %d, want 404", code)
	}
	if code, _ = a.keyed("DELETE", "/api/prefixes", nil); code != 400 {
		t.Fatalf("delete without p: %d, want 400", code)
	}

	// A weight is a JSON integer; a string or a fraction is refused and
	// nothing of the batch is written.
	for _, body := range []string{`[{"prefix":"S","color":"red","weight":"3"}]`, `[{"prefix":"S","color":"red","weight":3},{"prefix":"F","color":"red","weight":4.5}]`} {
		if code, _ = a.keyed("POST", "/api/prefixes", body); code != 400 {
			t.Fatalf("POST %s = %d, want 400", body, code)
		}
	}
	_, body = a.keyed("GET", "/api/prefixes", nil)
	if ps = decode[[]store.Prefix](t, body); len(ps) != 1 {
		t.Fatalf("after refused posts: %v", ps)
	}
	// A JSON null body is an empty list, never a null answer.
	if code, body = a.keyed("POST", "/api/prefixes", `null`); code != 200 || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("null body = %d %q", code, body)
	}
}

func TestErrorsAreJSON(t *testing.T) {
	a := newAPI(t)
	if code, body := a.keyed("GET", "/api/nope", nil); code != 404 || strings.TrimSpace(string(body)) != `{"detail":"Not Found"}` {
		t.Fatalf("unknown path = %d %q", code, body)
	}
	if code, body := a.keyed("DELETE", "/api/tickets", nil); code != 405 || strings.TrimSpace(string(body)) != `{"detail":"Method Not Allowed"}` {
		t.Fatalf("wrong method = %d %q", code, body)
	}
	if code, _ := a.keyed("GET", "/api/", nil); code != 200 {
		t.Fatalf("GET /api/ = %d, want 200", code)
	}
	if code, body := a.keyed("POST", "/api/tickets", `[{"prefix":"A","first_name":"no id"}]`); code != 400 || !strings.Contains(string(body), "t_id is required") {
		t.Fatalf("missing t_id = %d %s", code, body)
	}
	if code, _ := a.keyed("POST", "/api/tickets", `[{"prefix":"A","t_id":"12","pref":"CALL"}]`); code != 400 {
		t.Fatalf("a t_id sent as a string = %d, want 400", code)
	}
}

func TestTicketsBasketsDrawingReports(t *testing.T) {
	a := newAPI(t)
	code, body := a.keyed("POST", "/api/tickets", []store.Ticket{
		{Prefix: "A", TID: 1, FirstName: "Zed", LastName: "Young", PhoneNumber: "1", Pref: "CALL"},
		{Prefix: "A", TID: 2, FirstName: "Amy", LastName: "Adams", PhoneNumber: "2", Pref: "TEXT"},
	})
	if code != 200 {
		t.Fatalf("post tickets: %d %s", code, body)
	}

	_, body = a.keyed("GET", "/api/tickets", nil)
	if all := decode[[]store.Ticket](t, body); len(all) != 2 {
		t.Fatalf("all tickets = %v", all)
	}
	_, body = a.keyed("GET", "/api/tickets/A", nil)
	if byPrefix := decode[[]store.Ticket](t, body); len(byPrefix) != 2 {
		t.Fatalf("tickets by prefix = %v", byPrefix)
	}
	_, body = a.keyed("GET", "/api/tickets/A/2", nil)
	if single := decode[[]store.Ticket](t, body); len(single) != 1 || single[0].FirstName != "Amy" {
		t.Fatalf("single ticket = %v", single)
	}
	_, body = a.keyed("GET", "/api/tickets/A/9", nil)
	if missing := decode[[]store.Ticket](t, body); len(missing) != 0 {
		t.Fatalf("missing ticket = %v, want []", missing)
	}
	_, body = a.keyed("GET", "/api/tickets/A/2/1", nil) // reversed range is swapped
	if rng := decode[[]store.Ticket](t, body); len(rng) != 2 {
		t.Fatalf("range = %v", rng)
	}
	if code, _ = a.keyed("GET", "/api/tickets/A/x", nil); code != 400 {
		t.Fatalf("non-integer id: %d", code)
	}
	if code, _ = a.keyed("POST", "/api/tickets", `[{"prefix":"  ","t_id":3,"pref":"CALL"}]`); code != 400 {
		t.Fatalf("blank prefix: %d, want 400", code)
	}
	if code, _ = a.keyed("POST", "/api/tickets", `[{"prefix":"A","t_id":-3,"pref":"CALL"}]`); code != 400 {
		t.Fatalf("negative id: %d, want 400", code)
	}

	code, body = a.keyed("POST", "/api/baskets", []store.Basket{{Prefix: "A", BID: 1, Description: "Wine", Donors: "Smiths"}})
	if code != 200 {
		t.Fatalf("post baskets: %d %s", code, body)
	}
	_, body = a.keyed("GET", "/api/baskets/A/1", nil)
	if b := decode[[]store.Basket](t, body); len(b) != 1 || b[0].Description != "Wine" {
		t.Fatalf("single basket = %v", b)
	}

	// The drawing form sends the whole drawing line; extra fields are ignored.
	code, body = a.keyed("POST", "/api/drawing", `[{"prefix":"A","b_id":1,"description":"Wine","winning_ticket":2,"last_name":"","changed":true}]`)
	if code != 200 {
		t.Fatalf("post drawing: %d %s", code, body)
	}
	_, body = a.keyed("GET", "/api/drawing/A/1", nil)
	if d := decode[[]store.DrawingLine](t, body); len(d) != 1 || d[0].WinningTicket != 2 || d[0].LastName != "Adams" {
		t.Fatalf("drawing line = %v", d)
	}
	_, body = a.keyed("GET", "/api/drawing/A/1/3", nil)
	if d := decode[[]store.DrawingLine](t, body); len(d) != 1 {
		t.Fatalf("drawing range = %v", d)
	}
	_, body = a.keyed("GET", "/api/drawing", nil)
	if d := decode[[]store.DrawingLine](t, body); len(d) != 1 {
		t.Fatalf("all drawing = %v", d)
	}
	_, body = a.keyed("GET", "/api/baskets/A", nil)
	if b := decode[[]store.Basket](t, body); len(b) != 1 || b[0].Description != "Wine" || b[0].WinningTicket != 2 {
		t.Fatalf("baskets by prefix after drawing = %v", b)
	}

	_, body = a.keyed("GET", "/api/reports/byname/A", nil)
	if r := decode[[]store.ReportByNameLine](t, body); len(r) != 1 || r[0].LastName != "Adams" || r[0].Description != "Wine" {
		t.Fatalf("by name = %v", r)
	}
	_, body = a.keyed("GET", "/api/reports/bybasket/A", nil)
	if r := decode[[]store.ReportByBasketLine](t, body); len(r) != 1 || r[0].BID != 1 {
		t.Fatalf("by basket = %v", r)
	}
	_, body = a.keyed("GET", "/api/reports/counts", nil)
	counts := decode[[]store.ReportCountLine](t, body)
	if len(counts) != 2 || counts[1].Prefix != "Total" || counts[1].TotalBuys != 2 {
		t.Fatalf("counts = %v", counts)
	}

	_, body = a.keyed("GET", "/api/search/tickets?last_name=ada", nil)
	if found := decode[[]store.Ticket](t, body); len(found) != 1 || found[0].TID != 2 {
		t.Fatalf("search = %v", found)
	}
	code, _ = a.keyed("POST", "/api/search/tickets", []store.Ticket{{Prefix: "A", TID: 2, FirstName: "Amy", LastName: "Adams-Lee", PhoneNumber: "2", Pref: "TEXT"}})
	if code != 200 {
		t.Fatalf("search post: %d", code)
	}
	_, body = a.keyed("GET", "/api/tickets/A/2", nil)
	if single := decode[[]store.Ticket](t, body); single[0].LastName != "Adams-Lee" {
		t.Fatalf("search post did not update: %v", single)
	}
}

// TestTicketZeroWinsNothing: winning ticket 0 means the basket is not drawn
// yet, so a ticket numbered 0 must not show as the winner of every basket
// still to draw, on the drawing or in the reports; a drawn basket still
// shows its winner.
func TestTicketZeroWinsNothing(t *testing.T) {
	a := newAPI(t)
	for _, post := range []struct {
		path string
		body any
	}{
		{"/api/tickets", []store.Ticket{
			{Prefix: "A", TID: 0, FirstName: "Zero", LastName: "Zed", PhoneNumber: "000", Pref: "CALL"},
			{Prefix: "A", TID: 5, FirstName: "Fay", LastName: "Five", PhoneNumber: "555", Pref: "TEXT"},
		}},
		{"/api/baskets", []store.Basket{{Prefix: "A", BID: 1, Description: "Wine"}, {Prefix: "A", BID: 2, Description: "Cheese"}}},
		{"/api/drawing", []store.Basket{{Prefix: "A", BID: 2, WinningTicket: 5}}},
	} {
		if code, body := a.keyed("POST", post.path, post.body); code != 200 {
			t.Fatalf("POST %s = %d %s", post.path, code, body)
		}
	}

	_, body := a.keyed("GET", "/api/drawing/A", nil)
	drawing := decode[[]store.DrawingLine](t, body)
	if len(drawing) != 2 || drawing[0].LastName != "" || drawing[0].FirstName != "" || drawing[0].PhoneNumber != "" || drawing[1].LastName != "Five" {
		t.Fatalf("drawing = %+v; want basket 1 without a winner and basket 2 won by Five", drawing)
	}
	_, body = a.keyed("GET", "/api/drawing/A/1", nil)
	if single := decode[[]store.DrawingLine](t, body); len(single) != 1 || single[0].LastName != "" {
		t.Fatalf("drawing line 1 = %+v, want no winner", single)
	}
	_, body = a.keyed("GET", "/api/reports/byname/A", nil)
	byName := decode[[]store.ReportByNameLine](t, body)
	if len(byName) != 2 {
		t.Fatalf("by name = %+v", byName)
	}
	for _, l := range byName {
		if l.BID == 1 && (l.LastName != "" || l.FirstName != "" || l.PhoneNumber != "" || l.Pref != "") || l.BID == 2 && l.LastName != "Five" {
			t.Fatalf("by name = %+v; want basket 1 without a winner and basket 2 won by Five", byName)
		}
	}
	_, body = a.keyed("GET", "/api/reports/bybasket/A", nil)
	byBasket := decode[[]store.ReportByBasketLine](t, body)
	if len(byBasket) != 2 || byBasket[0].LastName != "" || byBasket[1].LastName != "Five" {
		t.Fatalf("by basket = %+v; want basket 1 without a winner and basket 2 won by Five", byBasket)
	}
}

func TestBackupRoundTrip(t *testing.T) {
	a := newAPI(t)
	a.keyed("POST", "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}})
	a.keyed("POST", "/api/tickets", []store.Ticket{{Prefix: "A", TID: 1, FirstName: "F", LastName: "L", PhoneNumber: "P", Pref: "CALL"}})
	a.keyed("POST", "/api/baskets", []store.Basket{{Prefix: "A", BID: 1, Description: "D"}})

	_, body := a.keyed("GET", "/api/backuprestore", nil)
	bf := decode[store.BackupFile](t, body)
	if len(bf.Prefixes) != 1 || len(bf.Tickets) != 1 || len(bf.Baskets) != 1 {
		t.Fatalf("export = %+v", bf)
	}

	other := newAPI(t)
	code, body := other.keyed("POST", "/api/backuprestore", bf)
	if code != 200 || !strings.Contains(string(body), "imported successfully") {
		t.Fatalf("import: %d %s", code, body)
	}
	_, body = other.keyed("GET", "/api/backuprestore", nil)
	if bf2 := decode[store.BackupFile](t, body); len(bf2.Tickets) != 1 || bf2.Tickets[0].FirstName != "F" {
		t.Fatalf("import result = %+v", bf2)
	}
	if code, _ = other.keyed("POST", "/api/backuprestore", `{"prefixes":[{"prefix":"","color":"red","weight":1}]}`); code != 400 {
		t.Fatalf("invalid backup: %d", code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	a := newAPI(t)
	if code, _ := a.keyed("DELETE", "/api/tickets", nil); code != 405 {
		t.Fatalf("DELETE /api/tickets = %d, want 405", code)
	}
	if code, _ := a.keyed("PUT", "/api/prefixes", `[]`); code != 405 {
		t.Fatalf("PUT /api/prefixes = %d, want 405", code)
	}
}

func TestOversizedBodyIs413(t *testing.T) {
	old := httpx.MaxBody
	httpx.MaxBody = 256
	defer func() { httpx.MaxBody = old }()
	a := newAPI(t)
	big := `[{"prefix":"A","t_id":1,"first_name":"` + strings.Repeat("x", 400) + `","pref":"CALL"}]`
	if code, _ := a.keyed("POST", "/api/tickets", big); code != 413 {
		t.Fatalf("oversized body = %d, want 413", code)
	}
}

// TestKeyAcceptedUnderTheOriginalClientsSpelling: the original client sends
// the key as TAM_KEY on its server-backup download, so that spelling counts
// too, on the data routes and on the root route's authenticated flag.
func TestRootReportsTheBuildVersion(t *testing.T) {
	old := version.Version
	version.Version = "9.9.9-test"
	t.Cleanup(func() { version.Version = old })
	a := newAPI(t)
	_, body := a.do("GET", "/api", nil, nil)
	if root := decode[map[string]any](t, body); root["version"] != "9.9.9-test" {
		t.Fatalf("root = %s, want the stamped version", body)
	}
}

// TestPresenceFollowsKeyedRequests: every request with a valid key is a
// sighting of that client, named by X-TAM-Client or else by its
// User-Agent; an accepted POST or DELETE is also an update.
func TestPresenceFollowsKeyedRequests(t *testing.T) {
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	reg := presence.New(func() time.Time { return now })
	a := newAPI(t, WithPresence(reg))
	if snap := reg.Snapshot(); len(snap) != 0 {
		t.Fatalf("nothing has happened yet: %v", snap)
	}

	a.do("GET", "/api/prefixes", nil, map[string]string{"TAM-KEY": a.key, "X-TAM-Client": "tam-client/1.2.3"})
	rec := reg.Snapshot()[a.key]
	if rec.Seen != now || rec.Client != "tam-client/1.2.3" || rec.HasPending || !rec.Updated.IsZero() {
		t.Fatalf("after a keyed read: %+v", rec)
	}

	// A wrong key is nobody.
	a.do("GET", "/api/prefixes", nil, map[string]string{"TAM-KEY": "WRONG", "X-TAM-Client": "tam-client/1.2.3"})
	if snap := reg.Snapshot(); len(snap) != 1 {
		t.Fatalf("a rejected key must not be recorded: %v", snap)
	}

	// An accepted write is an update; a refused one is only a sighting.
	now = now.Add(time.Second)
	if code, _ := a.keyed("POST", "/api/tickets", `[{"prefix":"A","t_id":1,"pref":"CALL"}]`); code != 200 {
		t.Fatalf("post = %d", code)
	}
	updated := now
	if rec = reg.Snapshot()[a.key]; rec.Updated != updated || rec.Seen != now {
		t.Fatalf("after an accepted write: %+v", rec)
	}
	now = now.Add(time.Second)
	if code, _ := a.keyed("POST", "/api/tickets", `[{"prefix":"","t_id":1}]`); code != 400 {
		t.Fatalf("invalid post = %d", code)
	}
	if rec = reg.Snapshot()[a.key]; rec.Updated != updated || rec.Seen != now {
		t.Fatalf("after a refused write: %+v, want seen now and updated unchanged", rec)
	}
	now = now.Add(time.Second)
	if code, _ := a.keyed("DELETE", "/api/prefixes?p=NOPE", nil); code != 404 {
		t.Fatalf("delete of a missing prefix = %d", code)
	}
	if rec = reg.Snapshot()[a.key]; rec.Updated != updated || rec.Seen != now {
		t.Fatalf("after a delete that found nothing: %+v, want seen now and updated unchanged", rec)
	}
	a.keyed("POST", "/api/prefixes", `[{"prefix":"A","color":"red","weight":1}]`)
	now = now.Add(time.Second)
	if code, _ := a.keyed("DELETE", "/api/prefixes?p=A", nil); code != 200 {
		t.Fatalf("delete = %d", code)
	}
	if rec = reg.Snapshot()[a.key]; rec.Updated != now {
		t.Fatalf("after an accepted delete: %+v, want updated now", rec)
	}

	// Without X-TAM-Client the program is the User-Agent's first word, and
	// "unknown" without that either. Plain requests never invent a count.
	a.do("GET", "/api/tickets", nil, map[string]string{"TAM-KEY": a.key, "User-Agent": "Mozilla/5.0 (X11; Linux x86_64)"})
	if rec = reg.Snapshot()[a.key]; rec.Client != "Mozilla/5.0" {
		t.Fatalf("client from the User-Agent = %q, want Mozilla/5.0", rec.Client)
	}
	a.do("GET", "/api/tickets", nil, map[string]string{"TAM-KEY": a.key, "User-Agent": ""})
	if rec = reg.Snapshot()[a.key]; rec.Client != "unknown" {
		t.Fatalf("client without any header = %q, want unknown", rec.Client)
	}
	long := strings.Repeat("x", 200)
	a.do("GET", "/api/tickets", nil, map[string]string{"TAM-KEY": a.key, "X-TAM-Client": long})
	if rec = reg.Snapshot()[a.key]; len(rec.Client) != maxClientLen || !strings.HasPrefix(long, rec.Client) {
		t.Fatalf("an over-long client name must be cut to %d characters, got %d", maxClientLen, len(rec.Client))
	}
	if rec.HasPending {
		t.Fatalf("plain requests must not invent a heartbeat count: %+v", rec)
	}
}

// TestHeartbeatRecordsTheQueuedSaves: GET /api with the key is the
// heartbeat, and X-TAM-Pending on it is how many saves wait on the client.
func TestHeartbeatRecordsTheQueuedSaves(t *testing.T) {
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	reg := presence.New(func() time.Time { return now })
	a := newAPI(t, WithPresence(reg))

	a.do("GET", "/api", nil, map[string]string{"TAM-KEY": a.key, "X-TAM-Client": "tam-client/1.2.3", "X-TAM-Pending": "2"})
	rec := reg.Snapshot()[a.key]
	if rec.Seen != now || !rec.HasPending || rec.Pending != 2 || rec.Client != "tam-client/1.2.3" || !rec.Updated.IsZero() {
		t.Fatalf("after a heartbeat with two queued: %+v", rec)
	}
	now = now.Add(5 * time.Second)
	a.do("GET", "/api/", nil, map[string]string{"TAM-KEY": a.key, "X-TAM-Client": "tam-client/1.2.3", "X-TAM-Pending": "0"})
	if rec = reg.Snapshot()[a.key]; rec.Seen != now || !rec.HasPending || rec.Pending != 0 {
		t.Fatalf("after a heartbeat with nothing queued: %+v", rec)
	}

	// A count that does not parse is still a sighting; the last good count
	// stays.
	a.do("GET", "/api", nil, map[string]string{"TAM-KEY": a.key, "X-TAM-Pending": "1"})
	for _, bad := range []string{"-1", "x", "1.5", ""} {
		now = now.Add(5 * time.Second)
		a.do("GET", "/api", nil, map[string]string{"TAM-KEY": a.key, "X-TAM-Client": "tam-client/1.2.3", "X-TAM-Pending": bad})
		if rec = reg.Snapshot()[a.key]; rec.Seen != now || !rec.HasPending || rec.Pending != 1 {
			t.Fatalf("after a heartbeat with X-TAM-Pending %q: %+v", bad, rec)
		}
	}

	// The original client sends neither header: it is seen under its
	// User-Agent and never gets a count.
	otherReg := presence.New(func() time.Time { return now })
	other := newAPI(t, WithPresence(otherReg))
	other.do("GET", "/api", nil, map[string]string{"TAM-KEY": other.key})
	if rec = otherReg.Snapshot()[other.key]; rec.Seen != now || rec.HasPending || rec.Client != "Go-http-client/1.1" {
		t.Fatalf("a heartbeat without the headers: %+v", rec)
	}

	// No key, or a wrong one, records nothing.
	a.do("GET", "/api", nil, map[string]string{"X-TAM-Pending": "3"})
	a.do("GET", "/api", nil, map[string]string{"TAM-KEY": "WRONG", "X-TAM-Pending": "3"})
	if snap := reg.Snapshot(); len(snap) != 1 {
		t.Fatalf("the root without a valid key must not be recorded: %v", snap)
	}
}

func TestDeletingAKeyForgetsItsPresence(t *testing.T) {
	reg := presence.New(nil)
	a := newAPI(t, WithPresence(reg))
	_, body := a.pw("POST", "/api/auth", map[string]string{"description": "client"})
	k := decode[store.AuthKey](t, body)
	a.do("GET", "/api", nil, map[string]string{"TAM-KEY": k.AuthKey, "X-TAM-Pending": "1"})
	a.do("GET", "/api", nil, map[string]string{"TAM-KEY": a.key, "X-TAM-Pending": "1"})
	if _, ok := reg.Snapshot()[k.AuthKey]; !ok {
		t.Fatal("the new key must be seen")
	}
	if code, _ := a.pw("DELETE", "/api/auth?key_to_del="+k.AuthKey, nil); code != 200 {
		t.Fatalf("delete = %d", code)
	}
	snap := reg.Snapshot()
	if _, ok := snap[k.AuthKey]; ok || len(snap) != 1 {
		t.Fatalf("a deleted key must be forgotten and the others kept: %v", snap)
	}
}

// TestAcceptedWritesPersistLastUpdateOncePerInterval: last_update is
// written like last_seen, throttled, and only by writes the server took.
func TestAcceptedWritesPersistLastUpdateOncePerInterval(t *testing.T) {
	a := newAPI(t)
	lastUpdate := func() string {
		t.Helper()
		ks, err := a.st.ListKeys()
		if err != nil || len(ks) != 1 {
			t.Fatalf("ListKeys = %v %v", ks, err)
		}
		return ks[0].LastUpdate
	}
	a.keyed("GET", "/api/prefixes", nil)
	a.do("GET", "/api", nil, map[string]string{"TAM-KEY": a.key, "X-TAM-Pending": "0"})
	if lastUpdate() != "" {
		t.Fatalf("reads and heartbeats must not set last_update, got %q", lastUpdate())
	}
	if code, _ := a.keyed("POST", "/api/tickets", `[{"prefix":"","t_id":1}]`); code != 400 || lastUpdate() != "" {
		t.Fatalf("a refused write must not set last_update: %d %q", code, lastUpdate())
	}

	a.keyed("POST", "/api/tickets", `[]`)
	if updated, err := time.Parse(time.RFC3339, lastUpdate()); err != nil || time.Since(updated) > 5*time.Second {
		t.Fatalf("last_update after an accepted write = %q (%v)", lastUpdate(), err)
	}
	if _, err := a.sqldb.Exec(`UPDATE auth_key_activity SET last_update = '2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	a.keyed("POST", "/api/baskets", `[]`)
	if lastUpdate() != "2000-01-01T00:00:00Z" {
		t.Fatalf("last_update was written again within the interval: %q", lastUpdate())
	}

	old := touchEvery
	touchEvery = 0
	defer func() { touchEvery = old }()
	a.keyed("POST", "/api/baskets", `[]`)
	if updated, err := time.Parse(time.RFC3339, lastUpdate()); err != nil || time.Since(updated) > 5*time.Second {
		t.Fatalf("last_update after the interval = %q (%v)", lastUpdate(), err)
	}
	if code, body := a.pw("GET", "/api/auth", nil); code != 200 || !strings.Contains(string(body), `"last_update":"`) {
		t.Fatalf("GET /api/auth should carry last_update once a key has written: %d %s", code, body)
	}
}
