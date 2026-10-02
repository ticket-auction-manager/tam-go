// Package server is the HTTP API of tam-server, the shared database that
// several tam-client installations talk to in remote mode.
package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"ticket-auction-manager/tam-go/internal/guard"
	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/presence"
	"ticket-auction-manager/tam-go/internal/store"
	"ticket-auction-manager/tam-go/internal/version"
)

// Password is the server password that protects key management. The admin
// package's Password implements it; FixedPassword is enough for tests.
type Password interface {
	// IsSet reports whether a password exists at all. While it is false the
	// key routes answer 503 so nobody can pair with an unconfigured server.
	IsSet() bool
	// Check reports whether plain is the password. An empty plain never
	// matches.
	Check(plain string) bool
}

type fixedPassword string

func (p fixedPassword) IsSet() bool { return p != "" }

func (p fixedPassword) Check(plain string) bool {
	return plain != "" && subtle.ConstantTimeCompare([]byte(plain), []byte(p)) == 1
}

// FixedPassword returns a Password that is the given string; "" is an unset
// password.
func FixedPassword(s string) Password { return fixedPassword(s) }

// Info describes the server to its clients in the GET /api answer.
type Info struct {
	Name    string // shown to clients when pairing; defaults to the host name
	Version string // defaults to version.Version
}

// Option configures NewHandler.
type Option func(*handler)

// WithInfo sets the name and version GET /api reports. Empty fields keep
// their defaults.
func WithInfo(info Info) Option {
	return func(h *handler) {
		if info.Name != "" {
			h.info.Name = info.Name
		}
		if info.Version != "" {
			h.info.Version = info.Version
		}
	}
}

// WithPresence sets the registry that records what each key's client last
// did: every keyed request is a sighting, an accepted POST or DELETE an
// update, and the heartbeat's X-TAM-Pending its queued saves. The admin
// page reads it. Without the option the handler fills a registry nobody
// reads.
func WithPresence(reg *presence.Registry) Option {
	return func(h *handler) {
		if reg != nil {
			h.presence = reg
		}
	}
}

// WithGuesses sets the limit on wrong passwords, per address, that TAM-PW
// counts against. Give the admin pages the same one (admin.WithGuesses), so
// guesses cannot be split between the two; without the option the API
// keeps a limit of its own.
func WithGuesses(l *guard.Limiter) Option {
	return func(h *handler) {
		if l != nil {
			h.guesses = l
		}
	}
}

// touchEvery is how often at most a key's last_seen and last_update are
// written. It is a variable so tests can lower it.
var touchEvery = time.Minute

// maxClientLen caps the program name taken from a request header, which
// the admin page shows.
const maxClientLen = 80

type handler struct {
	st       *store.Store
	pw       Password
	info     Info
	presence *presence.Registry
	guesses  *guard.Limiter

	mu      sync.Mutex
	touched map[string]time.Time // key -> last time last_seen was written
	updated map[string]time.Time // key -> last time last_update was written
}

