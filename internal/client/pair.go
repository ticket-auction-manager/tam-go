package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/discovery"
	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/remote"
)

// browseWait is how long GET /api/servers listens for announcements.
const browseWait = 1500 * time.Millisecond

// status answers where the client stands with its server.
func (h *handler) status(w http.ResponseWriter, r *http.Request) {
	st := h.sync.Status()
	if st.Mode == "standalone" {
		out := map[string]string{"mode": "standalone"}
		if st.SettingsError != "" {
			out["settings_error"] = st.SettingsError
		}
		httpx.WriteJSON(w, http.StatusOK, out)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

// sweepEvery is how long a subnet sweep's result is reused before the
// network is asked again; the Settings page polls more often than that.
const sweepEvery = 10 * time.Second

// servers lists the tam-servers on this network: the ones announcing
// themselves (mDNS) and, for networks that drop multicast, the ones found by
// asking the standard ports on every address of the local /24 networks.
// The sweep runs in the background and takes a few seconds; its result
// shows up on the Settings page's next poll. A server seen at several
// addresses is listed once, at the address this client shares a network
// with.
func (h *handler) servers(w http.ResponseWriter, r *http.Request) {
	announced, err := discovery.Browse(r.Context(), browseWait)
	if err != nil {
		log.Printf("discovery: %v", err)
	}
	out := discovery.Collapse(append(announced, h.sweep()...), discovery.LocalNetworks())
	if out == nil {
		out = []discovery.Server{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// sweep returns the last subnet sweep and starts a fresh one in the
// background when the last is older than sweepEvery.
func (h *handler) sweep() []discovery.Server {
	h.sweepMu.Lock()
	defer h.sweepMu.Unlock()
	if !h.sweeping && time.Since(h.sweptAt) >= sweepEvery {
		h.sweeping = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			found := discovery.Sweep(ctx)
			h.sweepMu.Lock()
			h.swept, h.sweptAt, h.sweeping = found, time.Now(), false
			h.sweepMu.Unlock()
		}()
	}
	return h.swept
}

type pairRequest struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	TLS      bool   `json:"tls"`
	Password string `json:"password"`
}

// pair connects this client to a server: it checks the address answers as
// a TAM server, creates an access key with the server password, and saves
// the connection. Over TLS the server certificate is pinned from now on.
//
// Saves still queued stay queued when the client pairs with the server it
// was paired with (at the same address, or by the same name at a new one),
// which is how a client whose key was refused, or whose server has a new
// certificate, gets going again. Pairing with another server moves them to
// the failed list instead: they are not sent anywhere by themselves, and
// Settings offers Retry and Discard.
//
// Pairing with another server while this client holds data of its own (from
// working standalone, or a copy of another server's data) first saves that
// data to a file in the data folder (see keepLocalData): the first copy of
// the server's data replaces the rows with the same numbers here.
func (h *handler) pair(w http.ResponseWriter, r *http.Request) {
	var req pairRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	req.Host = strings.TrimSpace(req.Host)
	req.Port = strings.TrimSpace(req.Port)
	if req.Port == "" {
		req.Port = "8000"
	}
	probe := config.Defaults()
	probe.RemoteServer, probe.RemotePort, probe.RemoteTLS = req.Host, req.Port, req.TLS
	if req.Host == "" {
		httpx.WriteError(w, http.StatusBadRequest, "host is required")
		return
	}
	if err := config.Validate(probe); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Password == "" {
		httpx.WriteError(w, http.StatusBadRequest, "password is required")
		return
	}

	hostPort := net.JoinHostPort(req.Host, req.Port)
	fingerprint := ""
	var rc *remote.Client
	if req.TLS {
		fp, err := remote.Fingerprint(hostPort)
		if err != nil {
			h.unreachableAt(w, hostPort, err)
			return
		}
		fingerprint = fp
		rc = remote.NewPinned(probe.RemoteURL(), "", fp)
	} else {
		rc = remote.New(probe.RemoteURL(), "", false)
	}
	rc = rc.WithTimeout(10 * time.Second)

	name := req.Host
	res, err := rc.Get("/api")
	if err != nil {
		h.unreachableAt(w, hostPort, err)
		return
	}
	var root struct {
		Whoami string `json:"whoami"`
		Name   string `json:"name"`
		Event  string `json:"event"`
	}
	if !res.OK() || res.JSON(&root) != nil || root.Whoami != "TAM Server" {
		httpx.WriteError(w, http.StatusBadGateway, fmt.Sprintf("%s did not answer as a TAM server", hostPort))
		return
	}
	if root.Name != "" {
		name = root.Name
	}

	prev := h.settings()
	mine, err := h.st.MirrorEvent()
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	// The same server, holding the same event: a server that names its event
	// is the same only while it holds the event of this client's copy.
	same := prev.RemoteURL() != "" &&
		(prev.RemoteServer == req.Host && prev.RemotePort == req.Port || prev.RemoteName != "" && prev.RemoteName == name) &&
		(root.Event == "" || mine == "" || root.Event == mine)
	res, err = rc.Do(http.MethodPost, "/api/auth", map[string]string{"TAM-PW": req.Password}, map[string]string{"description": h.host})
	if err != nil {
		h.unreachableAt(w, hostPort, err)
		return
	}
	switch {
	case res.Status == http.StatusUnauthorized || res.Status == http.StatusForbidden:
		httpx.WriteError(w, http.StatusUnauthorized, "The server rejected the password")
		return
	case res.Status == http.StatusServiceUnavailable:
		httpx.WriteError(w, http.StatusServiceUnavailable, "The server has no password yet; open its admin page first")
		return
	case !res.OK():
		forward(w, res)
		return
	}
	var key struct {
		AuthKey string `json:"auth_key"`
	}
	if res.JSON(&key) != nil || key.AuthKey == "" {
		httpx.WriteError(w, http.StatusBadGateway, "The server did not return an access key")
		return
	}

	// Before the settings change: from then on the syncer may copy the
	// server's data in at any moment. A copy of another event is emptied
	// below, so without its file this client does not pair.
	kept := ""
	if !same {
		var err error
		kept, err = h.keepLocalData(name, "before-pairing-")
		if err != nil && mine != "" {
			dropKey(rc, req.Password, key.AuthKey)
			httpx.WriteError(w, http.StatusInternalServerError, fmt.Sprintf(
				"This client's data could not be saved to a file in its data folder (%v), so it did not pair: pairing with a server of another event empties this client's copy. Make room or fix the folder, then pair again.", err))
			return
		}
	}

	// The settings and the queue change together, while no save is numbered
	// or on its way to the old server.
	defer h.sync.Numbering()()
	defer h.sync.Sending()()
	if _, err := h.cfg.Update(func(cur config.Settings) (config.Settings, error) {
		cur.RemoteServer, cur.RemotePort, cur.RemoteTLS = req.Host, req.Port, req.TLS
		cur.RemoteKey, cur.RemoteName, cur.RemoteFingerprint = key.AuthKey, name, fingerprint
		return config.Normalize(cur), nil
	}); err != nil {
		httpx.WriteInternal(w, fmt.Errorf("save settings: %w", err))
		return
	}
	msg := "Paired with " + name + "." + kept
	if !same {
		from := "before this pairing"
		if prev.RemoteURL() != "" {
			from = "for " + serverLabel(prev)
		}
		moved, err := h.st.FailAllOutbox("queued " + from + "; retry to send it to " + name)
		switch {
		case err != nil:
			log.Printf("outbox: %v", err)
		case moved > 0:
			log.Printf("paired with %s: %s queued %s set aside in the failed list", name, plural(moved, "save"), from)
			msg += fmt.Sprintf(" %s queued %s %s set aside: Settings lists them under could not be sent, to retry here or discard.",
				plural(moved, "save"), from, wasWere(moved))
		}
		// A copy of another event goes (it is in the file kept above): the
		// new server's event replaces it whole. A copy that names no event
		// stays, to be pushed or replaced row by row.
		if mine != "" {
			if err := h.st.ClearCopy(); err != nil {
				log.Printf("pair: setting the copy of the earlier event aside: %v", err)
			}
		}
	}
	h.sync.Reset()
	h.sync.Kick()
	log.Printf("paired with %s (%s)", name, hostPort)
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": msg, "server": hostPort})
}

// keepLocalData saves the data this client holds of its own to a file in
// its data folder (named prefix and the time) before it pairs with another
// server, or sets aside a copy of another event, after which the server's
// data replaces it: nothing entered on this client is lost, and Backup and
// Restore can load the file again or send it to the server. It returns the
// sentence the pairing's message adds ("" when the client holds no data),
// and an error when the data could not be read or written to the file, so
// the caller does not empty a copy that has no file.
func (h *handler) keepLocalData(server, prefix string) (string, error) {
	bf, err := h.st.Export()
	if err != nil {
		log.Printf("pair: keeping this client's data: %v", err)
		return fmt.Sprintf(" This client's own data could not be read to save it to a file first (%v); %s's data replaces rows with the same numbers.", err, server), err
	}
	if len(bf.Prefixes)+len(bf.Tickets)+len(bf.Baskets) == 0 {
		return "", nil
	}
	name := prefix + time.Now().Format("20060102-150405") + ".json"
	data, err := json.MarshalIndent(bf, "", "  ")
	if err == nil {
		err = writeFileSynced(filepath.Join(h.dataDir, name), data)
	}
	what := fmt.Sprintf("%s, %s and %s", count(len(bf.Prefixes), "prefix", "prefixes"), plural(len(bf.Tickets), "ticket"), plural(len(bf.Baskets), "basket"))
	if err != nil {
		log.Printf("pair: keeping this client's data (%s): %v", what, err)
		return fmt.Sprintf(" This client's own data (%s) could not be saved to a file first (%v); %s's data replaces rows with the same numbers.", what, err, server), err
	}
	log.Printf("pair: this client's own data (%s) saved to %s before pairing with %s", what, name, server)
	return fmt.Sprintf(" This client's own data (%s) was saved to %s in its data folder first; Backup and Restore can load it again or send it to the server.", what, name), nil
}

// writeFileSynced writes a file and flushes it to the disk before it
// returns, so a copy the caller empties next is on the disk first.
func writeFileSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}

// dropKey deletes, as well as it can, the key a pairing made on a server
// before the pairing was given up.
func dropKey(rc *remote.Client, password, key string) {
	res, err := rc.Do(http.MethodDelete, "/api/auth?key_to_del="+url.QueryEscape(key), map[string]string{"TAM-PW": password}, nil)
	if err != nil || !res.OK() {
		log.Printf("pair: the key made on the server could not be deleted again; delete it on the server's admin page")
	}
}

// changeEvent sets this client's copy aside when its server holds another
// event than the copy's (see sync's OnEventChange): the copy is kept in a
// file in the data folder, the saves still waiting for the earlier event
// go to the failed list (Settings can retry them into this event or discard
// them), and the copy is emptied for the server's event, whose data the
// syncer pulls next. Nothing of the earlier event reaches the server
// unasked.
func (h *handler) changeEvent(old, new string) error {
	defer h.sync.Numbering()()
	defer h.sync.Sending()()
	s := h.settings()
	kept, err := h.keepLocalData(serverLabel(s), "before-event-")
	if err != nil {
		return fmt.Errorf("this client's copy of the earlier event must be in a file before it is emptied, and it could not be saved (%w); nothing was changed, and the client tries again", err)
	}
	if kept != "" {
		log.Printf("event:%s", kept)
	}
	moved, err := h.st.FailAllOutbox("made for an earlier event than the one " + serverLabel(s) + " holds now; retry to send it to this event")
	if err != nil {
		return err
	}
	if moved > 0 {
		log.Printf("event: %s waiting for the earlier event set aside in the failed list", plural(moved, "save"))
	}
	if err := h.st.ClearCopy(); err != nil {
		return err
	}
	return h.st.SetMirrorEvent(new)
}

// serverLabel names the server of the settings as the pages do.
func serverLabel(s config.Settings) string {
	if s.RemoteName != "" {
		return s.RemoteName
	}
	return net.JoinHostPort(s.RemoteServer, s.RemotePort)
}

func wasWere(n int) string {
	if n == 1 {
		return "was"
	}
	return "were"
}

// unpair returns the client to standalone mode. With the server password
// the client's key is also deleted on the server, when it can be reached.
func (h *handler) unpair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	s := h.settings()
	if s.RemoteURL() == "" {
		httpx.WriteError(w, http.StatusBadRequest, "This client is not paired with a server")
		return
	}
	defer h.sync.Numbering()() // no save is numbered or on its way while the queue is set aside
	defer h.sync.Sending()()
	if req.Password != "" && s.RemoteKey != "" {
		if rc := h.remote(s); rc != nil {
			res, err := rc.WithTimeout(5*time.Second).Do(http.MethodDelete, "/api/auth?key_to_del="+url.QueryEscape(s.RemoteKey),
				map[string]string{"TAM-PW": req.Password}, nil)
			switch {
			case err != nil:
				log.Printf("unpair: the key stays on the server: %v", err)
			case !res.OK():
				log.Printf("unpair: the server kept the key (%d)", res.Status)
			}
		}
	}
	if _, err := h.cfg.Update(func(cur config.Settings) (config.Settings, error) {
		cur.RemoteServer, cur.RemoteKey, cur.RemoteName, cur.RemoteFingerprint = "", "", "", ""
		return cur, nil
	}); err != nil {
		httpx.WriteInternal(w, fmt.Errorf("save settings: %w", err))
		return
	}
	// Saves that had not reached the server are kept in the failed list,
	// where Settings offers Retry and Discard once the client is paired again.
	kept, err := h.st.FailAllOutbox("not sent before unpairing from " + serverLabel(s))
	if err != nil {
		log.Printf("outbox: %v", err)
	}
	h.sync.Reset()
	msg := "Standalone again."
	if kept > 0 {
		msg += fmt.Sprintf(" %s that had not reached the server %s kept: after pairing again, Settings lists them under could not be sent, to retry or discard.",
			plural(kept, "save"), wasWere(kept))
	}
	log.Print("unpaired: standalone again")
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": msg})
}

// retryOutbox puts the saves the server refused back in line.
func (h *handler) retryOutbox(w http.ResponseWriter, r *http.Request) {
	var ignored json.RawMessage
	if err := httpx.DecodeJSON(w, r, &ignored); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	done := h.sync.Numbering() // renumbered in line with the saves being made
	n, err := h.st.RetryFailed(h.host)
	done()
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	h.sync.Kick()
	pending, _, err := h.st.OutboxCounts()
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"message": "Retrying " + plural(n, "save"), "pending": pending})
}

