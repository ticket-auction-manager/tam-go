package admin

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/guard"
	"ticket-auction-manager/tam-go/internal/presence"
	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
)

// site is one admin page under test with a browser-like client: a cookie
// jar and no automatic redirects, so every 303 can be checked.
type site struct {
	t     *testing.T
	url   string
	dir   string
	sqldb *sql.DB
	st    *store.Store
	pw    *Password
	h     *handler
	reg   *presence.Registry
	c     *http.Client
}

func newSite(t *testing.T, envPassword string, opts ...Option) *site {
	t.Helper()
	dir := t.TempDir()
	sqldb, err := db.Open(filepath.Join(dir, "tam-remote.db"))
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
	pw, err := Load(dir, envPassword)
	if err != nil {
		t.Fatal(err)
	}
	// The registry reads the same clock as the pages, which tests move.
	var hd http.Handler
	reg := presence.New(func() time.Time { return hd.(*handler).ss.now() })
	hd = NewHandler(st, pw, Info{Addr: ":8000", DataDir: dir, Version: "0.0.1", Started: time.Now().Add(-90 * time.Second), Presence: reg}, opts...)
	ts := httptest.NewServer(hd)
	t.Cleanup(ts.Close)
	return &site{t: t, url: ts.URL, dir: dir, sqldb: sqldb, st: st, pw: pw, h: hd.(*handler), reg: reg, c: newBrowser(t)}
}