// NewHandler returns the server API. Data routes require a TAM-KEY header
// that matches a stored access key; key management requires a TAM-PW header
// that pw accepts, and answers 503 while no password is set. Wrong passwords
// count against the address they come from: after guard.MaxFailures of them
// it has to wait (429). Unknown paths and wrong methods under /api answer
// {"detail": ...}. Every request with a valid key is recorded for the admin
// page; see WithPresence.
func NewHandler(st *store.Store, pw Password, opts ...Option) http.Handler {
	hostname, _ := os.Hostname()
	h := &handler{
		st: st, pw: pw, info: Info{Name: hostname, Version: version.Version},
		presence: presence.New(nil),
		guesses:  guard.New(),
		touched:  map[string]time.Time{},
		updated:  map[string]time.Time{},
	}
	for _, opt := range opts {
		opt(h)
	}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api", h.root)
	mux.HandleFunc("GET /api/{$}", h.root)

	mux.Handle("GET /api/auth", h.requirePassword(h.listKeys))
	mux.Handle("POST /api/auth", h.requirePassword(h.createKey))
	mux.Handle("DELETE /api/auth", h.requirePassword(h.deleteKey))

	key := h.requireKey
	mux.Handle("GET /api/prefixes", key(h.listPrefixes))
	mux.Handle("POST /api/prefixes", key(h.postPrefixes))
	mux.Handle("DELETE /api/prefixes", key(h.deletePrefix))

	mux.Handle("GET /api/tickets", key(h.allTickets))
	mux.Handle("GET /api/tickets/{prefix}", key(h.ticketsByPrefix))
	mux.Handle("GET /api/tickets/{prefix}/{id}", key(h.singleTicket))
	mux.Handle("GET /api/tickets/{prefix}/{from}/{to}", key(h.ticketRange))
	mux.Handle("POST /api/tickets", key(h.postTickets))

	mux.Handle("GET /api/baskets", key(h.allBaskets))
	mux.Handle("GET /api/baskets/{prefix}", key(h.basketsByPrefix))
	mux.Handle("GET /api/baskets/{prefix}/{id}", key(h.singleBasket))
	mux.Handle("GET /api/baskets/{prefix}/{from}/{to}", key(h.basketRange))
	mux.Handle("POST /api/baskets", key(h.postBaskets))

	mux.Handle("GET /api/drawing", key(h.allDrawing))
	mux.Handle("GET /api/drawing/{prefix}", key(h.drawingByPrefix))
	mux.Handle("GET /api/drawing/{prefix}/{id}", key(h.singleDrawing))
	mux.Handle("GET /api/drawing/{prefix}/{from}/{to}", key(h.drawingRange))
	mux.Handle("POST /api/drawing", key(h.postDrawing))

	mux.Handle("GET /api/reports/byname/{prefix}", key(h.reportByName))
	mux.Handle("GET /api/reports/bybasket/{prefix}", key(h.reportByBasket))
	mux.Handle("GET /api/reports/counts", key(h.reportCounts))

	mux.Handle("GET /api/search/tickets", key(h.searchTickets))
	mux.Handle("POST /api/search/tickets", key(h.postTickets))

	mux.Handle("GET /api/backuprestore", key(h.exportBackup))
	mux.Handle("POST /api/backuprestore", key(h.importBackup))

	return httpx.JSONErrors(mux, "/api")
}

func (h *handler) requireKey(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := keyOf(r)
		ok, err := h.st.KeyExists(key)
		if err != nil {
			httpx.WriteInternal(w, err)
			return
		}
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, "Invalid Key")
			return
		}
		h.touch(key)
		h.presence.Seen(key, clientOf(r))
		if r.Method != http.MethodPost && r.Method != http.MethodDelete {
			next(w, r)
			return
		}
		// A write the handler accepted is an update of the shared data by
		// that client.
		sw := &statusWriter{ResponseWriter: w}
		next(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		// A repeat of a save already applied (X-TAM-Stale) changed nothing.
		if sw.status/100 == 2 && sw.Header().Get("X-TAM-Stale") == "" {
			h.presence.Updated(key)
			h.markUpdated(key)
		}
	})
}

// statusWriter passes everything through and remembers the status sent.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// keyOf returns the request's access key, from the TAM-KEY header.
func keyOf(r *http.Request) string {
	return r.Header.Get("TAM-KEY")
}

// clientOf names the program behind a request: the X-TAM-Client header
// tam-client sends, else the first word of the User-Agent, else "unknown".
func clientOf(r *http.Request) string {
	name := strings.TrimSpace(r.Header.Get("X-TAM-Client"))
	if name == "" {
		if words := strings.Fields(r.UserAgent()); len(words) > 0 {
			name = words[0]
		}
	}
	if name == "" {
		return "unknown"
	}
	if runes := []rune(name); len(runes) > maxClientLen {
		name = string(runes[:maxClientLen])
	}
	return name
}

// touch records the key's last_seen time, at most once per touchEvery so
// the heartbeat of every client does not turn into a write every 5 s. A
// failure is logged and never fails the request; last_seen is informational.
func (h *handler) touch(key string) {
	if h.due(h.touched, key) {
		if err := h.st.TouchKey(key); err != nil {
			log.Printf("record last_seen for a key: %v", err)
		}
	}
}

