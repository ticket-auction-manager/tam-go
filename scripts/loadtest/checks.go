package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"ticket-auction-manager/tam-go/internal/store"
)

// check is one verdict of the run.
type check struct {
	name    string
	passed  bool
	skipped bool // not checked on this server; neither passes nor fails the run
	detail  string
}

func (t *test) record(name string, passed bool, format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.checks = append(t.checks, check{name: name, passed: passed, detail: fmt.Sprintf(format, args...)})
}

func (t *test) passed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range t.checks {
		if !c.passed {
			return false
		}
	}
	return len(t.checks) > 0
}

// skipped counts the checks this server could not answer.
func (t *test) skipped() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, c := range t.checks {
		if c.skipped {
			n++
		}
	}
	return n
}

// skip records a check this server cannot answer.
func (t *test) skip(name string, format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.checks = append(t.checks, check{name: name, passed: true, skipped: true, detail: fmt.Sprintf(format, args...)})
}

// note keeps the first three descriptions of what differs.
func note(first *[]string, format string, args ...any) {
	if len(*first) < 3 {
		*first = append(*first, fmt.Sprintf(format, args...))
	}
}

func examples(first []string) string {
	if len(first) == 0 {
		return ""
	}
	return "; e.g. " + strings.Join(first, "; ")
}

func decodeJSON(res *http.Response, into any) error {
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("answered %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(res.Body).Decode(into); err != nil {
		return err
	}
	valuesOnly(into)
	return nil
}

// valuesOnly clears the order numbers the server stamps on rows (see
// store.Event) in what a program answered: the checks compare the values
// that were saved.
func valuesOnly(into any) {
	switch v := into.(type) {
	case *store.Ticket:
		v.Rev = 0
	case *store.Basket:
		v.Rev, v.WinRev = 0, 0
	case *store.DrawingLine:
		v.WinRev = 0
	case *[]store.Ticket:
		for i := range *v {
			(*v)[i].Rev = 0
		}
	case *[]store.Basket:
		for i := range *v {
			(*v)[i].Rev, (*v)[i].WinRev = 0, 0
		}
	case *[]store.Prefix:
		for i := range *v {
			(*v)[i].Rev = 0
		}
	case *[]store.DrawingLine:
		for i := range *v {
			(*v)[i].WinRev = 0
		}
	case *store.BackupFile:
		v.Event = ""
		valuesOnly(&v.Prefixes)
		valuesOnly(&v.Tickets)
		valuesOnly(&v.Baskets)
	}
}

func (t *test) checkData(when string) {
	t.checkServer(when)
	t.checkMirrors()
}

// rowsDiff is how the server's rows of one kind differ from what was saved.
type rowsDiff struct {
	saved, missing, differ, extra int
	first                         []string
}

func (d rowsDiff) clean() bool { return d.missing+d.differ+d.extra == 0 }

// serverDiff is how the server's data differs from what was saved.
type serverDiff struct {
	tickets, baskets   rowsDiff
	prefixesSame       bool
	prefixes, onServer int
}

func (d serverDiff) clean() bool { return d.tickets.clean() && d.baskets.clean() && d.prefixesSame }

func (d serverDiff) summary() string {
	return fmt.Sprintf("tickets %d missing, %d different, %d unexpected%s; baskets %d missing, %d different, %d unexpected%s; prefixes the same: %v",
		d.tickets.missing, d.tickets.differ, d.tickets.extra, examples(d.tickets.first),
		d.baskets.missing, d.baskets.differ, d.baskets.extra, examples(d.baskets.first), d.prefixesSame)
}