func newBrowser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (s *site) get(path string) (*http.Response, string) {
	s.t.Helper()
	res, err := s.c.Get(s.url + path)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

func (s *site) post(path string, form url.Values) (*http.Response, string) {
	s.t.Helper()
	res, err := s.c.PostForm(s.url+path, form)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

var csrfRe = regexp.MustCompile(`name="csrf" value="([0-9a-f]{64})"`)

// token returns the form token of a page.
func (s *site) token(page string) string {
	s.t.Helper()
	m := csrfRe.FindStringSubmatch(page)
	if m == nil {
		s.t.Fatalf("no form token in page:\n%s", page)
	}
	return m[1]
}

// login opens the login form and posts the password, expecting success.
func (s *site) login(password string) {
	s.t.Helper()
	_, page := s.get("/admin/")
	res, body := s.post("/admin/login", url.Values{"csrf": {s.token(page)}, "password": {password}})
	if res.StatusCode != 303 || res.Header.Get("Location") != "/admin/status" {
		s.t.Fatalf("login = %d %s\n%s", res.StatusCode, res.Header.Get("Location"), body)
	}
}

// page fetches a logged-in page and returns its body and form token.
func (s *site) page(path string) (string, string) {
	s.t.Helper()
	res, body := s.get(path)
	if res.StatusCode != 200 {
		s.t.Fatalf("GET %s = %d\n%s", path, res.StatusCode, body)
	}
	return body, s.token(body)
}

func wantRedirect(t *testing.T, res *http.Response, to string) {
	t.Helper()
	if res.StatusCode != 303 || res.Header.Get("Location") != to {
		t.Fatalf("got %d to %q, want 303 to %s", res.StatusCode, res.Header.Get("Location"), to)
	}
}

func TestSetupModeUntilThePasswordIsSet(t *testing.T) {
	s := newSite(t, "")
	res, page := s.get("/admin/")
	if res.StatusCode != 200 || !strings.Contains(page, `action="/admin/setup"`) || strings.Contains(page, `action="/admin/login"`) {
		t.Fatalf("without a password /admin/ must be the setup form: %d\n%s", res.StatusCode, page)
	}
	if strings.Contains(page, "/admin/status") {
		t.Fatal("the setup form must not show the navigation")
	}
	res, _ = s.get("/admin/status")
	wantRedirect(t, res, "/admin/")
	token := s.token(page)

	res, body := s.post("/admin/setup", url.Values{"csrf": {token}, "password": {"one"}, "confirm": {"two"}})
	if res.StatusCode != 400 || !strings.Contains(body, "do not match") {
		t.Fatalf("mismatched passwords = %d\n%s", res.StatusCode, body)
	}
	res, body = s.post("/admin/setup", url.Values{"csrf": {token}, "password": {""}, "confirm": {""}})
	if res.StatusCode != 400 || !strings.Contains(body, "must not be empty") {
		t.Fatalf("empty password = %d\n%s", res.StatusCode, body)
	}
	long := strings.Repeat("x", MaxPasswordLen+1)
	if res, _ = s.post("/admin/setup", url.Values{"csrf": {token}, "password": {long}, "confirm": {long}}); res.StatusCode != 400 {
		t.Fatalf("over-long password = %d", res.StatusCode)
	}
	if s.pw.IsSet() {
		t.Fatal("refused setups must not set a password")
	}
	if res, _ = s.post("/admin/setup", url.Values{"password": {"hunter2"}, "confirm": {"hunter2"}}); res.StatusCode != 403 {
		t.Fatalf("setup without the form token = %d, want 403", res.StatusCode)
	}

	res, _ = s.post("/admin/setup", url.Values{"csrf": {token}, "password": {"hunter2"}, "confirm": {"hunter2"}})
	wantRedirect(t, res, "/admin/status")
	if !s.pw.IsSet() || !s.pw.Check("hunter2") {
		t.Fatal("setup did not set the password")
	}
	if _, err := os.Stat(filepath.Join(s.dir, "server.json")); err != nil {
		t.Fatalf("server.json after setup: %v", err)
	}
	// Setup logs the browser in and says so once.
	res, body = s.get("/admin/status")
	if res.StatusCode != 200 || !strings.Contains(body, "Password set.") || !strings.Contains(body, "Clients and keys") {
		t.Fatalf("status after setup = %d\n%s", res.StatusCode, body)
	}
	if _, body = s.get("/admin/status"); strings.Contains(body, "Password set.") {
		t.Fatal("the message must show only once")
	}
	res, _ = s.get("/admin/")
	wantRedirect(t, res, "/admin/status")

	// Setup is over: the form is the login form now, and a second setup
	// cannot replace the password.
	other := &site{t: t, url: s.url, c: newBrowser(t)}
	res, page = other.get("/admin/")
	if res.StatusCode != 200 || !strings.Contains(page, `action="/admin/login"`) {
		t.Fatalf("after setup /admin/ must be the login form: %d\n%s", res.StatusCode, page)
	}
	res, _ = other.post("/admin/setup", url.Values{"csrf": {other.token(page)}, "password": {"evil"}, "confirm": {"evil"}})
	wantRedirect(t, res, "/admin/")
	if s.pw.Check("evil") || !s.pw.Check("hunter2") {
		t.Fatal("a second setup must not change the password")
	}
}

func TestLoginLogoutAndPages(t *testing.T) {
	s := newSite(t, "secret")
	res, page := s.get("/admin/")
	if res.StatusCode != 200 || !strings.Contains(page, `action="/admin/login"`) {
		t.Fatalf("login form = %d\n%s", res.StatusCode, page)
	}
	cookies := res.Cookies()
	if len(cookies) != 1 || cookies[0].Name != "tam_admin" || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/admin" || cookies[0].Secure || len(cookies[0].Value) != 64 {
		t.Fatalf("session cookie = %+v", cookies)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("headers = %v", res.Header)
	}
	if _, page2 := s.get("/admin/"); s.token(page2) != s.token(page) {
		t.Fatal("the same session must keep its form token")
	}

	res, _ = s.get("/admin/status")
	wantRedirect(t, res, "/admin/")
	res, _ = s.get("/admin/backup/download")
	wantRedirect(t, res, "/admin/")

	token := s.token(page)
	res, body := s.post("/admin/login", url.Values{"csrf": {token}, "password": {"wrong"}})
	if res.StatusCode != 401 || !strings.Contains(body, "Wrong password.") || !strings.Contains(body, `action="/admin/login"`) {
		t.Fatalf("wrong password = %d\n%s", res.StatusCode, body)
	}
	res, _ = s.post("/admin/login", url.Values{"csrf": {token}, "password": {"secret"}})
	wantRedirect(t, res, "/admin/status")
	loggedIn := res.Cookies()
	if len(loggedIn) != 1 || loggedIn[0].Value == cookies[0].Value || loggedIn[0].MaxAge < 12*3600-5 || loggedIn[0].MaxAge > 12*3600 {
		t.Fatalf("a login must issue a fresh 12 h session cookie: %+v", loggedIn)
	}
	if s.h.ss.get(cookies[0].Value) != nil {
		t.Fatal("the pre-login session must be gone after the login")
	}

	body, _ = s.page("/admin/status")
	for _, want := range []string{":8000", "off", s.dir, "0.0.1", "1 min 30 s", "No client has paired yet", "Log out", `href="/admin/keys"`} {
		if !strings.Contains(body, want) {
			t.Errorf("status page lacks %q:\n%s", want, body)
		}
	}
	res, _ = s.get("/admin/")
	wantRedirect(t, res, "/admin/status")
	res, _ = s.get("/admin")
	wantRedirect(t, res, "/admin/status")

	_, token = s.page("/admin/keys")
	res, _ = s.post("/admin/logout", url.Values{"csrf": {token}})
	wantRedirect(t, res, "/admin/")
	if c := res.Cookies(); len(c) != 1 || c[0].MaxAge >= 0 {
		t.Fatalf("logout must clear the cookie: %+v", c)
	}
	res, _ = s.get("/admin/status")
	wantRedirect(t, res, "/admin/")

	// GET /admin/logout works too, for a plain link.
	s.login("secret")
	res, _ = s.get("/admin/logout")
	wantRedirect(t, res, "/admin/")
	res, _ = s.get("/admin/status")
	wantRedirect(t, res, "/admin/")
}

func TestFormsNeedTheSessionToken(t *testing.T) {
	s := newSite(t, "secret")
	_, page := s.get("/admin/")
	for name, form := range map[string]url.Values{
		"no token":    {"password": {"secret"}},
		"wrong token": {"csrf": {strings.Repeat("0", 64)}, "password": {"secret"}},
	} {
		res, body := s.post("/admin/login", form)
		if res.StatusCode != 403 || !strings.Contains(body, "form token") {
			t.Fatalf("login with %s = %d\n%s", name, res.StatusCode, body)
		}
	}
	// A token from another browser's session does not work either.
	other := &site{t: t, url: s.url, c: newBrowser(t)}
	_, otherPage := other.get("/admin/")
	if res, _ := s.post("/admin/login", url.Values{"csrf": {other.token(otherPage)}, "password": {"secret"}}); res.StatusCode != 403 {
		t.Fatalf("login with another session's token = %d", res.StatusCode)
	}
	if res, _ := s.post("/admin/login", url.Values{"csrf": {s.token(page)}, "password": {"secret"}}); res.StatusCode != 303 {
		t.Fatalf("login with the right token = %d", res.StatusCode)
	}

	for _, path := range []string{"/admin/keys", "/admin/keys/delete", "/admin/password", "/admin/logout", "/admin/backup/restore"} {
		res, _ := s.post(path, url.Values{"description": {"x"}, "key": {"x"}, "confirm": {"yes"}})
		if res.StatusCode != 403 {
			t.Errorf("POST %s without a token = %d, want 403", path, res.StatusCode)
		}
	}
	if keys, _ := s.st.ListKeys(); len(keys) != 0 {
		t.Fatal("a refused form must not create a key")
	}
	if res, _ := s.get("/admin/status"); res.StatusCode != 200 {
		t.Fatal("a refused logout must keep the session")
	}
	// A POST without a body is refused as well, and never panics.
	req, _ := http.NewRequest("POST", s.url+"/admin/keys", nil)
	res, err := s.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("empty POST = %d, want 403", res.StatusCode)
	}
}