// discardOutbox forgets the saves the server refused.
// failedOutbox lists the saves in the failed list with why each is there,
// oldest first, so the volunteer can decide between Retry and Discard: a
// change another computer's newer value kept out names the record, the
// field and both values.
func (h *handler) failedOutbox(w http.ResponseWriter, r *http.Request) {
	failed, err := h.st.ListFailed()
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	type entry struct {
		Made   string `json:"made"`
		Save   string `json:"save"`
		Reason string `json:"reason"`
	}
	out := make([]entry, 0, len(failed))
	for _, o := range failed {
		out = append(out, entry{Made: o.CreatedAt.UTC().Format(time.RFC3339), Save: o.Method + " " + o.Path, Reason: o.LastError})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *handler) discardOutbox(w http.ResponseWriter, r *http.Request) {
	var ignored json.RawMessage
	if err := httpx.DecodeJSON(w, r, &ignored); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	n, err := h.st.DiscardFailed()
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "Discarded " + plural(n, "save")})
}

func (h *handler) unreachableAt(w http.ResponseWriter, hostPort string, err error) {
	log.Printf("pair %s: %v", hostPort, err)
	httpx.WriteError(w, http.StatusBadGateway, "Remote server unreachable")
}

// count is plural for a noun whose plural is not noun+"s".
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