// diffServer downloads the server's data, as the server backup button of
// the Settings page does, and compares every row with what was last saved.
func (t *test) diffServer() (serverDiff, error) {
	var d serverDiff
	var bf store.BackupFile
	if _, err := t.clients[0].call(nil, "", http.MethodGet, "/api/backuprestore/remote", nil, &bf, 0); err != nil {
		return d, err
	}
	t.ev.mu.Lock()
	defer t.ev.mu.Unlock()

	tickets := map[key]store.Ticket{}
	for _, tk := range bf.Tickets {
		tickets[key{tk.Prefix, tk.TID}] = tk
	}
	d.tickets.saved = len(t.ev.ticket)
	for k, want := range t.ev.ticket {
		got, ok := tickets[k]
		switch {
		case !ok:
			d.tickets.missing++
			note(&d.tickets.first, "%s is missing", k)
		case got != want:
			d.tickets.differ++
			note(&d.tickets.first, "%s is %s, client-%02d saved %s", k, describe(got), t.ev.tWriter[k], describe(want))
		}
	}
	for k := range tickets {
		if _, ok := t.ev.ticket[k]; !ok {
			d.tickets.extra++
			note(&d.tickets.first, "%s was never saved", k)
		}
	}

	baskets := map[key]store.Basket{}
	for _, b := range bf.Baskets {
		baskets[key{b.Prefix, b.BID}] = b
	}
	d.baskets.saved = len(t.ev.basket)
	for k, want := range t.ev.basket {
		got, ok := baskets[k]
		switch {
		case !ok:
			d.baskets.missing++
			note(&d.baskets.first, "basket %s is missing", k)
		case got != want:
			d.baskets.differ++
			note(&d.baskets.first, "basket %s is %+v, saved %+v", k, got, want)
		}
	}
	for k := range baskets {
		if _, ok := t.ev.basket[k]; !ok {
			d.baskets.extra++
			note(&d.baskets.first, "basket %s was never saved", k)
		}
	}

	prefixes := map[string]store.Prefix{}
	for _, p := range bf.Prefixes {
		prefixes[p.Prefix] = p
	}
	d.prefixes, d.onServer = len(t.ev.prefixes), len(bf.Prefixes)
	d.prefixesSame = len(prefixes) == len(t.ev.prefixes)
	for _, p := range t.ev.prefixes {
		d.prefixesSame = d.prefixesSame && prefixes[p.Prefix] == p
	}
	return d, nil
}

// checkServer turns diffServer into verdicts.
func (t *test) checkServer(when string) {
	d, err := t.diffServer()
	if err != nil {
		t.record("the server's data downloads ("+when+")", false, "%v", err)
		return
	}
	tk, bk := d.tickets, d.baskets
	t.record("the server holds every ticket as last saved ("+when+")", tk.clean(),
		"%d tickets: %d missing, %d different, %d unexpected%s", tk.saved, tk.missing, tk.differ, tk.extra, examples(tk.first))
	t.record("the server holds every basket and winner as saved ("+when+")", bk.clean(),
		"%d baskets: %d missing, %d different, %d unexpected%s", bk.saved, bk.missing, bk.differ, bk.extra, examples(bk.first))
	t.record("the server holds every prefix ("+when+")", d.prefixesSame, "%d saved, %d on the server", d.prefixes, d.onServer)
}