func TestFiveWrongPasswordsWaitThirtySeconds(t *testing.T) {
	s := newSite(t, "secret")
	_, page := s.get("/admin/")
	token := s.token(page)
	for i := 0; i < 5; i++ {
		if res, _ := s.post("/admin/login", url.Values{"csrf": {token}, "password": {"wrong"}}); res.StatusCode != 401 {
			t.Fatalf("wrong password %d = %d, want 401", i+1, res.StatusCode)
		}
	}
	res, body := s.post("/admin/login", url.Values{"csrf": {token}, "password": {"secret"}})
	if res.StatusCode != 429 || res.Header.Get("Retry-After") != "30" || !strings.Contains(body, "Wait 30 seconds") {
		t.Fatalf("sixth attempt = %d %q\n%s", res.StatusCode, res.Header.Get("Retry-After"), body)
	}
	if res, _ := s.get("/admin/status"); res.StatusCode != 303 {
		t.Fatal("a refused login must not log in")
	}

	// Once the wait is over the right password works.
	s.h.guesses.Now = func() time.Time { return time.Now().Add(31 * time.Second) }
	res, _ = s.post("/admin/login", url.Values{"csrf": {token}, "password": {"secret"}})
	wantRedirect(t, res, "/admin/status")
}

// TestWrongPasswordsSentAtOnceAreCountedFirst: wrong passwords sent all at
// once must not all be checked before the first of them is counted. With a
// password from server.json every check takes a while (bcrypt), long enough
// for the others to arrive; still five are checked and the rest wait.
func TestWrongPasswordsSentAtOnceAreCountedFirst(t *testing.T) {
	s := newSite(t, "")
	if err := s.pw.Set("secret"); err != nil {
		t.Fatal(err)
	}
	_, page := s.get("/admin/")
	token := s.token(page)
	const n = 40
	var (
		mu    sync.Mutex
		count = map[int]int{}
		errs  []error
		wg    sync.WaitGroup
		start = make(chan struct{})
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := s.c.PostForm(s.url+"/admin/login", url.Values{"csrf": {token}, "password": {fmt.Sprintf("wrong-%d", i)}})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			count[res.StatusCode]++
		}()
	}
	close(start)
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("%d of %d logins failed, the first: %v", len(errs), n, errs[0])
	}
	if count[401] != 5 || count[429] != n-5 {
		t.Fatalf("%d wrong passwords sent at once were answered %v; want 5 checked (401) and %d refused (429)", n, count, n-5)
	}
}