// markUpdated records the key's last_update time, throttled like touch.
func (h *handler) markUpdated(key string) {
	if h.due(h.updated, key) {
		if err := h.st.MarkKeyUpdated(key); err != nil {
			log.Printf("record last_update for a key: %v", err)
		}
	}
}

// due reports whether the key's entry in m is missing or older than
// touchEvery and, when so, sets it to now.
func (h *handler) due(m map[string]time.Time, key string) bool {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if last, ok := m[key]; ok && now.Sub(last) < touchEvery {
		return false
	}
	m[key] = now
	return true
}

// forget drops the throttle entries and the presence record of a deleted
// key.
func (h *handler) forget(key string) {
	h.mu.Lock()
	delete(h.touched, key)
	delete(h.updated, key)
	h.mu.Unlock()
	h.presence.Forget(key)
}

// requirePassword lets a request through when its TAM-PW is the password.
// A wrong one is logged with the address it came from, never the password,
// and counted against that address with the admin login's (see
// WithGuesses); a request without one guesses nothing and is not counted.
func (h *handler) requirePassword(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.pw.IsSet() {
			httpx.WriteError(w, http.StatusServiceUnavailable, "server password not set")
			return
		}
		plain := r.Header.Get("TAM-PW")
		if plain == "" {
			httpx.WriteError(w, http.StatusUnauthorized, "Invalid Password")
			return
		}
		right, wait := h.guesses.Check(httpx.RemoteIP(r), "api", func() bool { return h.pw.Check(plain) })
		switch {
		case wait > 0:
			secs := guard.Seconds(wait)
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			httpx.WriteError(w, http.StatusTooManyRequests, fmt.Sprintf("Too many wrong passwords. Wait %d seconds and try again.", secs))
			return
		case !right:
			httpx.WriteError(w, http.StatusUnauthorized, "Invalid Password")
			return
		}
		next(w, r)
	})
}

func (h *handler) root(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)
	authed, err := h.st.KeyExists(key)
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	if authed {
		// The client's heartbeat is this route; X-TAM-Pending on it says
		// how many saves still wait on the client.
		h.touch(key)
		if pending, err := strconv.Atoi(r.Header.Get("X-TAM-Pending")); err == nil && pending >= 0 {
			h.presence.Heartbeat(key, clientOf(r), pending)
		} else {
			h.presence.Seen(key, clientOf(r))
		}
	}
	ev, err := h.st.Event()
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	doc := map[string]any{
		"whoami": "TAM Server", "authenticated": authed, "healthy": true,
		"name": h.info.Name, "version": h.info.Version,
	}
	// The event the server holds: a client whose copy is of another event
	// sets that copy aside (see sync's event check).
	if ev != nil {
		doc["event"] = ev.Event
	}
	httpx.WriteJSON(w, http.StatusOK, doc)
}

// errOrder is a save without a sensible name and number (X-TAM-Client-Name,
// X-TAM-Save).
var errOrder = errors.New("X-TAM-Client-Name must name the client, in at most 64 characters, and X-TAM-Save number its save, above 0")

// behindError is a numbered save older than the last one applied from its
// client (store.Behind); last is that last number.
type behindError struct{ n, last int64 }

func (e behindError) Error() string {
	return fmt.Sprintf("save %d of this client is older than its save %d, which the server has applied; send it again with a number above %d", e.n, e.last, e.last)
}

// ordered runs a save in the order the client made it: every save names
// its client and number (X-TAM-Client-Name, X-TAM-Save), as tam-client sends
// them (see store.InOrder). A repeat of the last save applied from that
// client is not applied again, and the answer says so with X-TAM-Stale; the
// client takes it as done. An older save is not applied either, and is
// answered 409 with the last number applied (X-TAM-Last-Save, and last_save
// in the body), so a client whose numbers went back numbers it again and
// resends it. A save without a name and number is refused (400). content is
// the save as decoded (nil when the path says it all), for the save's
// digest.
func (h *handler) ordered(w http.ResponseWriter, r *http.Request, content any, save func(*store.Store) error) (stale bool, err error) {
	client, number := r.Header.Get("X-TAM-Client-Name"), r.Header.Get("X-TAM-Save")
	n, perr := strconv.ParseInt(number, 10, 64)
	if client == "" || len(client) > 64 || perr != nil || n <= 0 {
		return false, errOrder
	}
	outcome, last, err := h.st.InOrder(client, n, digest(r, content), save)
	if err != nil {
		return false, err
	}
	switch outcome {
	case store.Repeat:
		w.Header().Set("X-TAM-Stale", "1")
		log.Printf("save %d of client %s arrived again; it was applied the first time", n, client)
		return true, nil
	case store.Behind:
		log.Printf("save %d of client %s is older than its save %d, applied already: not applied (a late copy of a save sent again since, or a client whose numbers went back)", n, client, last)
		return false, behindError{n: n, last: last}
	}
	return false, nil
}