// checkMirrors compares every client's own copy with what that client saved
// last: its pages fall back on that copy whenever the server is away.
func (t *test) checkMirrors() {
	var mu sync.Mutex
	var first []string
	differ, affected := 0, 0
	t.each(func(l *client) {
		var bf store.BackupFile
		if _, err := l.call(nil, "", http.MethodGet, "/api/backuprestore/local", nil, &bf, 0); err != nil {
			mu.Lock()
			affected++
			note(&first, "%s: %v", l.prog.name, err)
			mu.Unlock()
			return
		}
		tickets := map[key]store.Ticket{}
		for _, tk := range bf.Tickets {
			tickets[key{tk.Prefix, tk.TID}] = tk
		}
		baskets := map[key]store.Basket{}
		for _, b := range bf.Baskets {
			baskets[key{b.Prefix, b.BID}] = b
		}
		n := 0
		var mine []string
		t.ev.mu.Lock()
		for k, want := range t.ev.ticket {
			if t.ev.tWriter[k] != l.n {
				continue
			}
			if got, ok := tickets[k]; !ok || got != want {
				n++
				note(&mine, "%s: %s shows %s, it saved %s", l.prog.name, k, describe(got), describe(want))
			}
		}
		for k, want := range t.ev.basket {
			got, ok := baskets[k]
			if t.ev.bWriter[k] == l.n && (!ok || got.Description != want.Description || got.Donors != want.Donors) {
				n++
				note(&mine, "%s: basket %s shows %q, it saved %q", l.prog.name, k, got.Description, want.Description)
			}
			if t.ev.wWriter[k] == l.n && (!ok || got.WinningTicket != want.WinningTicket) {
				n++
				note(&mine, "%s: basket %s shows winner %d, it saved %d", l.prog.name, k, got.WinningTicket, want.WinningTicket)
			}
		}
		t.ev.mu.Unlock()
		if n > 0 {
			mu.Lock()
			differ += n
			affected++
			for _, m := range mine {
				note(&first, "%s", m)
			}
			mu.Unlock()
		}
	})
	t.record("every client's own copy shows what it saved", affected == 0,
		"%d clients; %d rows differ on %d of them%s", len(t.clients), differ, affected, examples(first))
}

// checkClients reads every client's status bar at the end.
func (t *test) checkClients() {
	var mu sync.Mutex
	away, waiting, failed := 0, 0, 0
	t.each(func(l *client) {
		st, err := l.peek()
		mu.Lock()
		defer mu.Unlock()
		if err != nil || st.State != "connected" {
			away++
		}
		waiting += st.Pending
		failed += st.Failed
	})
	t.record("every client ends connected with nothing waiting or refused", away+waiting+failed == 0,
		"%d clients: %d not connected, %d saves waiting, %d refused by the server", len(t.clients), away, waiting, failed)
}

// checkPresence reads the Clients table of the server's admin page.
func (t *test) checkPresence() {
	st, _, err := t.admin.status()
	if errors.Is(err, errNoTable) {
		t.skip("the admin page's Clients table lists every client as connected and caught up", "%v", err)
		t.skip("the admin page counts every prefix, ticket and basket", "%v", err)
		return
	}
	if err != nil {
		t.record("the admin page's Clients table lists every client as connected and caught up", false, "%v", err)
		return
	}
	connected, updated, caughtUp := 0, 0, 0
	for _, l := range st.Clients {
		if l.State == "connected" {
			connected++
		}
		if l.LastUpdate != "" && l.LastUpdate != "never" {
			updated++
		}
		if l.Queued != nil && *l.Queued == 0 {
			caughtUp++
		}
	}
	n := len(t.clients)
	t.record("the admin page's Clients table lists every client as connected and caught up",
		len(st.Clients) == n && connected == n && updated == n && caughtUp == n,
		"%d rows for %d clients: %d connected, %d with a last update, %d with nothing queued", len(st.Clients), n, connected, updated, caughtUp)
	t.ev.mu.Lock()
	tickets, baskets := len(t.ev.ticket), len(t.ev.basket)
	t.ev.mu.Unlock()
	t.record("the admin page counts every prefix, ticket and basket",
		st.Prefixes == len(t.ev.prefixes) && st.Tickets == tickets && st.Baskets == baskets,
		"%d prefixes, %d tickets, %d baskets (saved: %d, %d, %d)", st.Prefixes, st.Tickets, st.Baskets, len(t.ev.prefixes), tickets, baskets)
}

