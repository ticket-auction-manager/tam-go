# Ticket Auction Manager (Go)

This is Ticket Auction Manager. A project I (Dilan Gilluly) am working on as a hobby project. It's main scope is to manage in person penny socials or benefit auctions.

This is the Go version of it. The remote server (cmd/tam-server directory) and the client (cmd/tam-client directory) are written in Go, with the client's web pages written in Sveltekit (frontend directory) and built into the program, so each one is a single file that runs on Windows, Linux and macOS with nothing to install. The original version, with the server in FastAPI and Python and the client in Sveltekit, is at [ticket-auction-manager/tam](https://github.com/ticket-auction-manager/tam). These programs talk to each other only; an event's data folder or backup file from the original opens here as it is (see [Switching from the original](#switching-from-the-original)).

Features:

- **Forms**: Facilitates the entry of data throughout the platform. The main goal of this is to allow one to manage in-person benefit auctions for non-profit causes.
  - **Ticket Form**: Enter the names and phone numbers of ticket purchasers, as well as their contact preference, which the default is controllable via the Settings screen.
  - **Basket Form**: Optionally, basket/item descriptions can be added as well as who the donor(s) are for each one. The descriptions appear on the reports later on.
  - **Drawing Form**: Use this form to enter the winning ticket numbers. The form automatically looks up if an entry exists when a field is changed and will populate the information next to it if it does.
- **Reports**: Reports are automatically generated with one click to avoid line shifts or other issues which may arise during compilation.
  - **By Name Report**: This report orders the lines by the last name of each winner, then first name, phone number, and finally basket number.
  - **By Basket Report**: Orders winners by basket number.
  - **Counts Report**: Displays counts of ticket sales by prefix as well as totals.
  - **Print Sheets**: Prints blank ticket sheets for a prefix, numbered, to write on at the door.
  - **Ticket Search**: Finds tickets across every prefix by name or phone number, and lets you correct them in place.
- **Settings**:
  - **Settings section**: The Admin/settings section can be accessed on the main menu by pressing Alt(option)+A. The combination toggles it so if you want it to go away again, just press Alt(option)+A again. It also holds the Shut Down TAM button, which stops the program on this computer.
  - **Settings page**: Allows you to control the base options of the operation. Including the server to pair with (leave it out for standalone(offline) mode), remote port, TLS, default contact preference, and the name of the venue/benefit (appears on main menu as well as reports).
  - **Auth Keys**: Allows you to manage auth keys if it's in remote mode. To do so, you need to know the auth password on the server.
  - **Prefixes**: Allows you to add or change prefixes which are available on the main menu to be able to access the respective forms for each prefix. Note, **you need to add prefixes through this form after first installation of either client or server to be able to access forms and reports.**
  - **Backup/Restore**: Downloads the data of this computer or of the server as one file, and restores such a file into either.
- **Remote Mode**: Remote mode, which is configurable in the Settings screen, allows data to be synched across multiple computers for large scale operations. Servers on the venue's network show up in the Settings screen by name, and pairing is done by entering the server password once. A computer that loses the server keeps working from its own copy of the data, queues what it saves, and delivers it when the server is back; the bar at the top of every page says Connected, Reconnecting or Offline, and how many saves are waiting. An older record never overwrites a newer one: a change made on a page left open, or saved while a computer was offline, never replaces what another computer saved since, and its volunteer is told (see [Remote mode](#remote-mode)).
- **Server admin page**: The server has its own pages in the browser, protected by the server password, which is set on the first visit. The Status page lists every paired computer with whether it is connected, when it was last seen, when it last saved, and how many saves it still has to deliver, so you can tell when everyone has caught up. Keys, backups and the password are managed there as well.
- **Runs everywhere**: One program per machine. On Windows an exe with an icon in the notification area; on Linux a `.deb`, an `.rpm` or a tarball with systemd units; on macOS with launchd files; or Docker. TLS is built in.

## Screenshots

The client, on a computer paired with a server (the bar at the top says so):

![Main menu](docs/screenshots/client-main-menu.png)

The Ticket Form. Rows are loaded by the pager at the top, and the row buttons duplicate, copy and paste entries:

![Ticket Form](docs/screenshots/client-tickets.png)

The Basket Form and the Drawing Form. The Drawing Form looks the winner up as the ticket number is typed:

![Basket Form](docs/screenshots/client-baskets.png)

![Drawing Form](docs/screenshots/client-drawing.png)

The reports:

![Winners by Name](docs/screenshots/client-report-by-name.png)

![Winners by Basket](docs/screenshots/client-report-by-basket.png)

![Ticket Counts](docs/screenshots/client-counts.png)

Ticket search across every prefix:

![Ticket Search](docs/screenshots/client-search.png)

The server went away: the pages keep working from the computer's own copy, and the save waits for the server to come back:

![Offline](docs/screenshots/client-offline.png)

Settings on a computer that has not paired yet, with the server it found on the network, and on one that has:

![Settings, pairing](docs/screenshots/client-settings-pairing.png)

![Settings, paired](docs/screenshots/client-settings.png)

![Prefixes](docs/screenshots/client-prefixes.png)

![Print Sheets](docs/screenshots/client-print-sheets.png)

The server's admin page. The first visit sets the password; from then on the Status page shows every computer:

![First visit](docs/screenshots/server-first-visit.png)

![Log in](docs/screenshots/server-login.png)

![Status](docs/screenshots/server-status.png)

![Keys](docs/screenshots/server-keys.png)

![Backup](docs/screenshots/server-backup.png)

## Downloading

The [releases page](https://github.com/ticket-auction-manager/tam-go/releases) has one file per program and system. Take `tam-client` for a computer at the event and `tam-server` for the machine that hosts the server:

- **Windows**: `tam-client-<version>-windows-amd64.exe` or `tam-server-<version>-windows-amd64.exe` (`-windows-arm64` for a Snapdragon machine). Put it in a folder of its own and double-click it. The `.zip` of the same name adds this README. The programs are not signed, so SmartScreen asks once: More info, then Run anyway.
- **Debian, Ubuntu, Mint and their relatives**: the `.deb` of the program (`sudo apt install ./tam-server_<version>-1_amd64.deb`, where `-1` is the package revision).
- **Fedora, RHEL, Rocky, Alma and their relatives**: the `.rpm` of the program (`sudo dnf install ./tam-server-<version>-1.x86_64.rpm`).
- **Any other Linux**: the `-linux-amd64.tar.gz` (or `-linux-arm64`) of the program, with an installer script for systemd; run `chmod +x` on the program if your unzip tool dropped the executable bit.
- **macOS** 13 or later: the `-darwin-arm64.tar.gz` (Apple silicon) or `-darwin-amd64.tar.gz` (Intel) of the program, with a launchd file; clear the quarantine flag once with `xattr -dr com.apple.quarantine tam-client` (or `tam-server`).
- **NixOS**: nothing to download; the flake in this repository builds both programs and has a NixOS module for them (see NixOS under [Deployment](#deployment)).

Then, on the server's machine, open `http://<that machine>:8000/admin` and set the server password. On each other computer start the client, press Alt(option)+A, open Settings, pick the server from the list, enter the password once and press Pair. See [Deployment](#deployment) for the details per system.

## Cloning the repo

To clone the repo you just need to run the git clone command to clone it to a directory of your choosing. Replace 'yourrepofolder' at the end with the folder/dir of your choosing.

Github:

`git clone https://www.github.com/Ticket-Auction-Manager/tam-go yourrepofolder`

## Installing dependencies

Server and client:

(needs Go 1.27 or newer installed)

Nothing to install: `go build` fetches the Go modules.

Client web pages:

(needs pnpm installed)

```
cd frontend
pnpm install
```

## Building

```
./build.sh client       # the web pages, then tam-client into build/
./build.sh server       # tam-server into build/
./build.sh all          # both
./build.sh release      # every system: build/<os>-<arch>/ and one archive per program and target
```

With Nix, `nix build` builds both programs into `result/bin` (see NixOS under [Deployment](#deployment)).

`./build.sh release` builds the web pages once, then both programs for Windows, Linux and macOS on amd64 and arm64 (`CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w"`) into `build/<os>-<arch>/`, and packs each program of each target into `build/tam-server-<version>-<os>-<arch>.zip` and `build/tam-client-<version>-<os>-<arch>.zip` (Windows, where the bare program is also copied to `build/tam-server-<version>-windows-<arch>.exe` and `build/tam-client-<version>-windows-<arch>.exe`) or `.tar.gz` (Linux, macOS): one folder with that program, `README.md`, `LICENSE.md` and its files from `deploy/linux` or `deploy/macos` (its unit and `install.sh`, plus the application-menu entry for the client; its launchd file and the macOS notes as `INSTALL.md`). For Linux it also writes a `.deb` and an `.rpm` of each program with [nfpm](https://nfpm.goreleaser.com), from `deploy/linux/nfpm`, which `build.sh` installs with `go install` when it is not on the PATH. The version stamped into both programs, `internal/version.Version`, is `$VERSION` when set and otherwise `git describe --tags --always --dirty`; both programs print it in their banner and the server reports it on `GET /api` and its admin page. `SKIP_WEB=1` keeps an existing `cmd/tam-client/dist`. A tag `1.2.3` (or `v1.2.3`) becomes version 1.2.3 everywhere: the programs' banners and `GET /api`, the admin page, the Windows file properties, the `.deb` and `.rpm` versions and every file name; a tag `1.2.3-rc1` is a pre-release, marked so on GitHub and sorted before 1.2.3 by apt and dnf. Anything else, such as the bare commit that `git describe` gives in a clone without tags, makes packages of version `0.0.0~<commit>`, below every release. `PKG_RELEASE` is the package revision, the `-1` in the package file names; set it to 2 or more to build the packages of a version again. To cut a release: `git tag -a 1.2.3 -m "1.2.3"` on the commit, then `git push origin 1.2.3`. The archives are written with `zip` and `tar` where those exist (the files in a `.tar.gz` belong to root) and with Python otherwise. Both Windows builds carry the TAM icons and version information from the `rsrc_windows_*.syso` files, which `go generate ./cmd/...` makes with [go-winres](https://github.com/tc-hib/go-winres) from `cmd/*/winres/winres.json`; a release build remakes them with the release version first and puts the committed files back afterwards.

Releases come from `.github/workflows/release.yml`: pushing a version tag runs the unit tests and then `VERSION=<tag> ./build.sh release` on GitHub, and attaches the twelve archives (two programs, six targets), the four bare Windows programs, the eight Linux packages and a `SHA256SUMS` file with their checksums to a GitHub release of that tag. The release's description is `docs/release-notes.md` with the version filled in (what to download for which machine, the first start, upgrading), followed by the list of changes GitHub generates. `ci.yml` vets, tests and cross-compiles all six targets on every push, runs the browser tests, runs the unit tests and a load test with the race detector, builds the Nix package and runs its NixOS test, and makes a release build with the version from `git describe`, keeping its files for two weeks and installing, upgrading and removing its Linux packages; every run keeps what they print (see [Running the tests](#running-the-tests)).

## Running dev instances

Server:

```
go run ./cmd/tam-server -addr 127.0.0.1:8000
```

Client, with the web pages served live by Vite (they proxy `/api` to the client program):

```
go run ./cmd/tam-client -addr 127.0.0.1:3080 -open=false
cd frontend
pnpm dev
```

Or build the pages once (`./build.sh client`) and run `build/tam-client`, which serves them itself and opens the browser.

## Running the tests

The tests need Go and the built web app (`pnpm build` in `frontend/`, as for any build). CI runs all of them on every push and keeps what they print: each run's page on GitHub (Actions) shows the unit tests, the browser tests, the tests with the race detector, the load test's report, the NixOS test, the release build and the Linux package tests, and has them as files to download under Artifacts. [docs/test-results.md](docs/test-results.md) has the printed results of the longer runs.

Unit and integration tests; the client tests drive the real server handler as their server:

```
go test ./...
```

The browser tests drive the web app in Chromium against real programs: marked rows saved when a page is hidden, left or closed; a change from a page, or from a save queued offline, kept out where another client saved a newer value, with the message and Settings' list; reports read from a client's copy saying so. [scripts/browser/README.md](scripts/browser/README.md) says how to run them.

The load test runs a whole event through one real `tam-server` and many real `tam-client` programs on the machine, each client with its own data folder and paired through its Settings route. Ticket entry is paced over 40 seconds, fixing a typo now and then and opening sheets again; a quarter of the way in the server is killed and started again 8 seconds later while the clients keep saving, and each client goes back to correct the sheets it saved meanwhile as soon as it sees the server again. Then another client corrects every 40th ticket, the baskets are entered and drawn (each winner looked up as the page does), every report and a few searches are read, and for 10 seconds every client saves as fast as it can. It then checks every ticket, basket and winner on the server and in each client's own copy, the admin page's Clients table, and everything the programs wrote, and prints the time of every page action:

```
go run ./scripts/loadtest
go run ./scripts/loadtest -clients 50 -tickets 9000 -baskets 1000
go run ./scripts/loadtest -h
```

Along the way, every client reaches the server through a relay of its own, its Wi-Fi: a quarter of them lose it silently for 12 seconds between opening a sheet and saving it (the relay holds what was sent and delivers it up to 10 seconds after the link is back, as TCP retransmissions do, so a save the client gave up on can arrive after its replay), and the volunteer types a row again after a save that hung. Two clients crash while the server is down and start again with their queue. An admin deletes a client's key and the volunteer pairs again. Then every client saves the same ten tickets at once for five seconds. The checks include that every save reaches the server in the order it was made, that nothing queued is lost, that tickets saved by everyone end whole and read the same on every client, and that no page waited more than six seconds. `-tls` runs it all over HTTPS; `-h` lists the knobs.

It exits with status 1 when a check fails and then keeps the data folders and logs for a look. `-out <file>` also writes everything it prints to that file, a report to share (with `-server` it names that server's address). `-bin <folder>` tests programs built elsewhere, such as a release or a build with `-race`.

To test over a real network, start `tam-server` on another machine with an empty data folder and a password, and point the clients at it; `-kill` and `-restart` take the commands that kill that server and start it again (through ssh, for example) for the outage, and without them the run has no outage:

```
go run ./scripts/loadtest -server http://<that machine>:8000 -password <its password> -clients 100
```

With Nix, the package and the NixOS module have their own check: it builds the package, which runs the unit tests in the build sandbox, and starts three NixOS machines (it needs KVM). A client finds the server by its announcement, pairs with it, saves a prefix and a ticket, and the ticket is on the server, also after both services restart; a second server serves HTTPS with a certificate of its own; and Shut Down TAM stops the client's service until it is started again:

```
nix flake check -L
```

The Linux packages have a test of their own, `deploy/linux/test-packages.sh`, for a machine or container that is thrown away afterwards. It installs the `.deb` or `.rpm` packages of both programs from one folder, turns the client's service off, upgrades to the packages in a second folder of a higher version, tries the application-menu entry, removes both, installs them again and removes them; a third folder with the 1.0.0-rc1 packages adds the upgrade from those. It checks after every step whether each service is installed, enabled and running, and prints every call the package scripts made to systemctl. With systemd running it also checks that both programs answer, that the client listens on this computer only, the permissions in the data folders, and the server password from `/etc/default/tam-server`; in a container without systemd, `--fake-systemd` puts a stand-in for systemctl there, which keeps each service's state in files. CI runs it on every push, for the `.deb` packages on its Ubuntu machine and for the `.rpm` packages in a Fedora container. To run it the same way with Docker, after `./build.sh release` (and `PKG_RELEASE=2 ./build.sh release` for the higher version, each folder holding one build's packages):

```
sudo deploy/linux/test-packages.sh <old folder> <new folder>
docker run --rm -v "$PWD/deploy/linux:/test:ro" -v "<old folder>:/old:ro" -v "<new folder>:/new:ro" \
  fedora:42 bash /test/test-packages.sh --fake-systemd /old /new
```

## Configuration

| Setting | Where | Default |
|---|---|---|
| `TAM_DATA_DIR` | environment, both daemons | `./data` |
| `TAM_PWD` | environment, tam-server | none: without it, and without a password in `server.json`, the server starts in setup mode and the password is set on the first visit of `/admin`. A password set or changed there is kept, hashed, in `server.json` in the data directory, and wins over `TAM_PWD` |
| `-addr` | flag, both daemons | `localhost:3080` for the client, `:8000` for the server (`:8443` with `-tls`) |
| `-open` | flag, tam-client | `true`: open the web app in the default browser on start |
| `-tray` | flag, both daemons | `true` on Windows: a TAM icon in the notification area with Open (client) and Shut Down entries; use `-tray=false` for services and scripts. Other systems have no icon and stop on Ctrl+C or SIGTERM |
| `-announce` | flag, tam-server | `true`: announce the server on the local network (mDNS) so clients can find it in Settings |
| `dev` | positional argument, tam-server | binds localhost instead of every interface |
| `-tls`, `-cert`, `-key` | flags, tam-server | HTTPS with the PEM files that `-cert` and `-key` name, together; they must exist. Without them, `server.crt` and `server.key` in the data directory, a self-signed pair created there on first start |

`tam-client` keeps `settings.json` in the data directory. It is safe to edit by hand while the daemon runs; the edit is picked up on the next request. Every save also writes a copy, `settings.json.bak`, and the file is flushed to the disk before it replaces the old one. A file that fails to parse (a hand edit with a typo, or a file a power cut left empty) is logged, the pages show a red bar saying so, and the last good settings stay in effect until it is fixed or saved again from the Settings page: the ones in use before the edit, or at start the copy's; only with no good copy at all does the client start with the defaults, standalone, and the bar says that too. Both programs also append everything they print to `tam-client.log` or `tam-server.log` in the data directory, including why they stopped (Ctrl+C, a closed window, the Shut Down button, or the notification-area icon), so the reason is there after the window is gone.

The databases use SQLite's WAL journal, so recent writes may sit in `tam-local.db-wal` next to the main file: copy the whole data folder, or stop the daemon first, when taking a copy by hand. Backup/Restore in the app is the safer route.

```json
{
  "remote_server": "",
  "remote_key": "",
  "remote_port": "8000",
  "remote_tls": false,
  "default_pref": "CALL",
  "venue_name": "Test Venue",
  "disable_attrib": false
}
```

## Remote mode

Remote mode is for events with several clients: one `tam-server` holds the data and every client works against it. It is built for clients that move around and lose wifi: a save never waits for a dead connection and is never lost.

1. Run `tam-server` on the machine that stays put, where every client can reach it. On first start it has no password: open `http://<that machine>:8000/admin` and set one (or start it with `TAM_PWD` set, as the original was). For HTTPS start it with `-tls`: it listens on port 8443 and writes a self-signed certificate (`server.crt`, `server.key`) into its data directory on first start; put your own PEM files there, or point `-cert` and `-key` at them, to use a real certificate. Windows asks once whether to allow the program through the firewall. To stop it, right-click its icon in the notification area and choose **Shut Down TAM Server**, close its console window, or press Ctrl+C in it; the clients' **Shut Down TAM** button only stops the client it is pressed on.
2. On each client press `Alt+A`, open Settings and look at the **Server** section. Servers on the venue network appear there by name: they announce themselves (mDNS, `_tam._tcp`), and because some access points drop multicast, the client also asks the standard ports (8000, and 8443 for TLS) on every address of its own /24 networks, which takes a second or two and finds the server wherever plain traffic gets through. Interfaces that only lead to containers or VMs on the computer itself (Docker, WSL and the like) are left out, and a server seen at several addresses is listed once, at the address the client shares a network with. A server on another port or another subnet is typed in by hand; its admin status page and its start-up banner list the addresses to type. Pick one, enter the server password once and press **Pair**. The client creates its own access key on the server, named after the computer it runs on, and over TLS it pins the server's certificate. When a client that holds data of its own (entered standalone, or a copy of another server's data) pairs with a different server, it first saves that data to `before-pairing-<date>-<time>.json` in its data folder and says so: the server's data then replaces the client's rows with the same numbers, and Backup and Restore can load the file again or send it to the server. **Unpair** returns the client to standalone mode, keeping the copy of the server's data it has. The original way still works too: the remote fields and the Auth Keys page are still there.
3. A bar on every page then shows where the client stands: green **Connected to <server>** (with the number of saves still being sent, if any), amber **Reconnecting** or red **Offline** with the number of saves waiting, red when the server rejected this client's key, and red when the server's certificate is no longer the one pinned at pairing; for those two, **Pair again** in Settings (with the server password) gets the client going and keeps its queue. The main menu footer keeps the original's three lines.

What happens with the connection:

- **Reads** come from the server while it answers and nothing saved on this client is still waiting to reach it, and are copied into the client's own database on the way. Otherwise the pages read that copy, so the forms, reports and search keep working, and a sheet saved while the server was away shows what was saved until the server has it too. On pairing and every time the connection comes back, the client pulls the server's whole data set into its copy (0.25 s at 9,000 tickets) so a client that goes offline later has everything; rows the client saves while that download is on its way keep what was saved.
- **Saves** go to the server first, with a five-second limit, one at a time: a save waits until the client's previous save has been answered or queued, so saves from two tabs, or a sheet saved while the last one is still on its way over a weak link, reach the server in the order they were numbered. When the server does not answer (or answers 5xx), or this client still has saves waiting for it, the rows are stored on the client and queued in an outbox behind the ones already there, so the server takes a client's saves in the order they were made; the page gets its normal answer plus an `X-TAM-Queued: 1` header. A background worker pings the server every five seconds, replays the outbox in order as soon as it answers, and then pulls the data set again. A save the server rejects as bad data (a 4xx) is not queued: the error goes back to the page. A save the server refuses because the key is wrong stays queued, the bar says so, and pairing again with the same server (at its old address, or by its name at a new one) sends it. Pairing with another server, or unpairing, sets the saves still queued aside in the failed list rather than sending them anywhere by themselves.
- **Two computers, one record**: an older record never overwrites a newer one. Each page sends, with every row it saves, the values the volunteer started from, and the server changes a field only while it still holds that value. A change made on an older copy (a computer that was offline, a page left open, a retry) is not written over what another computer saved since: the newer value stays, the page shows it and lists what was not saved ("Ticket 5 phone number: now 555-0888, yours was 555-0777"), and typing it again replaces it deliberately. A change that waited in the queue and met a newer value goes to the failed list with both values. Fields the volunteer left alone are not written at all, so a phone number fixed on one computer and a name fixed on another both stay; each form keeps to its own fields (a basket's description on the Baskets form, its winner on the Drawing form). Nothing blocks: every other computer keeps reading and saving.
- **Order**: within one client, its saves apply in the order it made them. When the Wi-Fi drops in the middle of a save, the network may still deliver that request seconds after the client gave up on it and sent it again, and the server does not apply that late copy, so it cannot undo newer saves (see API). A client whose data folder was put back from a copy counts its saves from an older number than the server has seen from it; the server says so, and the client numbers its saves past the server's count and sends them again, so none is lost. The server answers every save with the rows as it stored them; an answer that does not list them is not the server's (a Wi-Fi login page answers anything) and the save stays queued.
- **Refused saves** (the server answered 4xx during a replay), saves set aside when the client paired with another server or was unpaired, and queued saves older than what the server already has from this client (a data folder put back from a copy: they may have reached the server before, or not), are kept in a failed list, counted in the bar and listed in Settings with the reason for each, and can be retried (numbered anew and sent, after the saves already queued, to the server the client is paired with now) or discarded. Nothing queued is ever dropped without a Discard. A queued prefix delete that finds the prefix already gone from the server counts as done, as it does online.

- **Events**: each server names the event it holds, and every change it accepts carries an increasing order number that every copy of the row keeps. A client whose copy belongs to another event (a new event at the same address, or a server set up again) keeps that copy in `before-event-<date>-<time>.json` in its data folder, sets its waiting saves aside in the failed list and takes the server's event: nothing of an earlier event reaches the server unasked.
- **Reports** read from the client's copy while the server is away say so on screen and on paper: saves made on other computers since may be missing. Winners reports show the time they are as of, and their Print button reads them again first.

Backup/Restore downloads the server's data, restores a file into it, and pushes this client's prefixes, tickets or baskets to it. A restore replaces the server's rows with the file's, keeping the file's order numbers and event. A push keeps only what is newer than the server's: rows the server does not have, and rows changed later than its own, so an older copy never undoes a newer change; it waits while the client has saves queued, and a server that holds data of another event refuses it. An empty server takes a pushed copy's event, which rebuilds a lost server from a client. Both actions are direct and report failure instead of queueing.

### Server admin page

`tam-server` serves its own pages under `/admin`, protected by the server password: status, keys (create and delete), backup download and restore, and a password change. The password hash lives in `server.json` in the data directory and wins over `TAM_PWD`; with neither set the server starts in setup mode, logs the address to open, and refuses to hand out keys until a password exists.

The status page shows the address, TLS, data directory, version, uptime and counts, then a **Clients** table with one row per key: the client's name, the program it runs (**Program**), its **State**, when it was last seen, when it last saved anything (**Last update**) and how many saves it still has queued (**Queued**). A client is `connected` when the server heard from it in the last 15 seconds (the client pings every 5), `away for` some time otherwise, and `never` when its key has not been used yet. The page reloads every 5 seconds, so an admin can watch every client come back and its queue drain to 0 before packing up; `GET /admin/status` with `Accept: application/json` answers the same table as JSON (`uptime`, `prefixes`, `tickets`, `baskets`, and `clients` with `name`, `program`, `state`, `last_seen`, `last_update` and `queued`) for a logged-in session and a 401 without one, for scripts.

Two optional headers feed the table. `X-TAM-Client: tam-client/<version>`, which `tam-client` sends with every request, names the program; without it the server shows the first word of the `User-Agent`. `X-TAM-Pending: <n>` on the heartbeat (`GET /api` with the key, every 5 seconds) is the number of saves queued on that client; a client that never sends it shows `–` under Queued. Last seen and last update are also kept in the database (the `auth_key_activity` table, next to `auth_keys`, written at most once a minute) and reported by `GET /api/auth`, so they survive a restart; the live values win while the server runs.

## Deployment

Both programs are single, self-contained executables: copy the one you need to the machine and run it. There is nothing to install and no container runtime is needed. The original's Caddy and portable-Node deployment files are not carried over, and the server's `-tls` flag replaces the reverse proxy; a Dockerfile and a compose file under `deploy/docker` are there for those who ran the original's containers, and on NixOS this repository's flake takes the place of `nixos/tam.nix`.

| Original | Here |
|---|---|
| `dbob16/tam-client` container on port 3000 | `tam-client` (or `tam-client.exe`) on port 3080 |
| `dbob16/tam-server` container plus a Caddy proxy on 8443 | `tam-server -tls` on 8443, or `tam-server` on 8000 |
| Data volume `/data` | the `data` folder next to the program, or `TAM_DATA_DIR` |
| `nixos/tam.nix`, a client running the client container | `services.tam-client` from this flake, and `services.tam-server` for the server |

**Windows.** Download `tam-client-<version>-windows-amd64.exe` on a client, or `tam-server-<version>-windows-amd64.exe` on the machine that hosts the server (`-arm64` for a Snapdragon machine), put it in a folder of its own and double-click it; the zip of the same name holds the same program with this README and the license. The program is not signed, so SmartScreen asks once: More info, then Run anyway. Each shows a TAM icon in the notification area while it runs (right-click it for Open and Shut Down) and keeps its console window; Windows asks once whether to allow the server through the firewall. To start one at logon, put a shortcut to it in the Startup folder (`shell:startup`), with `-open=false` if the browser should not open by itself. The executables carry the TAM icons and version information (right-click, Properties, Details); `go generate ./cmd/...` regenerates the resource files with [go-winres](https://github.com/tc-hib/go-winres) after changing `icon.ico` or `winres/winres.json`.

**Linux.** On Debian, Ubuntu and their relatives install the `.deb` of the program (`sudo apt install ./tam-server_<version>-1_amd64.deb`), on Fedora, RHEL and their relatives the `.rpm` (`sudo dnf install ./tam-server-<version>-1.x86_64.rpm`); the client's package is `tam-client`. Either puts the program in `/usr/bin` with its unit, creates the `tam` user, and enables and starts the service at once: the server on port 8000 for the whole network, asking for its password on the first visit of its admin page, the client on `http://localhost:3080/` for this computer only. Installing a newer package the same way upgrades: a running service restarts with the new program, and a service you turned off stays off. `apt remove` or `dnf remove` stops, disables and removes it, keeping the data in `/var/lib/tam-server` or `/var/lib/tam-client` for a reinstall.

For any other distribution, or without root, extract `tam-server-<version>-linux-amd64.tar.gz` or `tam-client-<version>-linux-amd64.tar.gz` (or `-arm64`) and run the program by hand (`./tam-client` opens the browser; Ctrl+C, SIGTERM or the Shut Down button stops either), or install it as a service: `sudo ./install.sh` in the extracted folder installs the program found next to it (`server`, `client` or `all` as the argument chooses explicitly, for example from a checkout's build folder). It copies the program to `/usr/local/bin`, creates a `tam` system user with the data folders `/var/lib/tam-server` and `/var/lib/tam-client`, puts the icons and the client's application-menu entry under `/usr/local/share`, installs the unit, `tam-server.service` or `tam-client.service` from `deploy/linux`, into `/usr/local/lib/systemd/system`, enables it and restarts it. Running the `install.sh` of a newer archive the same way upgrades: it replaces the program and the unit and restarts the service, and a service you turned off stays off. It writes nothing into `/etc`, with one exception: the units that the 1.0.0-rc1 `install.sh` put into `/etc/systemd/system` go, and a `TAM_PWD` line uncommented in the server's moves to `/etc/default/tam-server`; a unit there with other changes stays until they are moved, and the script says how. The comment at the top of `install.sh` lists the commands that undo the installation. Without systemd, run the programs by hand.

Both ways install the same units. They run as `tam` with `ProtectSystem=strict`, so only the data folder is writable, and with `UMask=0077`, so what the programs keep there (names, phone numbers, access keys) is readable by `tam` only; `journalctl -u tam-server` has the log, and the program's own log file is in the data folder. Local settings go outside the units, where upgrades leave them. The server's password, if it should not be set on the first visit of the admin page at `http://<host>:8000/admin`, is a line `TAM_PWD=<password>` in `/etc/default/tam-server`, readable by root only (a password set on the admin page wins over it):

```
sudo install -m 600 /dev/null /etc/default/tam-server
sudoedit /etc/default/tam-server
sudo systemctl restart tam-server
```

Anything else goes into a drop-in from `sudo systemctl edit tam-server` (or `tam-client`); the comment at the top of each unit (`systemctl cat tam-server`) has examples. The client listens on this computer only, since the web app has no login and its settings hold the key it uses with the server. To let other machines open it, `sudo systemctl edit tam-client` and add

```ini
[Service]
ExecStart=
ExecStart=/usr/bin/tam-client -addr :3080 -open=false -tray=false
```

(`/usr/local/bin/tam-client` when `install.sh` installed it), then `sudo systemctl restart tam-client`.

The application-menu entry, Ticket Auction Manager, opens the web app of the client running on the computer, the service or one started by hand, and otherwise starts `tam-client` for the user who clicked it, with its data in `~/.local/share/tam-client`, apart from the service's. On a computer used by one person that is simpler than the service: `sudo systemctl disable --now tam-client` turns the service off for good, and the menu entry then runs the client by hand; it opens the browser itself and stops from Alt+A, Shut Down TAM.

**NixOS.** The repository is a flake. `nix build` builds both programs from source into `result/bin` (the web app with pnpm, then Go, running the unit tests on the way); `nix run github:ticket-auction-manager/tam-go` starts `tam-client`, `nix run github:ticket-auction-manager/tam-go#tam-server` the server, and `nix develop` gives Go, Node and pnpm. The version the programs report is the commit they were built from. Its NixOS module runs either program as a service under its own unprivileged user, with its data in `/var/lib/tam-server` or `/var/lib/tam-client` and its log in the journal (`journalctl -u tam-client`). In a flake-based configuration, a client computer:

```nix
{
  inputs.tam-go.url = "github:ticket-auction-manager/tam-go";

  outputs = { nixpkgs, tam-go, ... }: {
    nixosConfigurations.client1 = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        ./configuration.nix
        tam-go.nixosModules.default
        {
          services.tam-client.enable = true;              # the web app on http://localhost:3080/
          services.tam-client.openBrowserAtLogin = true;  # opened at login: with automatic login, a kiosk
          services.tam-client.openFirewall = true;        # UDP 5353, to find the server by its announcement
        }
      ];
    };
  };
}
```

And the machine that holds the event's data:

```nix
services.tam-server = {
  enable = true;
  openFirewall = true;                           # TCP 8000 (8443 with tls), and UDP 5353 for the announcement
  # tls = true;                                  # HTTPS with a self-signed certificate, or certFile and keyFile
  # passwordFile = "/run/secrets/tam-password";  # otherwise the password is set on the first visit of /admin
};
```

A configuration without flakes can import the module from a pinned commit, with flakes enabled in `nix.settings.experimental-features`:

```nix
imports = [ (builtins.getFlake "github:ticket-auction-manager/tam-go/<commit>").nixosModules.default ];
```

Compared with `nixos/tam.nix`, the client runs `tam-client` natively instead of the Docker image, finds and pairs with the server from its Settings page instead of a `tam.lan` hosts entry, and keeps the automatic login in its own configuration (`services.displayManager.autoLogin`). Shut Down TAM in the web app stops the service until the next boot or `systemctl start tam-client`. The programs in the Linux archives are static, so they also run by hand on NixOS; `install.sh` stops there, since NixOS keeps `/etc` and the units in its configuration. The flake pins its nixpkgs, because the build needs Go 1.27, which NixOS 26.05 does not have; a machine on a stable release runs the same build. When `go.sum` or `frontend/pnpm-lock.yaml` changes, `nix/package.nix` needs the new `vendorHash` or pnpm `hash`: set it to `lib.fakeHash`, run `nix build`, and copy the hash Nix reports. CI's Nix job fails until then.

**macOS.** The programs need macOS 13 or later. Extract `tam-server-<version>-darwin-arm64.tar.gz` or `tam-client-<version>-darwin-arm64.tar.gz` (Apple silicon; `-darwin-amd64` for Intel). The programs are not signed, so clear the quarantine flag once (`xattr -dr com.apple.quarantine tam-client` or `tam-server`) and make sure the program is executable (`chmod +x`), then run it by hand, or start it at login with its launchd agent, `com.ticket-auction-manager.tam-server.plist` or `com.ticket-auction-manager.tam-client.plist` from `deploy/macos`, next to it in the archive. `INSTALL.md` in the archive has the commands: `launchctl bootstrap` to start it, and to take it out of the login items `launchctl bootout` and removing the plist from `~/Library/LaunchAgents`, since the next login loads every plist there again. The agents keep the data under `~/Library/Application Support/tam-server` and `~/Library/Application Support/tam-client` and restart a program after a crash; the client's listens on this computer only. There is no notification-area icon on macOS.

**Docker.** `deploy/docker/Dockerfile` builds either program from source (`--build-arg PROGRAM=tam-client` for the client) into a small Alpine image with the data in `/data`. Inside the container both listen on every address, the server on port 8000 and the client on 3080 (its image starts it with `-addr :3080 -open=false -tray=false`); the published port decides who reaches it, for example `docker run -p 127.0.0.1:3080:3080 -v ./client-data:/data tam-client` for this computer only. Flags given to `docker run` replace those defaults. `deploy/docker/compose.yml` runs the server on port 8000 with `./data` mounted, like the original's compose file: `cd deploy/docker && TAM_PWD=secret docker compose up -d --build`. The client is under the `client` profile (`docker compose --profile client up -d --build`: port 3080, `./client-data`); pair it with host `tam-server` and port 8000 inside the compose network. A Docker whose buildx plugin is older than 0.17 (Unraid ships one) makes `docker compose` refuse to build; there, build the image from the repository root with `docker build -f deploy/docker/Dockerfile -t tam-server .` and start it with `docker compose up -d` without `--build`. A Docker without the plugin at all builds either way. Announcements on the local network do not leave a bridged container, so the compose file starts the server with `-announce=false` and the clients type the address; `network_mode: host` brings the announcement back.

## Switching from the original

The Go programs open the original's data as it is: `tam-remote.db` for the server, `tam-local.db` and `settings.json` for the client. Point `TAM_DATA_DIR` at the old data folder (or the folder behind the original's Docker volume, the `/data` of its containers), or copy those files into the `data` folder next to the program, and start it; what the Go programs add to the database is applied on the first start, and existing access keys keep working. A backup file from the original restores too. The switch goes one way: the original programs do not talk to these, so move the server and every client of an event together.

With the original's compose file the server was `dbob16/tam-server` on port 8000 with a `/data` volume, and the client `dbob16/tam-client` on port 3000, with a Caddy proxy on 8443 for HTTPS. `deploy/docker/compose.yml` keeps the server on `8000:8000` with `./data:/data`: move the contents of the old volume into `./data`, or name the old volume in its place, and `docker compose up -d`. The client moves from port 3000 to 3080 (`3080:3080`). The Caddy proxy is gone: `tam-server -tls` serves HTTPS itself on 8443, with a self-signed certificate created in the data folder on the first start or with your own PEM files through `-cert` and `-key`; it is the same flag on Windows, Linux and macOS, with no container involved.

## API

Both daemons answer JSON with the original's field names and `{"detail": "..."}` on errors. The server requires a `TAM-KEY` header on every data route and a `TAM-PW` header on key management.

| Route | Server | Client |
|---|---|---|
| `GET /api` | who am I, authenticated, healthy, name, version, and the event it holds | who am I; in remote mode the server's answer |
| `GET/POST /api/settings` | | read, or merge a full or partial object |
| `GET/POST/DELETE /api/auth` | list, create `{description}`, delete `?key_to_del=` (password) | proxied with the page's `TAM-PWD` header |
| `GET/POST/DELETE /api/prefixes` | list, upsert, delete `?p=` | same; DELETE answers 404 when nothing matched |
| `GET /api/tickets[/{prefix}[/{id}|/{from}/{to}]]`, `POST /api/tickets` | rows; single and range answer lists | single answers one object or a placeholder; range answers every id with placeholders, capped at 300 |
| `GET /api/baskets…`, `POST /api/baskets` | same shape | same shape |
| `GET /api/drawing…`, `POST /api/drawing` | baskets joined with winners; POST sets winning tickets | same |
| `GET /api/reports/byname/{prefix}`, `/bybasket/{prefix}`, `/counts` | report rows | same |
| `GET /api/search/tickets?first_name&last_name&phone_number`, `POST` | substring search, upsert | same |
| `GET/POST /api/backuprestore` | export, import (restore); with `X-TAM-Merge: newer` the rows newer than the server's | `/local`, `/remote`, and `POST /push/{prefixes\|tickets\|baskets}` |
| `GET /api/status` | | `{"mode":"standalone"}` or mode, state (`connected`, `reconnecting`, `offline`, `unauthenticated`), server, server_name, pending, failed, last_ok |
| `GET /api/servers` | | servers announcing themselves on the network: name, host, port, tls, version |
| `POST /api/pair`, `POST /api/unpair` | | `{host, port, tls, password}` pairs and stores the key; unpair takes an optional `{password}` to delete the key on the server |
| `GET /api/outbox/failed`, `POST /api/outbox/retry`, `POST /api/outbox/discard` | | the failed list with the reason for each save, back into the queue, or dropped |
| `/admin/...` | login, status, keys, backup, password (HTML) | |

Writes to the client require `Content-Type: application/json`, and a browser request from another site (`Sec-Fetch-Site: cross-site`) is refused, which replaces the original's per-process client id header.

`tam-client` names and numbers the saves it sends to the server: `X-TAM-Client-Name` is the client's name (made once and kept with its data; a data folder copied to another machine makes a new one) and `X-TAM-Save` a number that only grows. A client sends its numbered saves one at a time, in the order of their numbers. `tam-server` applies a numbered save only when it is newer than the last one it applied from that client, so a request the network delivers late cannot undo newer saves. The last save arriving again (the same number and content) is answered `200` with `X-TAM-Stale: 1` and not applied twice; any other save at or below the last number is answered `409` with `X-TAM-Last-Save` and `last_save` in the body, and the client numbers it past that and sends it again, or, when it was a queued save, keeps it in the failed list. A save without the headers is refused (400).

A save's rows may carry `base`, the values the volunteer started from: `{"prefix":"A","t_id":5,"phone_number":"555-0777",...,"base":{"first_name":"Ann","last_name":"Lee","phone_number":"555-0001","pref":"CALL"}}`. The server changes a field only while it holds the base value, and answers with the rows as stored, `rev` (and a basket's `win_rev`) included: the order number of the row's last change. A row without `base` replaces the fields as they come. Ids, weights and winning tickets are JSON integers (`null` reads as 0). In remote mode an answer the client read from its own copy carries `X-TAM-Copy: 1`.

## Differences from the original

- Every handler validates before writing and returns after an error; a rejected batch writes nothing.
- Settings saves merge onto the current file and are validated; a malformed file no longer breaks every request.
- Prefix deletion encodes the name (`A&B`, `50%`, `C+` can be deleted) and reports 404 when nothing matched.
- Restore overwrites existing rows on both daemons (the original overwrote locally but skipped existing tickets, and never updated winning tickets on the server).
- The remote backup download sends the correct `TAM-KEY` header, the single-basket lookup calls the baskets endpoint, and the by-basket report is titled by basket.
- Access keys are generated with a cryptographic random source; the server never accepts an empty key.
- The number input for prefix weight only accepts non-negative integers; prefix names are trimmed, at most 100 characters, may not contain `/` or `\` and may not be `.` or `..` (they appear in URLs).
- Ticket search treats `%` and `_` typed by the user as literal characters instead of SQL wildcards.
- A backup written by the original app restores even if a prefix carries a colour outside the palette (it is shown as white) or a name the prefix form would refuse today (`A/B`, a name with spaces around it): restores and a client copying its server's data take the rows as they are, so no prefix is cut off from its tickets. The contact preference stays free text as in the original.
- The Settings page refuses a remote server entered with a scheme or a path (`http://tam.lan`, `tam.lan/api`): enter the host name or address only.
- Every link is a full page load (`data-sveltekit-reload`, as in the original), so each prefix starts with a clean form. Marked rows are saved when you leave a form or the Search page, close the tab, or switch away from it: a browser may discard a tab left in the background, and a phone or tablet may stop one, without running the page's unload code. The save outlives the page (`keepalive`). The original saved the forms only as a page was left, with a request the closing page could cancel, and asked before leaving the Search page.
- The client listens on port 3080 instead of the original's 3000.
- Both programs run as plain desktop programs rather than containers: on Windows they show a TAM icon in the notification area with Shut Down (and, for the client, Open) entries, the client opens the browser on start and has a **Shut Down TAM** button under `Alt+A`, and Ctrl+C or a closed console window stops either one cleanly.
- Request bodies are capped at 64 MiB (the original ran Node with no limit); that is far above any realistic backup file. A larger body answers 413.
- Validation errors answer 400 where FastAPI answered 422. Unknown paths and wrong methods under `/api` answer `{"detail": ...}` as the original did. `HEAD` is accepted on every GET route.
- `DELETE /api/prefixes` and `DELETE /api/auth` echo the deleted row; a missing row is 404. In remote mode a prefix the server no longer has is still removed from the local mirror.
- Success messages use the `message` key everywhere (the original used `details` for a local restore and an empty list for a remote one). A reversed range (`/5/1`) is swapped instead of answered empty.
- Ids are JSON integers; a ticket or basket without an id is rejected instead of being stored as id 0.
- Both daemons require `Content-Type: application/json` on POST (the original client always sent it; the original server did not check). The push buttons send an empty JSON object for the same reason.
- 500 and 502 responses carry a generic message; the reason is in the daemon's log.
- `tam-server dev` binds localhost only when `-addr` is not given.
- The counts report counts a buyer as first name, last name and phone number together (the original ran them into one text, so "Jo Ann" and "Joa Nn" were one buyer), and labels its last row `Total`, marked as the total so a prefix named Total stays a row of its own (the original server said `Totals`); the report views are recreated on every start so an older database picks that up.
- A ticket numbered 0 is not the winner of the baskets not drawn yet: the report views join a winner only when a basket has a winning ticket (the original's views take the 0 of an undrawn basket as ticket 0).
- The server password, whether for the admin page or for creating keys (`TAM-PW`), takes five wrong guesses per address and then waits 30 seconds; the original checks every guess.
- When two computers change the same record, the original kept whichever save arrived last, an older one included; here an older record never overwrites a newer one (see [Remote mode](#remote-mode)).

## Layout

```
cmd/tam-client, cmd/tam-server   entry points
internal/env                     data directory from TAM_DATA_DIR, log file
internal/db                      SQLite open + schema
internal/store                   every query, typed models
internal/config                  settings.json
internal/httpx                   JSON helpers, request guards
internal/remote                  HTTP client for tam-server
internal/server                  tam-server API
internal/client                  tam-client API and web app serving
internal/desktop                 browser opening, console title, Ctrl+C handling, Windows notification-area icon
internal/sync                    connection state, heartbeat, outbox replay, mirror pull (remote mode)
internal/tlscert                 the self-signed certificate of tam-server -tls
internal/discovery               mDNS announce (server), browse and subnet sweep (client)
internal/presence                what the server last saw of each client, for the admin page
internal/admin                   the server's login-protected admin pages and password file
internal/guard                   the limit on password guesses, shared by the admin login and the API
internal/version                 the version both programs report, stamped at build time
scripts/loadtest                 the load test: a whole event through one server and many clients
scripts/browser                  browser tests of the web app against real programs
scripts/shutdown                 a stopping program finishes the saves under way
flake.nix, nix/                  the Nix package, the NixOS module for both services and its NixOS test
deploy/linux, deploy/macos, deploy/docker   systemd units, installer, menu entry, .deb/.rpm definitions (nfpm/) and their test, launchd agents, Dockerfile and compose
frontend/                        SvelteKit single-page app (built into cmd/tam-client/dist)
```

## License

MIT, see [LICENSE.md](LICENSE.md).
