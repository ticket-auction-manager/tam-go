package admin

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ticket-auction-manager/tam-go/internal/guard"
	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/presence"
	"ticket-auction-manager/tam-go/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

// staticFS holds the TAM logo and icon, copies of the web app's
// (frontend/src/lib/assets/logo.svg and frontend/static/favicon.ico).
//
//go:embed static
var staticFS embed.FS

// pages holds one parsed template set per page: the layout plus the page's
// title and content blocks.
var pages = parsePages("login", "setup", "status", "keys", "backup", "password", "error")

func parsePages(names ...string) map[string]*template.Template {
	out := map[string]*template.Template{}
	for _, name := range names {
		out[name] = template.Must(template.ParseFS(templateFS, "templates/layout.html", "templates/"+name+".html"))
	}
	return out
}

// Info is what the status page shows about the running server.
type Info struct {
	Addr      string    // the listen address
	Addresses []string  // the addresses clients can reach this machine at
	TLS       bool      // whether the server speaks HTTPS
	DataDir   string    // where the database, log and server.json live
	Version   string    // the program version
	Started   time.Time // when the server started, for the uptime

	// Presence is the API's record of what each client last did, for the
	// Clients table. Without it the table shows what the database
	// remembers.
	Presence *presence.Registry
}

// connectedWithin is how recently a client must have been seen to count as
// connected: three of the client's 5 s heartbeats.
const connectedWithin = 15 * time.Second

type handler struct {
	st      *store.Store
	pw      *Password
	info    Info
	ss      *sessions
	guesses *guard.Limiter
	mux     *http.ServeMux
}

// Option configures NewHandler.
type Option func(*handler)

// WithGuesses sets the limit on wrong passwords, per address, that the
// login and the password form count against. Give the API the same one
// (server.WithGuesses), so guesses cannot be split between the two; without
// the option the pages keep a limit of their own.
func WithGuesses(l *guard.Limiter) Option {
	return func(h *handler) {
		if l != nil {
			h.guesses = l
		}
	}
}

// NewHandler returns the admin pages, served under /admin. GET /admin/ is
// the login form, or the one-time setup form while no password is set.
// Every other page needs a session; every POST needs the session's form
// token. Unknown paths under /admin answer an HTML 404.
func NewHandler(st *store.Store, pw *Password, info Info, opts ...Option) http.Handler {
	h := &handler{st: st, pw: pw, info: info, ss: newSessions(), guesses: guard.New(), mux: http.NewServeMux()}
	for _, opt := range opts {
		opt(h)
	}
	h.mux.HandleFunc("GET /admin", h.home)
	h.mux.HandleFunc("GET /admin/{$}", h.home)
	h.mux.HandleFunc("POST /admin/login", h.login)
	h.mux.HandleFunc("POST /admin/setup", h.setup)
	h.mux.HandleFunc("GET /admin/logout", h.logout)
	h.mux.HandleFunc("POST /admin/logout", h.logout)
	h.mux.HandleFunc("GET /admin/logo.svg", asset("static/logo.svg", "image/svg+xml"))
	h.mux.HandleFunc("GET /admin/favicon.ico", asset("static/favicon.ico", "image/x-icon"))
	h.mux.HandleFunc("GET /favicon.ico", asset("static/favicon.ico", "image/x-icon"))

	in := h.loggedIn
	h.mux.Handle("GET /admin/status", in(h.status))
	h.mux.Handle("GET /admin/keys", in(h.keys))
	h.mux.Handle("POST /admin/keys", in(h.createKey))
	h.mux.Handle("POST /admin/keys/delete", in(h.deleteKey))
	h.mux.Handle("GET /admin/backup", in(h.backup))
	h.mux.Handle("GET /admin/backup/download", in(h.download))
	h.mux.Handle("POST /admin/backup/restore", in(h.restore))
	h.mux.Handle("GET /admin/password", in(h.passwordForm))
	h.mux.Handle("POST /admin/password", in(h.changePassword))
	return h
}