// checkRequests turns what was seen along the way into verdicts.
func (t *test) checkRequests() {
	requests, errs := 0, 0
	var first []string
	for _, ph := range t.phases {
		r, e, _, _, f := ph.rec.totals()
		requests, errs = requests+r, errs+e
		for _, m := range f {
			note(&first, "%s", m)
		}
	}
	t.record("every request was answered", errs == 0, "%d requests, %d failed%s", requests, errs, examples(first))

	if t.outageOn() {
		o := t.outage
		t.record("the server started again after being killed", o.restartErr == nil, "%v", o.restartErr)
	}
	// A save is queued while the server is away and, after that, while the
	// client still has saves from then to send: from the moment the server
	// was killed (a save already on its way may be cut off too, hence the
	// client's five-second write timeout of slack) until the client shows
	// nothing queued. Any other queued save means the server was too slow.
	total, unexpected := 0, 0
	for _, l := range t.clients {
		l.mu.Lock()
		for _, at := range l.queuedAt {
			total++
			if !l.queuedRightly(at) {
				unexpected++
			}
		}
		l.mu.Unlock()
	}
	t.record("saves were queued only while the server was out of reach", unexpected == 0,
		"%d saves queued, %d of them while the client could reach the server", total, unexpected)

	// A page waits for the server five seconds at most, then works from the
	// client's own copy; a second more covers the rest of the work.
	const patience = 6 * time.Second
	var slowOp, slowPhase string
	var slow time.Duration
	for _, ph := range t.phases {
		if op, took := ph.rec.slowest(); took > slow {
			slowOp, slowPhase, slow = op, ph.name, took
		}
	}
	t.record("no page action waited more than 6 s", slow <= patience, "the slowest: %s in %s, %s", slowOp, slowPhase, ms(slow))
	if t.outageOn() {
		inside := 0
		for _, l := range t.clients {
			l.mu.Lock()
			if l.backlog > 0 {
				inside++
			}
			l.mu.Unlock()
		}
		// Not a verdict: how many clients went back to their offline sheets
		// while they still had saves to send, the case the ordering matters in.
		t.record("every client saw the server again after the restart", t.problemFree("reconnect"),
			"%d of %d clients went back to their offline sheets while saves were still queued%s", inside, len(t.clients), t.problemText("reconnect"))
	}

	t.record("every sheet showed all its rows", t.problemFree("sheet size"), "%s", t.problemText("sheet size"))
	t.record("a sheet opened again showed what was saved", t.problemFree("stale sheet"), "%s", t.problemText("stale sheet"))
	t.record("reports, searches and winner lookups match the data", t.problemFree("report"), "%s", t.problemText("report"))
	t.record("the clients sent everything they queued", t.problemFree("settle"), "%s", t.problemText("settle"))
	if t.outageOn() && t.o.crashes > 0 {
		t.record("every crashed client came back with its queue", t.problemFree("crash"), "%d crashed%s", t.outage.crashed, t.problemText("crash"))
	}
	if len(t.wifi.dropped) > 0 {
		t.record("every client whose Wi-Fi dropped sent what it queued", t.problemFree("reconnect"), "clients %v%s", t.wifi.dropped, t.problemText("reconnect"))
	}
	t.record("a client whose key was deleted said so, and pairing again sent its queue", t.problemFree("key"), "%s", t.problemText("key"))
	if t.o.storm > 0 {
		t.record("tickets everyone saved at once end whole, and every client shows them", t.problemFree("storm"), "%s", t.problemText("storm"))
	}
}

func (t *test) problemFree(kind string) bool {
	n, _ := t.problems.get(kind)
	return n == 0
}

func (t *test) problemText(kind string) string {
	n, first := t.problems.get(kind)
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d times%s", n, examples(first))
}

// checkShutdown looks at how the clients stopped at the end.
func (t *test) checkShutdown() {
	clean := 0
	for _, l := range t.clients {
		l.prog.mu.Lock()
		if l.prog.stopped {
			clean++
		}
		l.prog.mu.Unlock()
	}
	t.record("every client shut down cleanly when asked", clean == len(t.clients), "%d of %d", clean, len(t.clients))
}

// logTrouble are the log lines that mean something went wrong inside a
// program. The expected ones (unreachable while the server was down,
// connected again, took a queued save) are not among them.
var logTrouble = regexp.MustCompile(`internal error|database is locked|SQLITE_BUSY|panic|fatal error|DATA RACE|mirror: |outbox: |refused a queued|unreadable backup|invalid backup|record last_(seen|update)|admin: render`)

