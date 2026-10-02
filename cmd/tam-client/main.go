// Command tam-client serves the Ticket Auction Manager web app and its API
// on a computer at the venue, against a local database or a remote tam-server.
//
//go:generate go-winres make --in winres/winres.json --arch amd64,arm64 --out rsrc
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"ticket-auction-manager/tam-go/internal/client"
	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/desktop"
	"ticket-auction-manager/tam-go/internal/env"
	"ticket-auction-manager/tam-go/internal/store"
)

//go:embed all:dist
var distFS embed.FS

//go:embed icon.ico
var iconICO []byte

// banner is Dilan's start-up art; it ends with "Now hosting ... at:" and
// the address follows on the next line.
//
//go:embed asciiart.txt
var banner string

func main() {
	addr := flag.String("addr", "localhost:3080", "address to listen on")
	open := flag.Bool("open", true, "open the web app in the default browser once it is listening")
	useTray := flag.Bool("tray", desktop.TraySupported, "show a TAM icon in the notification area with Open and Shut Down entries (Windows)")
	flag.Parse()
	desktop.SetConsoleTitle("Ticket Auction Manager - client")

	dataDir, err := env.DataDir()
	if err != nil {
		log.Fatal(err)
	}
	if f, err := env.OpenLog(dataDir, "tam-client.log"); err == nil {
		defer f.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, f))
	} else {
		log.Printf("not keeping a log file: %v", err)
	}
	sqldb, err := db.Open(filepath.Join(dataDir, "tam-local.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer sqldb.Close()
	if err := db.Migrate(sqldb); err != nil {
		log.Fatal(err)
	}
	if err := db.MigrateClient(sqldb); err != nil {
		log.Fatal(err)
	}
	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		log.Fatal(err)
	}

	// stop ends the program cleanly. The page's Shut Down button, the tray
	// icon, Ctrl+C, and closing the console window all come through here.
	var (
		srv      *http.Server
		stopOnce sync.Once
	)
	shutdownDone := make(chan struct{})
	stop := func() {
		stopOnce.Do(func() {
			go func() {
				defer close(shutdownDone)
				// Let the "shutting down" answer reach the page first.
				time.Sleep(300 * time.Millisecond)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				srv.Shutdown(ctx)
			}()
		})
	}
	// The heartbeat and outbox replay run until the server stops serving,
	// and stop before the database closes.
	syncCtx, stopSync := context.WithCancel(context.Background())
	defer stopSync()
	syncStopped := make(chan struct{})
	srv = &http.Server{
		Handler: client.NewHandler(store.New(sqldb), filepath.Join(dataDir, "settings.json"), dist,
			client.WithShutdown(stop), client.WithSyncLoop(syncCtx), client.WithSyncStopped(syncStopped)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	// Listen first so the browser is only opened once the port is really ours.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	url := "http://" + browserHost(ln.Addr().(*net.TCPAddr)) + "/"
	fmt.Print(banner)
	fmt.Println(url)
	log.Printf("tam-client listening on %s (data in %s)", url, dataDir)
	if *open {
		go func() {
			time.Sleep(300 * time.Millisecond)
			if err := desktop.OpenBrowser(url); err != nil {
				log.Printf("could not open the browser (%v); open %s yourself", err, url)
			}
		}()
	}

	done := make(chan struct{})
	var serveErr error
	go func() {
		defer close(done)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			serveErr = err
		}
	}()
	go desktop.StopOnSignal(stop, done)

	if *useTray {
		desktop.Tray(desktop.Options{
			Tooltip:   "Ticket Auction Manager - client on " + url,
			Icon:      iconICO,
			OpenURL:   url,
			QuitLabel: "Shut Down TAM",
			Quit:      stop,
		}, done)
	} else {
		<-done
	}
	// Serve returns when Shutdown closes the listener, before active saves
	// finish. Keep the database and syncer alive through the bounded drain.
	stop()
	<-shutdownDone
	stopSync()
	select {
	case <-syncStopped:
	case <-time.After(3 * time.Second): // a request of the replay still on its way
	}
	if serveErr != nil {
		log.Fatal(serveErr)
	}
	log.Print("tam-client stopped")
}

// browserHost turns the bound address into something a browser on this
// machine can open: an unspecified address becomes localhost.
func browserHost(a *net.TCPAddr) string {
	host := "localhost"
	if a.IP != nil && !a.IP.IsUnspecified() {
		host = a.IP.String()
	}
	return net.JoinHostPort(host, strconv.Itoa(a.Port))
}
