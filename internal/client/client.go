// Package client is the HTTP surface of tam-client: it serves the embedded
// web app and an /api that works against the local database or, in remote
// mode, against a tam-server, with the local database as the mirror that
// keeps the pages working while the server is away.
package client

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/discovery"
	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/remote"
	"ticket-auction-manager/tam-go/internal/store"
	tamsync "ticket-auction-manager/tam-go/internal/sync"
)

type handler struct {
	st      *store.Store
	cfg     *config.File
	sync    *tamsync.Syncer
	host    string // this machine's name, under which the client names itself
	dataDir string // the folder of settings.json and the database

	// shutdown, when set, is called after POST /api/shutdown has answered.
	shutdown func()

	// One remote client (one connection pool) per server address, TLS
	// setting and pinned certificate; the access key is applied per request.
	rcMu   sync.Mutex
	rc     *remote.Client
	rcBase string
	rcTLS  bool
	rcPin  string

	timings     tamsync.Timings
	runCtx      context.Context
	syncStopped chan<- struct{}

	// The last subnet sweep for servers, refreshed in the background.
	sweepMu  sync.Mutex
	swept    []discovery.Server
	sweptAt  time.Time
	sweeping bool
}

// Option configures NewHandler.
type Option func(*handler)

// WithShutdown enables POST /api/shutdown, the "Shut Down TAM" button on the
// main menu: fn runs after the request has been answered and should stop
// the program.
func WithShutdown(fn func()) Option {
	return func(h *handler) { h.shutdown = fn }
}

// WithSyncLoop starts the background heartbeat and outbox replay, which
// run until ctx ends. Without it the server is only contacted by requests.
func WithSyncLoop(ctx context.Context) Option {
	return func(h *handler) { h.runCtx = ctx }
}

// WithSyncStopped has the handler close ch once the loop WithSyncLoop
// started has stopped, so the program can close the database after it.
func WithSyncStopped(ch chan<- struct{}) Option {
	return func(h *handler) { h.syncStopped = ch }
}

// WithTimings shortens the syncer's delays (for tests).
func WithTimings(t tamsync.Timings) Option {
	return func(h *handler) { h.timings = t }
}

// NewHandler returns the client handler. dist is the built web app, served
// under /web with index.html as the fallback for client-side routes.
func NewHandler(st *store.Store, settingsPath string, dist fs.FS, opts ...Option) http.Handler {
	return newHandler(st, settingsPath, dist, opts...).routes(dist)
}

func newHandler(st *store.Store, settingsPath string, dist fs.FS, opts ...Option) *handler {
	h := &handler{st: st, cfg: config.Open(settingsPath), timings: tamsync.DefaultTimings(), host: hostname(), dataDir: filepath.Dir(settingsPath)}
	for _, opt := range opts {
		opt(h)
	}
	h.sync = tamsync.New(st, h.cfg, h.remote, h.timings)
	h.sync.OnEventChange(h.changeEvent)
	if h.runCtx != nil {
		go func() {
			h.sync.Run(h.runCtx)
			if h.syncStopped != nil {
				close(h.syncStopped)
			}
		}()
	}
	return h
}

func (h *handler) routes(dist fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/shutdown", guard(h.shutdownHandler))

	mux.Handle("GET /{$}", http.RedirectHandler("/web/", http.StatusFound))
	mux.Handle("GET /web/", newSPA(dist))
	mux.HandleFunc("GET /favicon.ico", icon(dist))

	mux.HandleFunc("GET /api", h.root)
	mux.HandleFunc("GET /api/{$}", h.root)
	mux.HandleFunc("GET /api/settings", h.getSettings)
	mux.HandleFunc("POST /api/settings", guard(h.postSettings))

	mux.HandleFunc("GET /api/status", h.status)
	mux.HandleFunc("GET /api/servers", h.servers)
	mux.HandleFunc("POST /api/pair", guard(h.pair))
	mux.HandleFunc("POST /api/unpair", guard(h.unpair))
	mux.HandleFunc("GET /api/outbox/failed", h.failedOutbox)
	mux.HandleFunc("POST /api/outbox/retry", guard(h.retryOutbox))
	mux.HandleFunc("POST /api/outbox/discard", guard(h.discardOutbox))

	mux.HandleFunc("GET /api/auth", h.proxyAuth)
	mux.HandleFunc("POST /api/auth", guard(h.proxyAuth))
	mux.HandleFunc("DELETE /api/auth", guard(h.proxyAuth))

	mux.HandleFunc("GET /api/prefixes", h.listPrefixes)
	mux.HandleFunc("POST /api/prefixes", guard(h.postPrefixes))
	mux.HandleFunc("DELETE /api/prefixes", guard(h.deletePrefix))

	mux.HandleFunc("GET /api/tickets", h.allTickets)
	mux.HandleFunc("GET /api/tickets/{prefix}", h.ticketsByPrefix)
	mux.HandleFunc("GET /api/tickets/{prefix}/{id}", h.singleTicket)
	mux.HandleFunc("GET /api/tickets/{prefix}/{from}/{to}", h.ticketRange)
	mux.HandleFunc("POST /api/tickets", guard(h.postTickets))

	mux.HandleFunc("GET /api/baskets", h.allBaskets)
	mux.HandleFunc("GET /api/baskets/{prefix}", h.basketsByPrefix)
	mux.HandleFunc("GET /api/baskets/{prefix}/{id}", h.singleBasket)
	mux.HandleFunc("GET /api/baskets/{prefix}/{from}/{to}", h.basketRange)
	mux.HandleFunc("POST /api/baskets", guard(h.postBaskets))

	mux.HandleFunc("GET /api/drawing", h.allDrawing)
	mux.HandleFunc("GET /api/drawing/{prefix}", h.drawingByPrefix)
	mux.HandleFunc("GET /api/drawing/{prefix}/{id}", h.singleDrawing)
	mux.HandleFunc("GET /api/drawing/{prefix}/{from}/{to}", h.drawingRange)
	mux.HandleFunc("POST /api/drawing", guard(h.postDrawing))

	mux.HandleFunc("GET /api/reports/byname/{prefix}", h.reportByName)
	mux.HandleFunc("GET /api/reports/bybasket/{prefix}", h.reportByBasket)
	mux.HandleFunc("GET /api/reports/counts", h.reportCounts)

	mux.HandleFunc("GET /api/search/tickets", h.searchTickets)
	mux.HandleFunc("POST /api/search/tickets", guard(h.postSearch))

	mux.HandleFunc("GET /api/backuprestore/local", h.exportLocal)
	mux.HandleFunc("POST /api/backuprestore/local", guard(h.importLocal))
	mux.HandleFunc("GET /api/backuprestore/remote", h.exportRemote)
	mux.HandleFunc("POST /api/backuprestore/remote", guard(h.importRemote))
	mux.HandleFunc("POST /api/backuprestore/push/{target}", guard(h.push))

	return httpx.JSONErrors(mux, "/api")
}