// checkLogs reads everything every program wrote: its log lines, which it
// also keeps in its data folder, and anything else on its console, such as
// a crash or a report of the race detector.
func (t *test) checkLogs() {
	var files []string
	if !t.server.external {
		files = append(files, filepath.Join(t.server.dir, "console.log"))
	}
	for _, l := range t.clients {
		files = append(files, filepath.Join(l.prog.dir, "console.log"))
	}
	lines, bad := 0, 0
	var first []string
	for _, name := range files {
		f, err := os.Open(name)
		if err != nil {
			bad++
			note(&first, "%v", err)
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			lines++
			if logTrouble.MatchString(sc.Text()) {
				bad++
				note(&first, "%s: %s", filepath.Base(filepath.Dir(name)), sc.Text())
			}
		}
		f.Close()
	}
	t.record("no errors in what the programs wrote", bad == 0, "%d programs, %d lines, %d errors%s", len(files), lines, bad, examples(first))
}

// errNoTable is a server whose status page answers HTML only, such as one
// from before the page could be read as JSON: there is no table to check.
var errNoTable = errors.New("this server's status page answers HTML only")

// adminPage is the server's admin page, logged in as the tests's browser.
type adminPage struct {
	url, password string

	mu sync.Mutex
	c  *http.Client
}

// adminStatus is the status page's JSON.
type adminStatus struct {
	Prefixes int `json:"prefixes"`
	Tickets  int `json:"tickets"`
	Baskets  int `json:"baskets"`
	Clients  []struct {
		Name       string `json:"name"`
		Program    string `json:"program"`
		State      string `json:"state"`
		LastSeen   string `json:"last_seen"`
		LastUpdate string `json:"last_update"`
		Queued     *int   `json:"queued"`
	} `json:"clients"`
}

var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// login signs in with the server password through the login form.
func (a *adminPage) login() error {
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 30 * time.Second, Transport: web.Transport}
	res, err := c.Get(a.url + "/admin/")
	if err != nil {
		return err
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	m := csrfField.FindSubmatch(page)
	if m == nil {
		return fmt.Errorf("the admin page has no login form")
	}
	res, err = c.PostForm(a.url+"/admin/login", url.Values{"csrf": {string(m[1])}, "password": {a.password}})
	if err != nil {
		return err
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("the admin login answered %d", res.StatusCode)
	}
	a.mu.Lock()
	a.c = c
	a.mu.Unlock()
	return nil
}

// status reads the status page as JSON, logging in first when needed (a
// restarted server has forgotten the session).
func (a *adminPage) status() (adminStatus, time.Duration, error) {
	var st adminStatus
	for attempt := 0; attempt < 2; attempt++ {
		a.mu.Lock()
		c := a.c
		a.mu.Unlock()
		if c == nil {
			if err := a.login(); err != nil {
				return st, 0, err
			}
			a.mu.Lock()
			c = a.c
			a.mu.Unlock()
		}
		req, _ := http.NewRequest(http.MethodGet, a.url+"/admin/status", nil)
		req.Header.Set("Accept", "application/json")
		start := time.Now()
		res, err := c.Do(req)
		if err != nil {
			return st, 0, err
		}
		took := time.Since(start)
		if res.StatusCode == http.StatusOK && !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
			res.Body.Close()
			return st, took, errNoTable
		}
		if res.StatusCode == http.StatusUnauthorized {
			res.Body.Close()
			a.mu.Lock()
			a.c = nil
			a.mu.Unlock()
			continue
		}
		return st, took, decodeJSON(res, &st)
	}
	return st, 0, fmt.Errorf("the admin page did not accept the login")
}