// digest names what a save says, for store.InOrder: its method, path and
// query, and its content as decoded, so it does not depend on how the
// client spaced its JSON.
func digest(r *http.Request, content any) string {
	body, _ := json.Marshal(content)
	sum := sha256.Sum256([]byte(r.Method + " " + r.URL.Path + "?" + r.URL.Query().Encode() + "\n" + string(body)))
	return hex.EncodeToString(sum[:])
}

// write is ordered for the save routes: it answers a bad number (400), a
// save older than the client's last (409) or a failure (500) itself, and
// reports whether the save was a repeat and whether the route goes on to
// answer.
func (h *handler) write(w http.ResponseWriter, r *http.Request, content any, save func(*store.Store) error) (stale, ok bool) {
	stale, err := h.ordered(w, r, content, save)
	var behind behindError
	switch {
	case errors.Is(err, errOrder):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return false, false
	case errors.As(err, &behind):
		w.Header().Set("X-TAM-Last-Save", strconv.FormatInt(behind.last, 10))
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"detail": behind.Error(), "last_save": behind.last})
		return false, false
	case err != nil:
		httpx.WriteInternal(w, err)
		return false, false
	}
	return stale, true
}

// respond writes a value (or a generic error) produced by a store call.
func respond[T any](w http.ResponseWriter, v T, err error) {
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, v)
}

// decodeList reads a JSON list body, validates it and answers the error
// itself when something is wrong. A JSON null becomes an empty list.
func decodeList[T any](w http.ResponseWriter, r *http.Request, validate func([]T) error) ([]T, bool) {
	var items []T
	if err := httpx.DecodeJSON(w, r, &items); err != nil {
		httpx.WriteDecodeError(w, err)
		return nil, false
	}
	if items == nil {
		items = []T{}
	}
	if err := validate(items); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return items, true
}

// asList mirrors the original single-item endpoints, which answer with a
// list holding zero or one row.
func asList[T any](item *T, err error) ([]T, error) {
	if err != nil {
		return nil, err
	}
	if item == nil {
		return []T{}, nil
	}
	return []T{*item}, nil
}

// rangeParams reads from and to, swapping them when reversed.
func rangeParams(r *http.Request) (int, int, error) {
	from, err := httpx.IntParam(r, "from")
	if err != nil {
		return 0, 0, err
	}
	to, err := httpx.IntParam(r, "to")
	if err != nil {
		return 0, 0, err
	}
	if from > to {
		from, to = to, from
	}
	return from, to, nil
}

// --- auth keys ---

func (h *handler) listKeys(w http.ResponseWriter, r *http.Request) {
	ks, err := h.st.ListKeys()
	respond(w, ks, err)
}