// ServeHTTP adds the headers every page needs, caps request bodies and
// turns the mux's plain-text 404 and 405 into pages.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, httpx.MaxBody)
	}
	if _, pattern := h.mux.Handler(r); pattern != "" {
		h.mux.ServeHTTP(w, r)
		return
	}
	probe := &statusProbe{ResponseWriter: w}
	h.mux.ServeHTTP(probe, r)
	switch {
	case probe.status/100 == 3:
		w.WriteHeader(probe.status)
	case probe.status == http.StatusMethodNotAllowed:
		h.errorPage(w, r, http.StatusMethodNotAllowed)
	default:
		h.errorPage(w, r, http.StatusNotFound)
	}
}

// asset serves one of the embedded images, which every page shows whether
// or not the visitor is logged in; unlike the pages it may be cached.
func asset(name, contentType string) http.HandlerFunc {
	data, err := staticFS.ReadFile(name)
	if err != nil {
		panic(err) // embedded when the program is built
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(data)
	}
}

// statusProbe records the status the mux would send and drops its body.
type statusProbe struct {
	http.ResponseWriter
	status int
}

func (p *statusProbe) WriteHeader(code int) { p.status = code }

func (p *statusProbe) Write(b []byte) (int, error) {
	if p.status == 0 {
		p.status = http.StatusOK
	}
	return len(b), nil
}

// --- rendering ---

// view is what every template receives.
type view struct {
	LoggedIn bool   // show the navigation
	Active   string // the navigation entry of this page
	CSRF     string // the session's form token
	Message  string // a success message
	Error    string // an error message
	Data     any    // the page's own data
}

// keyRow is a key as the pages show it.
type keyRow struct {
	Description string
	Key         string // the whole key, for the delete form
	Short       string // enough of the key to tell rows apart
	LastSeen    string
}

// clientRow is one paired client as the status page and its JSON show it.
type clientRow struct {
	Name       string `json:"name"`        // the key's description, the client's name
	Program    string `json:"program"`     // the program and its version, "" when unknown
	State      string `json:"state"`       // connected, away for ..., or never
	LastSeen   string `json:"last_seen"`   // as formatSeen writes it
	LastUpdate string `json:"last_update"` // as formatSeen writes it
	Queued     *int   `json:"queued"`      // nil when the client never sent a heartbeat
}

type statusData struct {
	Info
	Uptime                     string
	Prefixes, Tickets, Baskets int
	Clients                    []clientRow
}

// statusJSON is the status page for scripts.
type statusJSON struct {
	Uptime   string      `json:"uptime"`
	Prefixes int         `json:"prefixes"`
	Tickets  int         `json:"tickets"`
	Baskets  int         `json:"baskets"`
	Clients  []clientRow `json:"clients"`
}

type keysData struct {
	Keys    []keyRow
	NewKey  *store.AuthKey // shown once, right after creation
	Confirm *keyRow        // the key a delete is asking about
}

type backupData struct {
	Prefixes, Tickets, Baskets int
}

type passwordData struct {
	FromEnv bool
}

type errorData struct {
	Title string
	Text  string
}