// shutdownHandler stops the program on request from the main menu. The
// empty JSON body the page sends keeps the Content-Type barrier that stops
// cross-site form posts.
func (h *handler) shutdownHandler(w http.ResponseWriter, r *http.Request) {
	var ignored json.RawMessage
	if err := httpx.DecodeJSON(w, r, &ignored); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	if h.shutdown == nil {
		httpx.WriteError(w, http.StatusNotImplemented, "Shutdown is not available in this setup")
		return
	}
	log.Print("shutdown requested from the web page: stopping")
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "TAM client is shutting down."})
	go h.shutdown()
}

// hostname is this machine's name, "client" when it has none.
func hostname() string {
	if name, _ := os.Hostname(); name != "" {
		return name
	}
	return "client"
}

// guard refuses writes that a browser reports as coming from another site.
func guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !httpx.SameSite(r) {
			httpx.WriteError(w, http.StatusForbidden, "Cross-site request refused")
			return
		}
		next(w, r)
	}
}

// settings returns the current settings; a hand edit of the file is picked
// up without a restart and a broken file keeps the last good values.
func (h *handler) settings() config.Settings {
	return h.cfg.Get()
}

// remote returns a client for the configured server, or nil in standalone
// mode. The connection pool is kept across requests and rebuilt only when
// the server address, TLS setting or pinned certificate changes.
func (h *handler) remote(s config.Settings) *remote.Client {
	base := s.RemoteURL()
	if base == "" {
		return nil
	}
	h.rcMu.Lock()
	defer h.rcMu.Unlock()
	if h.rc == nil || h.rcBase != base || h.rcTLS != s.RemoteTLS || h.rcPin != s.RemoteFingerprint {
		if s.RemoteTLS && s.RemoteFingerprint != "" {
			h.rc = remote.NewPinned(base, "", s.RemoteFingerprint)
		} else {
			h.rc = remote.New(base, "", s.RemoteTLS)
		}
		h.rcBase, h.rcTLS, h.rcPin = base, s.RemoteTLS, s.RemoteFingerprint
	}
	return h.rc.WithKey(s.RemoteKey)
}

// --- single page app ---

// icon serves the web app's icon at /favicon.ico, where a browser asks for
// it on pages that name no icon, such as the API's own answers.
func icon(dist fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(dist, "favicon.ico")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/x-icon")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(data)
	}
}

type spa struct {
	dist  fs.FS
	files http.Handler
	index []byte
}

func newSPA(dist fs.FS) http.Handler {
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		log.Printf("web app: index.html is missing from the embedded build: %v", err)
		index = []byte("<!doctype html><title>TAM</title><p>The web app was not built. Run <code>pnpm build</code> in frontend/ before building tam-client.")
	}
	return &spa{dist: dist, files: http.FileServer(http.FS(dist)), index: index}
}

func (s *spa) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/web/")
	if rel != "" {
		if f, err := s.dist.Open(rel); err == nil {
			info, statErr := f.Stat()
			f.Close()
			if statErr == nil && !info.IsDir() {
				// Hashed bundle files never change; everything else may.
				if strings.HasPrefix(rel, "_app/immutable/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				http.StripPrefix("/web/", s.files).ServeHTTP(w, r)
				return
			}
		}
		// A missing bundle file is a 404; any other address is a page of the
		// app, whatever it looks like: a prefix may be named 5.00.
		if strings.HasPrefix(rel, "_app/") && !strings.HasSuffix(rel, "/") {
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	w.Write(s.index)
}