func (h *handler) createKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Description string `json:"description"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	k, err := h.st.CreateKey(req.Description)
	respond(w, k, err)
}

func (h *handler) deleteKey(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key_to_del")
	if key == "" {
		httpx.WriteError(w, http.StatusBadRequest, "key_to_del is required")
		return
	}
	gone, err := h.st.DeleteKey(key)
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	if gone == nil {
		httpx.WriteError(w, http.StatusNotFound, "Key not found")
		return
	}
	h.forget(key)
	httpx.WriteJSON(w, http.StatusOK, gone)
}

// --- prefixes ---

func (h *handler) listPrefixes(w http.ResponseWriter, r *http.Request) {
	ps, err := h.st.ListPrefixes()
	respond(w, ps, err)
}

// The save routes write each form's saves field by field (see store's
// saves) and answer with the rows as stored afterwards, in the order of the
// saves: the client sees from them which changes were not made, because
// another computer changed the field first, and writes them into its copy.

func (h *handler) postPrefixes(w http.ResponseWriter, r *http.Request) {
	saves, ok := decodeList(w, r, store.ValidatePrefixSaves)
	if !ok {
		return
	}
	var stored []store.Prefix
	saveRoute(h, w, r, saves, func(st *store.Store) (err error) { stored, err = st.SavePrefixes(saves); return err },
		&stored, func(sv store.PrefixSave) (*store.Prefix, error) { return h.st.PrefixByName(sv.Prefix.Prefix) })
}

func (h *handler) deletePrefix(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("p")
	if name == "" {
		httpx.WriteError(w, http.StatusBadRequest, "p is required")
		return
	}
	var gone *store.Prefix
	stale, ok := h.write(w, r, nil, func(st *store.Store) error {
		var err error
		gone, err = st.DeletePrefix(name)
		return err
	})
	switch {
	case !ok:
	case stale:
		// Done before: answered as done, so a client replaying it does
		// not file it as refused.
		httpx.WriteJSON(w, http.StatusOK, store.Prefix{Prefix: name})
	case gone == nil:
		httpx.WriteError(w, http.StatusNotFound, "Prefix not found")
	default:
		httpx.WriteJSON(w, http.StatusOK, gone)
	}
}

// --- tickets ---

func (h *handler) allTickets(w http.ResponseWriter, r *http.Request) {
	ts, err := h.st.AllTickets()
	respond(w, ts, err)
}

func (h *handler) ticketsByPrefix(w http.ResponseWriter, r *http.Request) {
	ts, err := h.st.TicketsByPrefix(r.PathValue("prefix"))
	respond(w, ts, err)
}

func (h *handler) singleTicket(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.IntParam(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	ts, err := asList(h.st.Ticket(r.PathValue("prefix"), id))
	respond(w, ts, err)
}

func (h *handler) ticketRange(w http.ResponseWriter, r *http.Request) {
	from, to, err := rangeParams(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	ts, err := h.st.TicketRange(r.PathValue("prefix"), from, to)
	respond(w, ts, err)
}

func (h *handler) postTickets(w http.ResponseWriter, r *http.Request) {
	saves, ok := decodeList(w, r, store.ValidateTicketSaves)
	if !ok {
		return
	}
	var stored []store.Ticket
	saveRoute(h, w, r, saves, func(st *store.Store) (err error) { stored, err = st.SaveTickets(saves); return err },
		&stored, func(sv store.TicketSave) (*store.Ticket, error) { return h.st.Ticket(sv.Prefix, sv.TID) })
}

func (h *handler) searchTickets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ts, err := h.st.SearchTickets(q.Get("first_name"), q.Get("last_name"), q.Get("phone_number"))
	respond(w, ts, err)
}

// --- baskets ---

func (h *handler) allBaskets(w http.ResponseWriter, r *http.Request) {
	bs, err := h.st.AllBaskets()
	respond(w, bs, err)
}

func (h *handler) basketsByPrefix(w http.ResponseWriter, r *http.Request) {
	bs, err := h.st.BasketsByPrefix(r.PathValue("prefix"))
	respond(w, bs, err)
}

func (h *handler) singleBasket(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.IntParam(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	bs, err := asList(h.st.Basket(r.PathValue("prefix"), id))
	respond(w, bs, err)
}

func (h *handler) basketRange(w http.ResponseWriter, r *http.Request) {
	from, to, err := rangeParams(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	bs, err := h.st.BasketRange(r.PathValue("prefix"), from, to)
	respond(w, bs, err)
}

func (h *handler) postBaskets(w http.ResponseWriter, r *http.Request) {
	saves, ok := decodeList(w, r, store.ValidateBasketSaves)
	if !ok {
		return
	}
	var stored []store.Basket
	saveRoute(h, w, r, saves, func(st *store.Store) (err error) { stored, err = st.SaveBaskets(saves); return err },
		&stored, func(sv store.BasketSave) (*store.Basket, error) { return h.st.Basket(sv.Prefix, sv.BID) })
}

// --- drawing ---

func (h *handler) allDrawing(w http.ResponseWriter, r *http.Request) {
	ds, err := h.st.AllDrawing()
	respond(w, ds, err)
}

func (h *handler) drawingByPrefix(w http.ResponseWriter, r *http.Request) {
	ds, err := h.st.DrawingByPrefix(r.PathValue("prefix"))
	respond(w, ds, err)
}

func (h *handler) singleDrawing(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.IntParam(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	ds, err := asList(h.st.DrawingLine(r.PathValue("prefix"), id))
	respond(w, ds, err)
}

func (h *handler) drawingRange(w http.ResponseWriter, r *http.Request) {
	from, to, err := rangeParams(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	ds, err := h.st.DrawingRange(r.PathValue("prefix"), from, to)
	respond(w, ds, err)
}

func (h *handler) postDrawing(w http.ResponseWriter, r *http.Request) {
	saves, ok := decodeList(w, r, store.ValidateDrawingSaves)
	if !ok {
		return
	}
	var stored []store.Basket
	saveRoute(h, w, r, saves, func(st *store.Store) (err error) { stored, err = st.SaveWinning(saves); return err },
		&stored, func(sv store.DrawingSave) (*store.Basket, error) { return h.st.Basket(sv.Prefix, sv.BID) })
}

// saveRoute runs a save in its client's order (see write) and answers with
// the rows as stored. A repeat of a save already applied answers with the
// rows as they are now, read with current.
func saveRoute[S any, R any](h *handler, w http.ResponseWriter, r *http.Request, saves []S, save func(*store.Store) error,
	stored *[]R, current func(S) (*R, error)) {
	stale, ok := h.write(w, r, saves, save)
	if !ok {
		return
	}
	if stale {
		rows := make([]R, 0, len(saves))
		for _, sv := range saves {
			row, err := current(sv)
			if err != nil {
				httpx.WriteInternal(w, err)
				return
			}
			if row == nil {
				httpx.WriteError(w, http.StatusConflict, "a row of this save is gone from the server")
				return
			}
			rows = append(rows, *row)
		}
		*stored = rows
	}
	httpx.WriteJSON(w, http.StatusOK, *stored)
}

// --- reports ---

func (h *handler) reportByName(w http.ResponseWriter, r *http.Request) {
	ls, err := h.st.ReportByName(r.PathValue("prefix"))
	respond(w, ls, err)
}

func (h *handler) reportByBasket(w http.ResponseWriter, r *http.Request) {
	ls, err := h.st.ReportByBasket(r.PathValue("prefix"))
	respond(w, ls, err)
}

func (h *handler) reportCounts(w http.ResponseWriter, r *http.Request) {
	ls, err := h.st.ReportCounts()
	respond(w, ls, err)
}

// --- backup and restore ---

func (h *handler) exportBackup(w http.ResponseWriter, r *http.Request) {
	bf, err := h.st.Export()
	respond(w, bf, err)
}

// importBackup restores a backup file: its rows replace the server's. With
// X-TAM-Merge: newer it merges a client's copy instead (Push), writing only
// the rows newer than the server's (see store.MergeNewer).
func (h *handler) importBackup(w http.ResponseWriter, r *http.Request) {
	var bf store.BackupFile
	if err := httpx.DecodeJSON(w, r, &bf); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	if err := store.ValidateBackup(&bf); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.Header.Get("X-TAM-Merge") == "newer" {
		res, err := h.st.MergeNewer(bf)
		switch {
		case errors.Is(err, store.ErrOtherEvent):
			httpx.WriteError(w, http.StatusConflict, "This client's copy belongs to another event than the one this server holds; nothing was written.")
		case err != nil:
			httpx.WriteInternal(w, err)
		default:
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"message": fmt.Sprintf("%d rows added and %d updated; %d were as new or newer on the server and stayed.", res.Added, res.Updated, res.Kept),
				"added":   res.Added, "updated": res.Updated, "kept": res.Kept,
			})
		}
		return
	}
	if err := h.st.Import(bf); err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "Backup file imported successfully."})
}