func (h *handler) render(w http.ResponseWriter, status int, page string, v view) {
	var buf bytes.Buffer
	if err := pages[page].ExecuteTemplate(&buf, "layout.html", v); err != nil {
		log.Printf("admin: render %s: %v", page, err)
		http.Error(w, "Internal error; see the daemon log", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

// view builds the common part of a logged-in page and takes the session's
// pending message.
func (h *handler) view(s *session, active string, data any) view {
	return view{LoggedIn: true, Active: active, CSRF: s.csrf, Message: h.ss.takeFlash(s.id), Data: data}
}

var errorTexts = map[int]string{
	http.StatusForbidden:             "The form token is missing or has expired. Go back and try again.",
	http.StatusNotFound:              "There is no such page.",
	http.StatusMethodNotAllowed:      "That method is not allowed here.",
	http.StatusRequestEntityTooLarge: "The upload is too large.",
	http.StatusInternalServerError:   "Internal error; see the server log.",
}

func (h *handler) errorPage(w http.ResponseWriter, r *http.Request, status int) {
	s := h.ss.get(cookieID(r))
	v := view{LoggedIn: s != nil && s.loggedIn, Data: errorData{Title: http.StatusText(status), Text: errorTexts[status]}}
	if s != nil {
		v.CSRF = s.csrf
	}
	h.render(w, status, "error", v)
}

// internal logs err and answers a 500 page.
func (h *handler) internal(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("admin: internal error: %v", err)
	h.errorPage(w, r, http.StatusInternalServerError)
}

// tooMany answers 429 with the page again and how long the visitor's
// address has to wait before its next password.
func (h *handler) tooMany(w http.ResponseWriter, page string, v view, wait time.Duration) {
	secs := guard.Seconds(wait)
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	v.Error = fmt.Sprintf("Too many wrong passwords. Wait %d seconds and try again.", secs)
	h.render(w, http.StatusTooManyRequests, page, v)
}

// --- forms and sessions ---

// parseForm parses a URL-encoded or multipart form and answers the error
// page when it cannot.
func (h *handler) parseForm(w http.ResponseWriter, r *http.Request) bool {
	var err error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		err = r.ParseMultipartForm(32 << 20)
	} else {
		err = r.ParseForm()
	}
	if err == nil {
		return true
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		h.errorPage(w, r, http.StatusRequestEntityTooLarge)
	} else {
		h.errorPage(w, r, http.StatusBadRequest)
	}
	return false
}

// formSession parses the form and returns the session it belongs to, after
// checking the form token. It answers the error itself and returns nil
// when either is missing.
func (h *handler) formSession(w http.ResponseWriter, r *http.Request) *session {
	if !h.parseForm(w, r) {
		return nil
	}
	s := h.ss.visitor(cookieID(r))
	if s == nil || !s.validToken(r.PostFormValue("csrf")) {
		h.errorPage(w, r, http.StatusForbidden)
		return nil
	}
	return s
}

// wantsJSON reports whether the request asked for JSON, as a script does.
func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// loggedIn wraps a page that needs a login. A visitor without one is sent
// to the login form, or told so in JSON when that is what was asked for;
// a POST without the session's token is refused.
func (h *handler) loggedIn(next func(http.ResponseWriter, *http.Request, *session)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := h.ss.get(cookieID(r))
		if s == nil || !s.loggedIn {
			if wantsJSON(r) {
				httpx.WriteError(w, http.StatusUnauthorized, "Not logged in")
				return
			}
			http.Redirect(w, r, "/admin/", http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost {
			if s = h.formSession(w, r); s == nil {
				return
			}
			if !s.loggedIn { // logged out in between
				http.Redirect(w, r, "/admin/", http.StatusSeeOther)
				return
			}
		}
		next(w, r, s)
	})
}

// startSession replaces the visitor's session with a logged-in one, so a
// session id handed out before the login never becomes a logged-in one.
func (h *handler) startSession(w http.ResponseWriter, r *http.Request, old *session) *session {
	if old != nil {
		h.ss.delete(old.id)
	}
	s := h.ss.create()
	setCookie(w, r, s, h.ss.now())
	return s
}

// --- login and setup ---

func (h *handler) home(w http.ResponseWriter, r *http.Request) {
	s := h.ss.visitor(cookieID(r))
	if s != nil && s.loggedIn && h.pw.IsSet() {
		http.Redirect(w, r, "/admin/status", http.StatusSeeOther)
		return
	}
	if s == nil {
		s = h.ss.visit()
		setCookie(w, r, s, h.ss.now())
	}
	if !h.pw.IsSet() {
		h.render(w, http.StatusOK, "setup", view{CSRF: s.csrf})
		return
	}
	h.render(w, http.StatusOK, "login", view{CSRF: s.csrf})
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	if !h.pw.IsSet() {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
		return
	}
	s := h.formSession(w, r)
	if s == nil {
		return
	}
	addr := httpx.RemoteIP(r)
	right, wait := h.guesses.Check(addr, "admin", func() bool { return h.pw.Check(r.PostFormValue("password")) })
	switch {
	case wait > 0:
		h.tooMany(w, "login", view{CSRF: s.csrf}, wait)
		return
	case !right:
		h.render(w, http.StatusUnauthorized, "login", view{CSRF: s.csrf, Error: "Wrong password."})
		return
	}
	h.startSession(w, r, s)
	log.Printf("admin: login from %s", addr)
	http.Redirect(w, r, "/admin/status", http.StatusSeeOther)
}

func (h *handler) setup(w http.ResponseWriter, r *http.Request) {
	if h.pw.IsSet() {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
		return
	}
	s := h.formSession(w, r)
	if s == nil {
		return
	}
	password, confirm := r.PostFormValue("password"), r.PostFormValue("confirm")
	if err := ValidatePassword(password); err != nil {
		h.render(w, http.StatusBadRequest, "setup", view{CSRF: s.csrf, Error: err.Error()})
		return
	}
	if password != confirm {
		h.render(w, http.StatusBadRequest, "setup", view{CSRF: s.csrf, Error: "The passwords do not match."})
		return
	}
	if err := h.pw.Set(password); err != nil {
		log.Printf("admin: set password: %v", err)
		h.render(w, http.StatusInternalServerError, "setup", view{CSRF: s.csrf, Error: "Could not save the password; see the server log."})
		return
	}
	addr := httpx.RemoteIP(r)
	log.Printf("admin: password set from %s", addr)
	n := h.startSession(w, r, s)
	h.ss.setFlash(n.id, "Password set. Clients can pair with this server now.")
	http.Redirect(w, r, "/admin/status", http.StatusSeeOther)
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	s := h.ss.get(cookieID(r))
	if r.Method == http.MethodPost {
		if s = h.formSession(w, r); s == nil {
			return
		}
	}
	if s != nil {
		h.ss.delete(s.id)
	}
	clearCookie(w, r)
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

// --- status ---

func (h *handler) status(w http.ResponseWriter, r *http.Request, s *session) {
	prefixes, tickets, baskets, err := h.st.Counts()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	keys, err := h.st.ListKeys()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	now := h.ss.now()
	data := statusData{
		Info:     h.info,
		Uptime:   formatUptime(now.Sub(h.info.Started)),
		Prefixes: prefixes, Tickets: tickets, Baskets: baskets,
		Clients: clientRows(keys, h.snapshot(), now),
	}
	if wantsJSON(r) {
		httpx.WriteJSON(w, http.StatusOK, statusJSON{Uptime: data.Uptime, Prefixes: prefixes, Tickets: tickets, Baskets: baskets, Clients: data.Clients})
		return
	}
	h.render(w, http.StatusOK, "status", h.view(s, "status", data))
}

// snapshot returns the registry's records, or nothing without a registry.
func (h *handler) snapshot() map[string]presence.Record {
	if h.info.Presence == nil {
		return nil
	}
	return h.info.Presence.Snapshot()
}

// clientRows joins the keys with what the registry saw of each. The times
// in memory are exact and win; the persisted ones stand in after a
// restart until the client shows up again.
func clientRows(keys []store.AuthKey, live map[string]presence.Record, now time.Time) []clientRow {
	rows := make([]clientRow, 0, len(keys))
	for _, k := range keys {
		rec := live[k.AuthKey]
		seen := pick(rec.Seen, k.LastSeen)
		row := clientRow{
			Name:       k.Description,
			Program:    rec.Client,
			State:      stateOf(seen, now),
			LastSeen:   formatAgo(seen, now),
			LastUpdate: formatAgo(pick(rec.Updated, k.LastUpdate), now),
		}
		if rec.HasPending {
			pending := rec.Pending
			row.Queued = &pending
		}
		rows = append(rows, row)
	}
	return rows
}

// pick returns the live time when there is one, else the persisted RFC
// 3339 value, else the zero time.
func pick(live time.Time, persisted string) time.Time {
	if !live.IsZero() {
		return live
	}
	t, err := time.Parse(time.RFC3339, persisted)
	if err != nil {
		return time.Time{}
	}
	return t
}

// stateOf is the connection state of a client last seen at seen.
func stateOf(seen, now time.Time) string {
	switch {
	case seen.IsZero():
		return "never"
	case now.Sub(seen) <= connectedWithin:
		return "connected"
	}
	return "away for " + formatUptime(now.Sub(seen))
}

func keyRows(keys []store.AuthKey, now time.Time) []keyRow {
	rows := make([]keyRow, 0, len(keys))
	for _, k := range keys {
		short := k.AuthKey
		if len(short) > 4 {
			short = short[:4] + "…"
		}
		rows = append(rows, keyRow{Description: k.Description, Key: k.AuthKey, Short: short, LastSeen: formatSeen(k.LastSeen, now)})
	}
	return rows
}

// formatUptime writes a duration the way a person would say it.
func formatUptime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	minutes := int(d / time.Minute)
	seconds := int((d - time.Duration(minutes)*time.Minute) / time.Second)
	switch {
	case days > 0:
		return fmt.Sprintf("%d d %d h %d min", days, hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%d h %d min", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%d min %d s", minutes, seconds)
	}
	return fmt.Sprintf("%d s", seconds)
}

// formatSeen turns a persisted last_seen value into local time plus how
// long ago that was; "" is "never".
func formatSeen(seen string, now time.Time) string {
	if seen == "" {
		return "never"
	}
	t, err := time.Parse(time.RFC3339, seen)
	if err != nil {
		return seen
	}
	return formatAgo(t, now)
}

// formatAgo writes t as local time plus how long before now that was; the
// zero time is "never".
func formatAgo(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	ago := now.Sub(t)
	var rel string
	switch {
	case ago < time.Minute:
		rel = "just now"
	case ago < time.Hour:
		rel = fmt.Sprintf("%d min ago", int(ago/time.Minute))
	case ago < 24*time.Hour:
		rel = fmt.Sprintf("%d h ago", int(ago/time.Hour))
	default:
		rel = fmt.Sprintf("%d d ago", int(ago/(24*time.Hour)))
	}
	return t.Local().Format("2006-01-02 15:04") + " (" + rel + ")"
}

// --- keys ---

func (h *handler) keys(w http.ResponseWriter, r *http.Request, s *session) {
	h.renderKeys(w, r, s, http.StatusOK, keysData{}, "")
}

func (h *handler) renderKeys(w http.ResponseWriter, r *http.Request, s *session, status int, data keysData, errText string) {
	keys, err := h.st.ListKeys()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	data.Keys = keyRows(keys, h.ss.now())
	v := h.view(s, "keys", data)
	v.Error = errText
	h.render(w, status, "keys", v)
}

func (h *handler) createKey(w http.ResponseWriter, r *http.Request, s *session) {
	description := strings.TrimSpace(r.PostFormValue("description"))
	if description == "" {
		h.renderKeys(w, r, s, http.StatusBadRequest, keysData{}, "Give the client a name.")
		return
	}
	k, err := h.st.CreateKey(description)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	log.Printf("admin: key created for %q", description)
	h.renderKeys(w, r, s, http.StatusOK, keysData{NewKey: &k}, "")
}

func (h *handler) deleteKey(w http.ResponseWriter, r *http.Request, s *session) {
	key := r.PostFormValue("key")
	if key == "" {
		h.renderKeys(w, r, s, http.StatusBadRequest, keysData{}, "Choose a key to delete.")
		return
	}
	if r.PostFormValue("confirm") == "" {
		// First step: show which client this is and ask.
		keys, err := h.st.ListKeys()
		if err != nil {
			h.internal(w, r, err)
			return
		}
		for _, row := range keyRows(keys, h.ss.now()) {
			if row.Key == key {
				h.renderKeys(w, r, s, http.StatusOK, keysData{Confirm: &row}, "")
				return
			}
		}
		h.ss.setFlash(s.id, "That key was already gone.")
		http.Redirect(w, r, "/admin/keys", http.StatusSeeOther)
		return
	}
	gone, err := h.st.DeleteKey(key)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if gone == nil {
		h.ss.setFlash(s.id, "That key was already gone.")
	} else {
		log.Printf("admin: key deleted for %q", gone.Description)
		h.ss.setFlash(s.id, fmt.Sprintf("Deleted the key for %s.", gone.Description))
	}
	http.Redirect(w, r, "/admin/keys", http.StatusSeeOther)
}

// --- backup and restore ---

func (h *handler) backup(w http.ResponseWriter, r *http.Request, s *session) {
	h.renderBackup(w, r, s, http.StatusOK, "")
}

func (h *handler) renderBackup(w http.ResponseWriter, r *http.Request, s *session, status int, errText string) {
	prefixes, tickets, baskets, err := h.st.Counts()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	v := h.view(s, "backup", backupData{Prefixes: prefixes, Tickets: tickets, Baskets: baskets})
	v.Error = errText
	h.render(w, status, "backup", v)
}

func (h *handler) download(w http.ResponseWriter, r *http.Request, s *session) {
	bf, err := h.st.Export()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="tam-backup.json"`)
	if err := json.NewEncoder(w).Encode(bf); err != nil {
		log.Printf("admin: write backup: %v", err)
	}
}

func (h *handler) restore(w http.ResponseWriter, r *http.Request, s *session) {
	if r.PostFormValue("confirm") == "" {
		h.renderBackup(w, r, s, http.StatusBadRequest, "Tick the box to confirm that the file may overwrite this server's data.")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		h.renderBackup(w, r, s, http.StatusBadRequest, "Choose a backup file.")
		return
	}
	defer f.Close()
	var bf store.BackupFile
	if err := json.NewDecoder(f).Decode(&bf); err != nil {
		h.renderBackup(w, r, s, http.StatusBadRequest, "That is not a backup file: "+err.Error())
		return
	}
	if err := store.ValidateBackup(&bf); err != nil {
		h.renderBackup(w, r, s, http.StatusBadRequest, "That backup cannot be restored: "+err.Error())
		return
	}
	if err := h.st.Import(bf); err != nil {
		h.internal(w, r, err)
		return
	}
	log.Printf("admin: restored %d prefixes, %d baskets and %d tickets from an uploaded backup", len(bf.Prefixes), len(bf.Baskets), len(bf.Tickets))
	h.ss.setFlash(s.id, fmt.Sprintf("Restored %d prefixes, %d baskets and %d tickets.", len(bf.Prefixes), len(bf.Baskets), len(bf.Tickets)))
	http.Redirect(w, r, "/admin/backup", http.StatusSeeOther)
}

// --- password ---

func (h *handler) passwordForm(w http.ResponseWriter, r *http.Request, s *session) {
	h.render(w, http.StatusOK, "password", h.view(s, "password", passwordData{FromEnv: h.pw.FromEnv()}))
}

func (h *handler) changePassword(w http.ResponseWriter, r *http.Request, s *session) {
	fail := func(msg string) {
		v := h.view(s, "password", passwordData{FromEnv: h.pw.FromEnv()})
		v.Error = msg
		h.render(w, http.StatusBadRequest, "password", v)
	}
	current, password, confirm := r.PostFormValue("current"), r.PostFormValue("password"), r.PostFormValue("confirm")
	// The current password is a guess like any other: a stolen session must
	// not get to try passwords without limit here.
	addr := httpx.RemoteIP(r)
	right, wait := h.guesses.Check(addr, "admin", func() bool { return h.pw.Check(current) })
	switch {
	case wait > 0:
		h.tooMany(w, "password", h.view(s, "password", passwordData{FromEnv: h.pw.FromEnv()}), wait)
		return
	case !right:
		fail("The current password is wrong.")
		return
	}
	if err := ValidatePassword(password); err != nil {
		fail(err.Error())
		return
	}
	if password != confirm {
		fail("The new passwords do not match.")
		return
	}
	if err := h.pw.Set(password); err != nil {
		log.Printf("admin: change password: %v", err)
		v := h.view(s, "password", passwordData{FromEnv: h.pw.FromEnv()})
		v.Error = "Could not save the password; see the server log."
		h.render(w, http.StatusInternalServerError, "password", v)
		return
	}
	// Whoever logged in with the old password, perhaps the one it was
	// changed to keep out, is logged out; this browser stays in.
	h.ss.deleteOthers(s.id)
	log.Printf("admin: password changed from %s; every other session is logged out", addr)
	h.ss.setFlash(s.id, "Password changed. Every other browser logged in here is logged out.")
	http.Redirect(w, r, "/admin/password", http.StatusSeeOther)
}