// watch reloads the status page every five seconds, as the open admin page
// does, until stop is closed. While the server is down it waits.
func (a *adminPage) watch(t *test, stop <-chan struct{}) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		if !t.server.up.Load() {
			continue
		}
		_, took, err := a.status()
		if err != nil && !t.server.up.Load() {
			continue // it went down during the request
		}
		if errors.Is(err, errNoTable) {
			err = nil // the page answered; it only has no table to read
		}
		t.current().rec.add("admin status page", took, err, 0, false)
	}
}

// report prints the phases, the programs' use of the machine and the
// verdicts.
func (t *test) report(runErr error, took time.Duration) {
	var b strings.Builder
	fmt.Fprintf(&b, "\nPhases\n")
	for _, ph := range t.phases {
		requests, _, rows, _, _ := ph.rec.totals()
		d := ph.duration()
		fmt.Fprintf(&b, "  %s: %s, %d requests (%.0f a second)", ph.name, secs(d), requests, float64(requests)/d.Seconds())
		if saves := ph.rec.saves(); rows > 0 {
			fmt.Fprintf(&b, ", %d rows saved in %d saves (%.0f rows and %.0f saves a second)", rows, saves, float64(rows)/d.Seconds(), float64(saves)/d.Seconds())
		}
		b.WriteString("\n")
		if ph.note != "" {
			fmt.Fprintf(&b, "    %s\n", ph.note)
		}
		ph.rec.table(&b)
	}

	t.soakTable(&b)
	fmt.Fprintf(&b, "\nPrograms\n")
	if t.server != nil && t.server.external {
		fmt.Fprintf(&b, "  tam-server %s at %s: on its own machine, which measures its own CPU and memory\n", t.version, t.server.url)
	} else if t.server != nil {
		cpu, peak, runs := t.server.usage()
		size := int64(0)
		for _, f := range []string{"tam-remote.db", "tam-remote.db-wal"} {
			if fi, err := os.Stat(filepath.Join(t.server.dir, f)); err == nil {
				size += fi.Size()
			}
		}
		fmt.Fprintf(&b, "  tam-server %s: CPU %s over %d runs, peak memory %s, database %s\n", t.version, secs(cpu), runs, peakText(peak), megabytes(uint64(size)))
	}
	if len(t.clients) > 0 {
		var cpu time.Duration
		var peak uint64
		for _, l := range t.clients {
			c, p, _ := l.prog.usage()
			cpu += c
			peak = max(peak, p)
		}
		fmt.Fprintf(&b, "  tam-client x%d: CPU %s in all (%s each on average), peak memory %s for the largest\n",
			len(t.clients), secs(cpu), secs(cpu/time.Duration(len(t.clients))), peakText(peak))
	}
	fmt.Fprintf(&b, "  this machine: %s/%s, %d CPUs; the test ran %s\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), secs(took))

	fmt.Fprintf(&b, "\nChecks\n")
	failed := 0
	for _, c := range t.checks {
		verdict := "PASS"
		switch {
		case c.skipped:
			verdict = "SKIP"
		case !c.passed:
			verdict = "FAIL"
			failed++
		}
		fmt.Fprintf(&b, "  %s  %s", verdict, c.name)
		if c.detail != "" && c.detail != "<nil>" {
			fmt.Fprintf(&b, ": %s", c.detail)
		}
		b.WriteString("\n")
	}
	skipped, counted := "", len(t.checks)-t.skipped()
	if n := t.skipped(); n > 0 {
		skipped = fmt.Sprintf(" (%d skipped: this server cannot answer them)", n)
	}
	switch {
	case runErr != nil:
		fmt.Fprintf(&b, "\nThe run stopped early: %v\n", runErr)
	case failed > 0:
		fmt.Fprintf(&b, "\nFAILED: %d of %d checks%s\n", failed, counted, skipped)
	default:
		fmt.Fprintf(&b, "\nPASSED: all %d checks%s\n", counted, skipped)
	}
	fmt.Print(b.String())
}

func peakText(n uint64) string {
	if n == 0 {
		return "not measured here"
	}
	return megabytes(n)
}