// TestGuessesAreSharedWithTheAPI: TAM-PW on the API's key routes checks the
// same password as the login, so both count against one limit, as
// tam-server wires them: three wrong guesses through the API and two
// through the login form use up an address's five.
func TestGuessesAreSharedWithTheAPI(t *testing.T) {
	guesses := guard.New()
	s := newSite(t, "secret", WithGuesses(guesses))
	api := httptest.NewServer(server.NewHandler(s.st, s.pw, server.WithGuesses(guesses)))
	t.Cleanup(api.Close)
	apiAuth := func(password string) int {
		t.Helper()
		req, _ := http.NewRequest("GET", api.URL+"/api/auth", nil)
		req.Header.Set("TAM-PW", password)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for i := 0; i < 3; i++ {
		if code := apiAuth("wrong"); code != 401 {
			t.Fatalf("wrong password %d through the API = %d", i+1, code)
		}
	}
	_, page := s.get("/admin/")
	token := s.token(page)
	for i := 0; i < 2; i++ {
		if res, _ := s.post("/admin/login", url.Values{"csrf": {token}, "password": {"wrong"}}); res.StatusCode != 401 {
			t.Fatalf("wrong password %d through the login = %d", i+1, res.StatusCode)
		}
	}
	if res, _ := s.post("/admin/login", url.Values{"csrf": {token}, "password": {"secret"}}); res.StatusCode != 429 {
		t.Fatalf("the login after five wrong passwords in all = %d, want 429", res.StatusCode)
	}
	if code := apiAuth("secret"); code != 429 {
		t.Fatalf("the API after five wrong passwords in all = %d, want 429", code)
	}
}

// TestTheCurrentPasswordFieldIsLimited: the password form checks the current
// password, so it counts wrong ones like the login does; a stolen session
// cannot guess the password there without limit.
func TestTheCurrentPasswordFieldIsLimited(t *testing.T) {
	s := newSite(t, "secret")
	s.login("secret")
	_, token := s.page("/admin/password")
	change := url.Values{"csrf": {token}, "current": {"nope"}, "password": {"new one"}, "confirm": {"new one"}}
	for i := 0; i < 5; i++ {
		if res, body := s.post("/admin/password", change); res.StatusCode != 400 || !strings.Contains(body, "The current password is wrong.") {
			t.Fatalf("wrong current password %d = %d\n%s", i+1, res.StatusCode, body)
		}
	}
	change.Set("current", "secret")
	res, body := s.post("/admin/password", change)
	if res.StatusCode != 429 || res.Header.Get("Retry-After") != "30" || !strings.Contains(body, "Wait 30 seconds") {
		t.Fatalf("the right current password after five wrong ones = %d %q\n%s", res.StatusCode, res.Header.Get("Retry-After"), body)
	}
	if !s.pw.Check("secret") {
		t.Fatal("a refused change must keep the password")
	}
}

func TestKeysPage(t *testing.T) {
	s := newSite(t, "secret")
	s.login("secret")
	body, token := s.page("/admin/keys")
	if !strings.Contains(body, "No keys yet.") {
		t.Fatalf("keys page:\n%s", body)
	}

	res, body := s.post("/admin/keys", url.Values{"csrf": {token}, "description": {"  "}})
	if res.StatusCode != 400 || !strings.Contains(body, "Give the client a name.") {
		t.Fatalf("blank description = %d\n%s", res.StatusCode, body)
	}
	res, body = s.post("/admin/keys", url.Values{"csrf": {token}, "description": {" Client <A> "}})
	if res.StatusCode != 200 {
		t.Fatalf("create key = %d\n%s", res.StatusCode, body)
	}
	m := regexp.MustCompile(`<p class="key">([A-Z0-9]{32})</p>`).FindStringSubmatch(body)
	if m == nil || !strings.Contains(body, "Client &lt;A&gt;") || !strings.Contains(body, "not shown again") {
		t.Fatalf("the new key must be shown once, escaped:\n%s", body)
	}
	key := m[1]
	if ok, _ := s.st.KeyExists(key); !ok {
		t.Fatal("the shown key is not in the store")
	}

	body, _ = s.page("/admin/keys")
	// The whole key appears once more, hidden in the delete form; the
	// visible cell shows the short form only.
	if strings.Count(body, key) != 1 || !strings.Contains(body, `name="key" value="`+key+`"`) || strings.Contains(body, ">"+key+"<") {
		t.Fatalf("the list must not display the whole key again:\n%s", body)
	}
	if !strings.Contains(body, "<code>"+key[:4]+"\u2026</code>") || !strings.Contains(body, "never") {
		t.Fatalf("the list lacks the short key or the last-seen time:\n%s", body)
	}
	if err := s.st.TouchKey(key); err != nil {
		t.Fatal(err)
	}
	body, _ = s.page("/admin/status")
	if !strings.Contains(body, "Client &lt;A&gt;") || !strings.Contains(body, "just now") {
		t.Fatalf("status must list the client with its last-seen time:\n%s", body)
	}

	// Deleting asks first, then deletes.
	res, body = s.post("/admin/keys/delete", url.Values{"csrf": {token}, "key": {key}})
	if res.StatusCode != 200 || !strings.Contains(body, "Delete the key for <strong>Client &lt;A&gt;</strong>") {
		t.Fatalf("delete without confirmation = %d\n%s", res.StatusCode, body)
	}
	if ok, _ := s.st.KeyExists(key); !ok {
		t.Fatal("the question must not delete anything")
	}
	res, _ = s.post("/admin/keys/delete", url.Values{"csrf": {token}, "key": {key}, "confirm": {"yes"}})
	wantRedirect(t, res, "/admin/keys")
	body, _ = s.page("/admin/keys")
	if !strings.Contains(body, "Deleted the key for Client &lt;A&gt;.") || !strings.Contains(body, "No keys yet.") {
		t.Fatalf("after the delete:\n%s", body)
	}
	if ok, _ := s.st.KeyExists(key); ok {
		t.Fatal("the key is still in the store")
	}
	res, _ = s.post("/admin/keys/delete", url.Values{"csrf": {token}, "key": {key}, "confirm": {"yes"}})
	wantRedirect(t, res, "/admin/keys")
	if body, _ = s.page("/admin/keys"); !strings.Contains(body, "already gone") {
		t.Fatalf("deleting a missing key:\n%s", body)
	}
	if res, body = s.post("/admin/keys/delete", url.Values{"csrf": {token}}); res.StatusCode != 400 || !strings.Contains(body, "Choose a key") {
		t.Fatalf("delete without a key = %d", res.StatusCode)
	}
}

func seed(t *testing.T, st *store.Store) {
	t.Helper()
	for _, err := range []error{
		st.UpsertPrefixes([]store.Prefix{{Prefix: "A", Color: "red", Weight: 1}}),
		st.UpsertTickets([]store.Ticket{{Prefix: "A", TID: 1, FirstName: "Amy", LastName: "Adams", PhoneNumber: "555", Pref: "CALL"}}),
		st.UpsertBaskets([]store.Basket{{Prefix: "A", BID: 1, Description: "Wine", Donors: "Smiths"}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// upload posts a multipart restore form.
func (s *site) upload(token string, file []byte, confirm bool) (*http.Response, string) {
	s.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("csrf", token)
	if confirm {
		mw.WriteField("confirm", "yes")
	}
	if file != nil {
		fw, _ := mw.CreateFormFile("file", "tam-backup.json")
		fw.Write(file)
	}
	mw.Close()
	res, err := s.c.Post(s.url+"/admin/backup/restore", mw.FormDataContentType(), &buf)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

func TestBackupDownloadAndRestore(t *testing.T) {
	src := newSite(t, "secret")
	seed(t, src.st)
	src.login("secret")
	body, _ := src.page("/admin/backup")
	if !strings.Contains(body, "1 prefixes, 1 tickets and 1 baskets") || !strings.Contains(body, `href="/admin/backup/download"`) {
		t.Fatalf("backup page:\n%s", body)
	}

	res, file := src.get("/admin/backup/download")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/json" || res.Header.Get("Content-Disposition") != `attachment; filename="tam-backup.json"` {
		t.Fatalf("download = %d %v", res.StatusCode, res.Header)
	}
	var bf store.BackupFile
	if err := json.Unmarshal([]byte(file), &bf); err != nil || len(bf.Prefixes) != 1 || len(bf.Tickets) != 1 || len(bf.Baskets) != 1 || bf.Tickets[0].FirstName != "Amy" {
		t.Fatalf("download body = %s (%v)", file, err)
	}
	want, _ := src.st.Export()
	if wantJSON, _ := json.Marshal(want); strings.TrimSpace(file) != string(wantJSON) {
		t.Fatalf("the download must be store.Export():\n%s\n%s", file, wantJSON)
	}

	dst := newSite(t, "secret")
	dst.login("secret")
	_, token := dst.page("/admin/backup")
	res, body = dst.upload(token, []byte(file), false)
	if res.StatusCode != 400 || !strings.Contains(body, "Tick the box") {
		t.Fatalf("restore without confirmation = %d\n%s", res.StatusCode, body)
	}
	if _, tickets, _, _ := dst.st.Counts(); tickets != 0 {
		t.Fatal("an unconfirmed restore must not import")
	}
	res, body = dst.upload(token, nil, true)
	if res.StatusCode != 400 || !strings.Contains(body, "Choose a backup file.") {
		t.Fatalf("restore without a file = %d\n%s", res.StatusCode, body)
	}
	res, body = dst.upload(token, []byte("not json"), true)
	if res.StatusCode != 400 || !strings.Contains(body, "not a backup file") {
		t.Fatalf("restore of garbage = %d\n%s", res.StatusCode, body)
	}
	res, body = dst.upload(token, []byte(`{"prefixes":[{"prefix":"","color":"red","weight":1}]}`), true)
	if res.StatusCode != 400 || !strings.Contains(body, "cannot be restored") {
		t.Fatalf("restore of an invalid backup = %d\n%s", res.StatusCode, body)
	}

	res, _ = dst.upload(token, []byte(file), true)
	wantRedirect(t, res, "/admin/backup")
	body, _ = dst.page("/admin/backup")
	if !strings.Contains(body, "Restored 1 prefixes, 1 baskets and 1 tickets.") {
		t.Fatalf("after the restore:\n%s", body)
	}
	got, _ := dst.st.Export()
	if gotJSON, _ := json.Marshal(got); strings.TrimSpace(file) != string(gotJSON) {
		t.Fatalf("restored data differs:\n%s\n%s", gotJSON, file)
	}
}

func TestChangePassword(t *testing.T) {
	s := newSite(t, "secret")
	s.login("secret")
	body, token := s.page("/admin/password")
	if !strings.Contains(body, "comes from TAM_PWD") {
		t.Fatalf("the page must say where the password comes from:\n%s", body)
	}
	for name, form := range map[string]url.Values{
		"wrong current": {"current": {"nope"}, "password": {"new"}, "confirm": {"new"}},
		"mismatch":      {"current": {"secret"}, "password": {"new"}, "confirm": {"newer"}},
		"empty":         {"current": {"secret"}, "password": {""}, "confirm": {""}},
	} {
		form.Set("csrf", token)
		if res, _ := s.post("/admin/password", form); res.StatusCode != 400 {
			t.Errorf("%s = %d, want 400", name, res.StatusCode)
		}
	}
	if !s.pw.Check("secret") {
		t.Fatal("refused changes must keep the password")
	}

	res, _ := s.post("/admin/password", url.Values{"csrf": {token}, "current": {"secret"}, "password": {"new one"}, "confirm": {"new one"}})
	wantRedirect(t, res, "/admin/password")
	body, _ = s.page("/admin/password")
	if !strings.Contains(body, "Password changed.") || strings.Contains(body, "comes from TAM_PWD") {
		t.Fatalf("after the change:\n%s", body)
	}
	if !s.pw.Check("new one") || s.pw.Check("secret") {
		t.Fatal("the password did not change")
	}
	reloaded, err := Load(s.dir, "secret")
	if err != nil || !reloaded.Check("new one") || reloaded.Check("secret") {
		t.Fatalf("server.json after the change: %v", err)
	}

	// The new password logs in; the session survives the change.
	if res, _ = s.get("/admin/status"); res.StatusCode != 200 {
		t.Fatal("the session must survive a password change")
	}
	fresh := &site{t: t, url: s.url, c: newBrowser(t)}
	_, page := fresh.get("/admin/")
	if res, _ = fresh.post("/admin/login", url.Values{"csrf": {fresh.token(page)}, "password": {"secret"}}); res.StatusCode != 401 {
		t.Fatalf("old password after the change = %d", res.StatusCode)
	}
	fresh.login("new one")
}

// TestChangingThePasswordLogsOutTheOtherSessions: whoever logged in with
// the old password, perhaps the person it was changed to keep out, is
// logged out; the browser that changed it stays in.
func TestChangingThePasswordLogsOutTheOtherSessions(t *testing.T) {
	s := newSite(t, "secret")
	s.login("secret")
	other := &site{t: t, url: s.url, h: s.h, c: newBrowser(t)}
	other.login("secret")
	if res, _ := other.get("/admin/status"); res.StatusCode != 200 {
		t.Fatal("the second browser is not logged in")
	}
	_, token := s.page("/admin/password")
	res, _ := s.post("/admin/password", url.Values{"csrf": {token}, "current": {"secret"}, "password": {"new one"}, "confirm": {"new one"}})
	wantRedirect(t, res, "/admin/password")
	res, _ = other.get("/admin/status")
	wantRedirect(t, res, "/admin/")
	if res, _ := s.get("/admin/status"); res.StatusCode != 200 {
		t.Fatalf("the browser that changed the password = %d, want still logged in", res.StatusCode)
	}
	if n := s.sessionCount(); n != 1 {
		t.Fatalf("%d sessions left after the change, want the one that made it", n)
	}
}

func TestUnknownPathsAndMethodsAreHTML(t *testing.T) {
	s := newSite(t, "secret")
	res, body := s.get("/admin/nope")
	if res.StatusCode != 404 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") || !strings.Contains(body, "There is no such page.") {
		t.Fatalf("unknown path = %d %q\n%s", res.StatusCode, res.Header.Get("Content-Type"), body)
	}
	res, body = s.get("/admin/login")
	if res.StatusCode != 405 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") || !strings.Contains(body, "not allowed") {
		t.Fatalf("GET on a POST route = %d\n%s", res.StatusCode, body)
	}
	s.login("secret")
	res, body = s.get("/admin/status/extra")
	if res.StatusCode != 404 || !strings.Contains(body, `href="/admin/keys"`) {
		t.Fatalf("unknown path while logged in = %d, want a 404 with the navigation\n%s", res.StatusCode, body)
	}
}

func TestSessionExpires(t *testing.T) {
	s := newSite(t, "secret")
	s.login("secret")
	s.h.ss.now = func() time.Time { return time.Now().Add(sessionLife + time.Minute) }
	res, _ := s.get("/admin/status")
	wantRedirect(t, res, "/admin/")
	if len(s.h.ss.byID) != 0 {
		t.Fatalf("expired sessions must be dropped, %d left", len(s.h.ss.byID))
	}
}

// sessionCount is how many sessions the table holds.
func (s *site) sessionCount() int {
	s.h.ss.mu.Lock()
	defer s.h.ss.mu.Unlock()
	return len(s.h.ss.byID)
}

// TestVisitsOfTheLoginFormKeepNoSession: anyone can open the login form,
// as often as they like, so a visit must not add to the session table:
// every visit used to add a session, and 100,000 of them made every page
// that creates one 7 times slower and took 40 MB. The form's token is
// still tied to the visitor's cookie (TestFormsNeedTheSessionToken).
func TestVisitsOfTheLoginFormKeepNoSession(t *testing.T) {
	for _, env := range []string{"secret", ""} { // the login form, and the setup form
		s := newSite(t, env)
		for i := 0; i < 200; i++ {
			visitor := &site{t: t, url: s.url, c: newBrowser(t)}
			if res, page := visitor.get("/admin/"); res.StatusCode != 200 || len(res.Cookies()) != 1 || visitor.token(page) == "" {
				t.Fatalf("visit %d = %d, cookies %v", i+1, res.StatusCode, res.Cookies())
			}
		}
		if n := s.sessionCount(); n != 0 {
			t.Fatalf("200 visits of the form (password %q) left %d sessions", env, n)
		}
	}
	s := newSite(t, "secret")
	s.login("secret")
	if n := s.sessionCount(); n != 1 {
		t.Fatalf("after a login the table holds %d sessions, want the logged-in one", n)
	}
}

// TestTheFormTokenOfAVisitExpires: the token of an anonymous visit is good
// for an hour, like the session it replaces, and a cookie the server did
// not hand out gets a new one rather than a token of the visitor's choice.
func TestTheFormTokenOfAVisitExpires(t *testing.T) {
	s := newSite(t, "secret")
	res, page := s.get("/admin/")
	cookie, token := res.Cookies()[0].Value, s.token(page)
	s.h.ss.now = func() time.Time { return time.Now().Add(anonymousLife - time.Minute) }
	if _, again := s.get("/admin/"); s.token(again) != token {
		t.Fatal("within the hour the visit keeps its token")
	}
	s.h.ss.now = func() time.Time { return time.Now().Add(anonymousLife + time.Minute) }
	if res, body := s.post("/admin/login", url.Values{"csrf": {token}, "password": {"secret"}}); res.StatusCode != 403 || !strings.Contains(body, "expired") {
		t.Fatalf("login with an hour-old form = %d\n%s", res.StatusCode, body)
	}
	s.h.ss.now = time.Now

	far := cookie[:48] + "00000000ffffffff" // the same visit, good until the year 2106
	req, _ := http.NewRequest("GET", s.url+"/admin/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: far})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if c := res.Cookies(); len(c) != 1 || c[0].Value == far {
		t.Fatalf("a cookie the server never handed out was kept: %v", c)
	}
}

func TestCookieIsSecureOverTLS(t *testing.T) {
	dir := t.TempDir()
	sqldb, err := db.Open(filepath.Join(dir, "tam-remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	if err := db.Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	pw, _ := Load(dir, "secret")
	ts := httptest.NewTLSServer(NewHandler(store.New(sqldb), pw, Info{TLS: true}))
	defer ts.Close()
	res, err := ts.Client().Get(ts.URL + "/admin/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if c := res.Cookies(); len(c) != 1 || !c[0].Secure {
		t.Fatalf("cookie over TLS = %+v, want Secure", c)
	}
}

// statusDoc is what GET /admin/status answers to Accept: application/json.
type statusDoc struct {
	Uptime   string `json:"uptime"`
	Prefixes int    `json:"prefixes"`
	Tickets  int    `json:"tickets"`
	Baskets  int    `json:"baskets"`
	Clients  []struct {
		Name       string `json:"name"`
		Program    string `json:"program"`
		State      string `json:"state"`
		LastSeen   string `json:"last_seen"`
		LastUpdate string `json:"last_update"`
		Queued     *int   `json:"queued"`
	} `json:"clients"`
}

// getJSON fetches a page with Accept: application/json.
func (s *site) getJSON(path string) (*http.Response, string) {
	s.t.Helper()
	req, err := http.NewRequest("GET", s.url+path, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	res, err := s.c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

// stamp writes the persisted last_seen and last_update of a key, as an
// earlier run of the server would have left them.
func (s *site) stamp(key string, seen, update time.Time) {
	s.t.Helper()
	if _, err := s.sqldb.Exec(`INSERT INTO auth_key_activity (auth_key, last_seen, last_update) VALUES (?, ?, ?)
		ON CONFLICT (auth_key) DO UPDATE SET last_seen = excluded.last_seen, last_update = excluded.last_update`,
		key, seen.UTC().Format(time.RFC3339), update.UTC().Format(time.RFC3339)); err != nil {
		s.t.Fatal(err)
	}
}

// row is a Clients table row as the status page renders it.
func row(cells ...string) string {
	return "<tr><td>" + strings.Join(cells, "</td><td>") + "</td></tr>"
}

func TestStatusListsClients(t *testing.T) {
	s := newSite(t, "secret")
	s.login("secret")
	base := time.Now().Truncate(time.Second) // stored stamps have whole seconds
	s.h.ss.now = func() time.Time { return base }
	s.h.info.Started = base.Add(-90 * time.Second) // the uptime is measured from the same clock
	at := func(t time.Time) string { return t.Local().Format("2006-01-02 15:04") }
	const dash = "\u2013"

	// Three clients: one paired by hand and never seen, one the registry
	// knows, and one only the database remembers from before a restart.
	if _, err := s.st.CreateKey("Quiet <one>"); err != nil {
		t.Fatal(err)
	}
	busy, err := s.st.CreateKey("Busy")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.st.CreateKey("Stored")
	if err != nil {
		t.Fatal(err)
	}
	s.stamp(stored.AuthKey, base.Add(-40*time.Second), base.Add(-time.Hour))
	s.reg.Heartbeat(busy.AuthKey, "tam-client/1.2.3", 0)

	body, _ := s.page("/admin/status")
	if !strings.Contains(body, `<meta http-equiv="refresh" content="5">`) {
		t.Fatalf("the status page must reload itself:\n%s", body)
	}
	if !strings.Contains(body, "<h2>Clients</h2>") {
		t.Fatalf("the table is called Clients:\n%s", body)
	}
	for _, want := range []string{
		row("Busy", "tam-client/1.2.3", "connected", at(base)+" (just now)", "never", "0"),
		row("Quiet &lt;one&gt;", dash, "never", "never", "never", dash),
		row("Stored", dash, "away for 40 s", at(base.Add(-40*time.Second))+" (just now)", at(base.Add(-time.Hour))+" (1 h ago)", dash),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("status page lacks the row %s:\n%s", want, body)
		}
	}

	// An accepted write and a heartbeat with a queue show up, and the
	// values in memory win over what the database remembers.
	s.stamp(busy.AuthKey, base.Add(-24*time.Hour), base.Add(-24*time.Hour))
	s.reg.Updated(busy.AuthKey)
	s.reg.Heartbeat(busy.AuthKey, "tam-client/1.2.3", 2)
	body, _ = s.page("/admin/status")
	if want := row("Busy", "tam-client/1.2.3", "connected", at(base)+" (just now)", at(base)+" (just now)", "2"); !strings.Contains(body, want) {
		t.Fatalf("status page lacks the row %s:\n%s", want, body)
	}

	// Connected means seen within 15 s; after that the client is away.
	s.h.ss.now = func() time.Time { return base.Add(15 * time.Second) }
	if body, _ = s.page("/admin/status"); !strings.Contains(body, row("Busy", "tam-client/1.2.3", "connected", at(base)+" (just now)", at(base)+" (just now)", "2")) {
		t.Fatalf("at 15 s the client is still connected:\n%s", body)
	}
	s.h.ss.now = func() time.Time { return base.Add(16 * time.Second) }
	if body, _ = s.page("/admin/status"); !strings.Contains(body, row("Busy", "tam-client/1.2.3", "away for 16 s", at(base)+" (just now)", at(base)+" (just now)", "2")) {
		t.Fatalf("at 16 s the client is away:\n%s", body)
	}
	s.h.ss.now = func() time.Time { return base.Add(2*time.Minute + 3*time.Second) }
	body, _ = s.page("/admin/status")
	if !strings.Contains(body, row("Busy", "tam-client/1.2.3", "away for 2 min 3 s", at(base)+" (2 min ago)", at(base)+" (2 min ago)", "2")) {
		t.Fatalf("after two minutes:\n%s", body)
	}
	if !strings.Contains(body, row("Stored", dash, "away for 2 min 43 s", at(base.Add(-40*time.Second))+" (2 min ago)", at(base.Add(-time.Hour))+" (1 h ago)", dash)) {
		t.Fatalf("the stored client after two minutes:\n%s", body)
	}

	// The same as JSON, for scripts.
	res, text := s.getJSON("/admin/status")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("JSON status = %d %q\n%s", res.StatusCode, res.Header.Get("Content-Type"), text)
	}
	var doc statusDoc
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("JSON status: %v\n%s", err, text)
	}
	if doc.Uptime != "3 min 33 s" || doc.Prefixes != 0 || len(doc.Clients) != 3 {
		t.Fatalf("JSON status = %+v", doc)
	}
	b, q, st := doc.Clients[0], doc.Clients[1], doc.Clients[2]
	if b.Name != "Busy" || b.Program != "tam-client/1.2.3" || b.State != "away for 2 min 3 s" || b.LastSeen != at(base)+" (2 min ago)" || b.LastUpdate != at(base)+" (2 min ago)" || b.Queued == nil || *b.Queued != 2 {
		t.Fatalf("JSON row for Busy = %+v", b)
	}
	if q.Name != "Quiet <one>" || q.Program != "" || q.State != "never" || q.LastSeen != "never" || q.LastUpdate != "never" || q.Queued != nil {
		t.Fatalf("JSON row for Quiet = %+v", q)
	}
	if st.Name != "Stored" || st.Program != "" || st.State != "away for 2 min 43 s" || st.Queued != nil {
		t.Fatalf("JSON row for Stored = %+v", st)
	}
	seed(t, s.st)
	if _, text = s.getJSON("/admin/status"); !strings.Contains(text, `"prefixes":1,"tickets":1,"baskets":1`) {
		t.Fatalf("JSON counts:\n%s", text)
	}

	// Without a login the JSON is a 401, not a redirect to the login form.
	fresh := &site{t: t, url: s.url, c: newBrowser(t)}
	res, text = fresh.getJSON("/admin/status")
	if res.StatusCode != 401 || res.Header.Get("Content-Type") != "application/json" || strings.TrimSpace(text) != `{"detail":"Not logged in"}` {
		t.Fatalf("JSON status without a login = %d %q %s", res.StatusCode, res.Header.Get("Content-Type"), text)
	}
	if res, _ = fresh.get("/admin/status"); res.StatusCode != 303 {
		t.Fatalf("HTML status without a login = %d, want the redirect", res.StatusCode)
	}

	// Only the status page reloads itself.
	if body, _ = s.page("/admin/keys"); strings.Contains(body, `http-equiv="refresh"`) {
		t.Fatal("the keys page must not reload itself")
	}
}

// TestStatusWithoutARegistry: with no registry wired in, the page shows
// what the database remembers.
func TestStatusWithoutARegistry(t *testing.T) {
	dir := t.TempDir()
	sqldb, err := db.Open(filepath.Join(dir, "tam-remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	if err := db.Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateServer(sqldb); err != nil {
		t.Fatal(err)
	}
	st := store.New(sqldb)
	if _, err := st.CreateKey("client"); err != nil {
		t.Fatal(err)
	}
	pw, _ := Load(dir, "secret")
	ts := httptest.NewServer(NewHandler(st, pw, Info{Started: time.Now()}))
	defer ts.Close()
	s := &site{t: t, url: ts.URL, c: newBrowser(t)}
	s.login("secret")
	if body, _ := s.page("/admin/status"); !strings.Contains(body, row("client", "\u2013", "never", "never", "never", "\u2013")) {
		t.Fatalf("status without a registry shows what the database has:\n%s", body)
	}
}

// TestLogoAndIcon: every page shows the TAM logo and names the TAM icon, its
// policy lets them load, and they are the web app's own files.
func TestLogoAndIcon(t *testing.T) {
	s := newSite(t, "secret")
	for _, c := range []struct{ path, contentType, file string }{
		{"/admin/logo.svg", "image/svg+xml", "logo.svg"},
		{"/admin/favicon.ico", "image/x-icon", "favicon.ico"},
		{"/favicon.ico", "image/x-icon", "favicon.ico"}, // asked for by pages that name no icon
	} {
		want, err := os.ReadFile(filepath.Join("static", c.file))
		if err != nil {
			t.Fatal(err)
		}
		res, body := s.get(c.path)
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != c.contentType || body != string(want) {
			t.Fatalf("%s = %d %q, %d bytes; want the %d bytes of static/%s", c.path, res.StatusCode, res.Header.Get("Content-Type"), len(body), len(want), c.file)
		}
		if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "max-age") {
			t.Fatalf("%s: Cache-Control %q, want it cached unlike the pages", c.path, cc)
		}
	}
	res, page := s.get("/admin/")
	if !strings.Contains(page, `<link rel="icon" href="/admin/favicon.ico">`) || !strings.Contains(page, `src="/admin/logo.svg"`) {
		t.Fatalf("the login page must name the icon and show the logo:\n%s", page)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self'") {
		t.Fatalf("Content-Security-Policy %q blocks the logo", csp)
	}
	s.login("secret")
	if page, _ := s.page("/admin/status"); !strings.Contains(page, `src="/admin/logo.svg"`) {
		t.Fatalf("the status page must show the logo:\n%s", page)
	}

	// The server keeps copies of the web app's logo and icon; they must not drift.
	for file, original := range map[string]string{
		"static/logo.svg":    "../../frontend/src/lib/assets/logo.svg",
		"static/favicon.ico": "../../frontend/static/favicon.ico",
	} {
		a, errA := os.ReadFile(file)
		b, errB := os.ReadFile(original)
		if errA != nil || errB != nil || !bytes.Equal(a, b) {
			t.Fatalf("%s must be a copy of %s (%v, %v)", file, original, errA, errB)
		}
	}
}
