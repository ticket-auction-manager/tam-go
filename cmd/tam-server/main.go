// Command tam-server is the shared Ticket Auction Manager database that
// several tam-client installations talk to in remote mode.
//
//go:generate go-winres make --in winres/winres.json --arch amd64,arm64 --out rsrc
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ticket-auction-manager/tam-go/internal/admin"
	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/desktop"
	"ticket-auction-manager/tam-go/internal/discovery"
	"ticket-auction-manager/tam-go/internal/env"
	"ticket-auction-manager/tam-go/internal/guard"
	"ticket-auction-manager/tam-go/internal/presence"
	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
	"ticket-auction-manager/tam-go/internal/tlscert"
	"ticket-auction-manager/tam-go/internal/version"
)

//go:embed icon.ico
var iconICO []byte

// banner is Dilan's start-up art; it ends with "Now hosting ... at:" and
// the address follows on the next line.
//
//go:embed asciiart.txt
var banner string

// reachableURLs lists the addresses a client elsewhere on the network can
// use: the listen address itself when it names an interface, otherwise
// this machine's addresses with the listen port.
func reachableURLs(scheme, addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return []string{scheme + "://" + net.JoinHostPort(host, port) + "/"}
	}
	var out []string
	for _, ip := range discovery.LocalAddresses() {
		out = append(out, scheme+"://"+net.JoinHostPort(ip, port)+"/")
	}
	return out
}

// browseAddr turns a listen address into one a browser on this machine can
// open: ":8000" listens everywhere, so it is reachable as localhost:8000.
func browseAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}

func main() {
	addr := flag.String("addr", "", "address to listen on (default :8000, or :8443 with -tls)")
	useTLS := flag.Bool("tls", false, "serve HTTPS; without -cert and -key, with a self-signed certificate created in the data directory on first start")
	certFile := flag.String("cert", "", "TLS certificate file (PEM) to serve with -tls; it must exist, and -key goes with it (default <data dir>/server.crt, created when missing)")
	keyFile := flag.String("key", "", "TLS key file (PEM) of -cert; it must exist (default <data dir>/server.key, created when missing)")
	useTray := flag.Bool("tray", desktop.TraySupported, "show a TAM icon in the notification area with a Shut Down entry (Windows)")
	announce := flag.Bool("announce", true, "announce this server on the local network (mDNS) so clients can find it in Settings")
	flag.Parse()
	desktop.SetConsoleTitle("Ticket Auction Manager - server")

	// "dev" binds the loopback interface unless an address was given.
	if *addr == "" {
		host := ""
		if flag.Arg(0) == "dev" {
			host = "localhost"
		}
		if *useTLS {
			*addr = host + ":8443"
		} else {
			*addr = host + ":8000"
		}
	}
	scheme := "http"
	if *useTLS {
		scheme = "https"
	}
	reachable := reachableURLs(scheme, *addr)

	dataDir, err := env.DataDir()
	if err != nil {
		log.Fatal(err)
	}
	if f, err := env.OpenLog(dataDir, "tam-server.log"); err == nil {
		defer f.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, f))
	} else {
		log.Printf("not keeping a log file: %v", err)
	}
	sqldb, err := db.Open(filepath.Join(dataDir, "tam-remote.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer sqldb.Close()
	if err := db.Migrate(sqldb); err != nil {
		log.Fatal(err)
	}
	if err := db.MigrateServer(sqldb); err != nil {
		log.Fatal(err)
	}

	// The password comes from server.json in the data directory, which the
	// admin page writes, or else from TAM_PWD. With neither the server runs
	// in setup mode: the API refuses to create keys until the admin page
	// has set a password.
	password, err := admin.Load(dataDir, os.Getenv("TAM_PWD"))
	if err != nil {
		log.Fatal(err)
	}

	// stop ends the program cleanly: the tray icon, Ctrl+C, and closing the
	// console window all come through here.
	var (
		srv      *http.Server
		stopOnce sync.Once
	)
	shutdownDone := make(chan struct{})
	stop := func() {
		stopOnce.Do(func() {
			go func() {
				defer close(shutdownDone)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				srv.Shutdown(ctx)
			}()
		})
	}
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		absDataDir = dataDir
	}
	st := store.New(sqldb)
	// The API records what every client does; the admin page shows it.
	clients := presence.New(nil)
	// One limit on wrong passwords per address covers the admin login and
	// the API's key routes, so guesses cannot be split between the two.
	guesses := guard.New()
	mux := http.NewServeMux()
	adminPages := admin.NewHandler(st, password, admin.Info{Addr: *addr, Addresses: reachable, TLS: *useTLS, DataDir: absDataDir, Version: version.Version, Started: time.Now(), Presence: clients}, admin.WithGuesses(guesses))
	mux.Handle("/admin", adminPages)
	mux.Handle("/admin/", adminPages)
	mux.Handle("/favicon.ico", adminPages)
	mux.Handle("/", server.NewHandler(st, password, server.WithPresence(clients), server.WithGuesses(guesses)))
	srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	if *useTLS {
		hostname, _ := os.Hostname()
		cert, key, created, err := tlscert.Files(dataDir, *certFile, *keyFile, []string{"localhost", hostname, "127.0.0.1", "::1"})
		if err != nil {
			log.Fatal(err)
		}
		*certFile, *keyFile = cert, key
		if created {
			log.Printf("created a self-signed certificate at %s (clients with Remote TLS on accept it)", cert)
		}
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(banner)
	fmt.Printf("%s://%s/\n", scheme, browseAddr(*addr))
	// The addresses a client on the network can be pointed at by hand when
	// the network drops the announcement.
	for _, u := range reachable {
		fmt.Println(u)
	}
	log.Printf("tam-server listening on %s://%s (data in %s)", scheme, *addr, dataDir)
	if !password.IsSet() {
		log.Printf("no password set: open %s://%s/admin to set one", scheme, browseAddr(*addr))
	}
	if *announce {
		announceCtx, stopAnnounce := context.WithCancel(context.Background())
		defer stopAnnounce()
		name, _ := os.Hostname()
		if name == "" {
			name = "TAM Server"
		}
		if err := discovery.Announce(announceCtx, name, ln.Addr().(*net.TCPAddr).Port, *useTLS, version.Version); err != nil {
			log.Printf("not announcing on the network (%v); clients can still type the address", err)
		} else {
			log.Printf("announcing as %q on the local network", name)
		}
	}

	done := make(chan struct{})
	var serveErr error
	go func() {
		defer close(done)
		var err error
		if *useTLS {
			err = srv.ServeTLS(ln, *certFile, *keyFile)
		} else {
			err = srv.Serve(ln)
		}
		if err != nil && err != http.ErrServerClosed {
			serveErr = err
		}
	}()
	go desktop.StopOnSignal(stop, done)

	if *useTray {
		desktop.Tray(desktop.Options{
			Tooltip:   "Ticket Auction Manager - server on " + *addr,
			Icon:      iconICO,
			QuitLabel: "Shut Down TAM Server",
			Quit:      stop,
		}, done)
	} else {
		<-done
	}
	// Serve returns when Shutdown closes the listener, before active saves
	// finish. Keep the database open until the bounded drain has completed.
	stop()
	<-shutdownDone
	if serveErr != nil {
		log.Fatal(serveErr)
	}
	log.Print("tam-server stopped")
}
