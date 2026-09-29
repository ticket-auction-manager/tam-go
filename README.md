# Ticket Auction Manager (Go)

Ticket Auction Manager (TAM) runs in-person penny socials and benefit auctions: sell numbered tickets by prefix, describe the baskets, enter the drawn winning tickets, and print the winners and counts reports. This is the Go rewrite of [ticket-auction-manager/tam](https://github.com/ticket-auction-manager/tam): the same features and the same wire format, in two single-file binaries with no Python or Node at runtime.

- **tam-client** serves the web app and its API on `http://localhost:3080`. In standalone mode it keeps everything in a local SQLite file. In remote mode it sends every change to a **tam-server** and keeps a local mirror.
- **tam-server** is the shared database for large events with several clients. It speaks the same API, protected by access keys.

## Features

- **Forms** per prefix: Tickets (names, phone numbers, contact preference), Baskets (descriptions and donors), Drawing (winning ticket numbers with instant winner lookup). Range-based paging, keyboard shortcuts, copy/paste and duplicate rows, save-marked-rows.
- **Reports**: Winners by Name, Winners by Basket (both printable, filterable by CALL or TEXT preference), Ticket Counts (unique buyers and total buys per prefix, auto-refresh).
- **Print Sheets** for ticket sales, **Ticket Search** across every prefix.
- **Settings**: remote server, port and TLS, default contact preference, venue name, attribution toggle; **Prefixes** with colours and ordering; **Auth Keys** for the server; **Backup/Restore** with local and remote downloads, uploads, and push-to-server.
- Admin section on the main menu with `Alt+A`.

## Quick start

1. Run `tam-client` (double-click, or `./tam-client` in a terminal). It creates a `data/` folder in the directory it is started from, which is its own folder when double-clicked; set `TAM_DATA_DIR` to keep the data elsewhere. Prebuilt binaries from a release zip are under `build/<os>-<arch>/`; on Linux or macOS run `chmod +x` on them if your unzip tool dropped the executable bit.
2. It opens http://localhost:3080/ in your default browser as soon as it is listening (start it with `-open=false` to skip that, for example from a script). On Windows a TAM icon sits in the notification area next to the clock while it runs (under the `^` overflow unless you pin it): left-click it to open the app again, right-click it for **Open TAM** and **Shut Down TAM**. The console window it started with stays open too, titled "Ticket Auction Manager - client" in the taskbar; closing it or pressing Ctrl+C in it also stops the program cleanly, as does **Shut Down TAM** under `Alt+A` on the main menu. Closing the browser tab alone leaves it running.
3. Press `Alt+A`, open Settings, then Prefixes, and add at least one prefix. Prefixes are the ticket series (for example `A`, `B`, `C`) and unlock the forms and reports on the main menu.

## Building from source

Requirements: Go 1.27.1 or newer (the version in `go.mod`), Node 24 or newer with pnpm (the scripts fall back to `npx pnpm` when pnpm is not installed).

```bash
./build.sh all          # web app + tam-client + tam-server into ./build
./build.sh client       # or one of them
GOOS=linux GOARCH=amd64 ./build.sh all     # cross-compile (CGO is not needed)
```

The web app must be built before `go build ./cmd/tam-client`, because the binary embeds `cmd/tam-client/dist`. `build.sh client` does both.

Development:

```bash
./run.sh server         # tam-server on localhost:8000; set its password on http://localhost:8000/admin
./run.sh client         # installs and builds the web app, then tam-client on localhost:3080
go test ./...           # store, server and client tests, including remote mode
```

For work on the pages, `pnpm dev` in `frontend/` serves them on http://localhost:5173/web/ and proxies `/api` to a running `tam-client`.

## Running the tests

The tests need Go and the built web app (`pnpm build` in `frontend/`, as for any build). CI runs all of them on every push and keeps what they print: each run's page on GitHub (Actions) shows the unit tests, the compatibility run, the tests with the race detector, the load test's report and the NixOS test, and has them as files to download under Artifacts. [docs/test-results.md](docs/test-results.md) has the printed results of the longer runs, made on a real machine.

Unit and integration tests; the client tests drive the real server handler as their server:

```
go test ./...
```

The compatibility run against the original tam, its FastAPI server at a pinned commit and its SvelteKit client next to the Go programs (needs Python 3, Node with pnpm, git and curl):

```
bash scripts/compat/run.sh
```

The load test runs a whole event through one real `tam-server` and many real `tam-client` programs on the machine, each client with its own data folder and paired through its Settings route. Ticket entry is paced over 40 seconds, fixing a typo now and then and opening sheets again; a quarter of the way in the server is killed and started again 8 seconds later while the clients keep saving, and each client goes back to correct the sheets it saved meanwhile as soon as it sees the server again. Then another client corrects every 40th ticket, the baskets are entered and drawn (each winner looked up as the page does), every report and a few searches are read, and for 10 seconds every client saves as fast as it can. It then checks every ticket, basket and winner on the server and in each client's own copy, and everything the programs wrote, and prints the time of every page action:

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

A data folder from the original app is a drop-in: the tables and views are the same, and the Go daemons open it as is.

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
- **Conflicts** are settled by arrival at the server: the last save wins, as in the original. A client replaying an old edit after another client changed the same ticket wins with the older edit. Within one client, its saves apply in the order it made them: when the Wi-Fi drops in the middle of a save, the network may still deliver that request seconds after the client gave up on it and sent it again, and the server does not apply that late copy, so it cannot undo newer saves (see API). A client whose data folder was put back from a copy counts its saves from an older number than the server has seen from it; the server says so, and the client numbers its saves past the server's count and sends them again, so none is lost.
- **Refused saves** (the server answered 4xx during a replay), saves set aside when the client paired with another server or was unpaired, and queued saves older than what the server already has from this client (a data folder put back from a copy: they may have reached the server before, or not), are kept in a failed list, counted in the bar, and can be retried (numbered anew and sent, after the saves already queued, to the server the client is paired with now) or discarded from Settings. Nothing queued is ever dropped without a Discard. A queued prefix delete that finds the prefix already gone from the server counts as done, as it does online.

Backup/Restore can still push the local prefixes, tickets or baskets to the server and download the server's data; those two actions are direct and report failure instead of queueing.

### Server admin page

`tam-server` serves its own pages under `/admin`, protected by the server password: status (address, TLS, data directory, counts, and the paired clients with the time each was last seen), keys (create and delete), backup download and restore, and a password change. The password hash lives in `server.json` in the data directory and wins over `TAM_PWD`; with neither set the server starts in setup mode, logs the address to open, and refuses to hand out keys until a password exists.

### Compatibility with the original

The API is the original's, so the original `tam-client` (Linux/Docker) and the Go client can share one server, and either server works. `scripts/compat/run.sh` proves it: it starts the original FastAPI server at a pinned commit and runs the Go client's `TestCompat*` tests against it, then starts the Go server with both the original SvelteKit client and the Go client and drives every route through each (`scripts/compat/drive.py`). CI runs it on every push. Two quirks of the original as published are handled on this side, so a mixed setup comes out right either way: its server leaves winning tickets alone on a restore, so `tam-client` sends them a second time through the drawing route after every restore or push into a server; and its client sends the key as `TAM_KEY` on its server-backup download, so `tam-server` accepts that spelling too. Fixes for the original itself, including its local restore skipping tickets that already exist, are submitted as [ticket-auction-manager/tam#1](https://github.com/ticket-auction-manager/tam/pull/1). The checks here are strict.

## Deployment

Both programs are single, self-contained executables: copy the one you need to the machine and run it. There is nothing to install and no container runtime involved. The original's Docker, Caddy and portable-Node deployment files are therefore not carried over, the server's `-tls` flag replaces the reverse proxy, and on NixOS this repository's flake replaces `nixos/tam.nix` (see [NixOS](#nixos)).

| Original | Here |
|---|---|
| `dbob16/tam-client` container on port 3000 | `tam-client` (or `tam-client.exe`) on port 3080 |
| `dbob16/tam-server` container plus a Caddy proxy on 8443 | `tam-server -tls` on 8443, or `tam-server` on 8000 |
| Data volume `/data` | the `data` folder next to the program, or `TAM_DATA_DIR` |
| `nixos/tam.nix`, a client running the client container | `services.tam-client` from this flake, and `services.tam-server` for the server |

On Windows the executables carry the TAM icons and version information; `go generate ./cmd/...` regenerates the resource files with [go-winres](https://github.com/tc-hib/go-winres) after changing `icon.ico`.

### NixOS

The repository is a flake. `nix build` builds both programs from source into `result/bin` (the web app with pnpm, then Go, running the unit tests on the way); `nix run github:ticket-auction-manager/tam-go` starts `tam-client`, `nix run github:ticket-auction-manager/tam-go#tam-server` the server, and `nix develop` gives Go, Node and pnpm. Its NixOS module runs either program as a service under its own unprivileged user, with its data in `/var/lib/tam-server` or `/var/lib/tam-client` and its log in the journal (`journalctl -u tam-client`). In a flake-based configuration, a client computer:

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

Compared with `nixos/tam.nix`, the client runs `tam-client` natively instead of the Docker image, finds and pairs with the server from its Settings page instead of a `tam.lan` hosts entry, and keeps the automatic login in its own configuration (`services.displayManager.autoLogin`). Shut Down TAM in the web app stops the service until the next boot or `systemctl start tam-client`. The flake pins its nixpkgs, because the build needs Go 1.27, which NixOS 26.05 does not have; a machine on a stable release runs the same build. When `go.sum` or `frontend/pnpm-lock.yaml` changes, `nix/package.nix` needs the new `vendorHash` or pnpm `hash`: set it to `lib.fakeHash`, run `nix build`, and copy the hash Nix reports. CI's Nix job fails until then.

## API

Both daemons answer JSON with the original's field names and `{"detail": "..."}` on errors. The server requires a `TAM-KEY` header on every data route and a `TAM-PW` header on key management.

| Route | Server | Client |
|---|---|---|
| `GET /api` | who am I, authenticated, healthy | who am I; in remote mode the server's answer |
| `GET/POST /api/settings` | | read, or merge a full or partial object |
| `GET/POST/DELETE /api/auth` | list, create `{description}`, delete `?key_to_del=` (password) | proxied with the page's `TAM-PWD` header |
| `GET/POST/DELETE /api/prefixes` | list, upsert, delete `?p=` | same; DELETE answers 404 when nothing matched |
| `GET /api/tickets[/{prefix}[/{id}|/{from}/{to}]]`, `POST /api/tickets` | rows; single and range answer lists | single answers one object or a placeholder; range answers every id with placeholders, capped at 300 |
| `GET /api/baskets…`, `POST /api/baskets` | same shape | same shape |
| `GET /api/drawing…`, `POST /api/drawing` | baskets joined with winners; POST sets winning tickets | same |
| `GET /api/reports/byname/{prefix}`, `/bybasket/{prefix}`, `/counts` | report rows | same |
| `GET /api/search/tickets?first_name&last_name&phone_number`, `POST` | substring search, upsert | same |
| `GET/POST /api/backuprestore` | export, import | `/local`, `/remote`, and `POST /push/{prefixes\|tickets\|baskets}` |
| `GET /api/status` | | `{"mode":"standalone"}` or mode, state (`connected`, `reconnecting`, `offline`, `unauthenticated`), server, server_name, pending, failed, last_ok |
| `GET /api/servers` | | servers announcing themselves on the network: name, host, port, tls, version |
| `POST /api/pair`, `POST /api/unpair` | | `{host, port, tls, password}` pairs and stores the key; unpair takes an optional `{password}` to delete the key on the server |
| `POST /api/outbox/retry`, `POST /api/outbox/discard` | | the failed list back into the queue, or dropped |
| `/admin/...` | login, status, keys, backup, password (HTML) | |

Writes to the client require `Content-Type: application/json`, and a browser request from another site (`Sec-Fetch-Site: cross-site`) is refused, which replaces the original's per-process client id header.

`tam-client` names and numbers the saves it sends to the server: `X-TAM-Client-Name` is the client's name (made once and kept with its data; a data folder copied to another machine makes a new one) and `X-TAM-Save` a number that only grows. A client sends its numbered saves one at a time, in the order of their numbers. `tam-server` applies a numbered save only when it is newer than the last one it applied from that client, so a request the network delivers late cannot undo newer saves. The last save arriving again (the same number and content) is answered `200` with `X-TAM-Stale: 1` and not applied twice; any other save at or below the last number is answered `409` with `X-TAM-Last-Save` and `last_save` in the body, and the client numbers it past that and sends it again, or, when it was a queued save, keeps it in the failed list. Saves without the headers, as the original client sends them, apply as they come, and the original server ignores the headers.

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
- Ids accept every spelling the original accepted (`4`, `4.0`, `"4"`); a ticket or basket without an id is rejected instead of being stored as id 0.
- Both daemons require `Content-Type: application/json` on POST (the original client always sent it; the original server did not check). The push buttons send an empty JSON object for the same reason.
- 500 and 502 responses carry a generic message; the reason is in the daemon's log.
- `tam-server dev` binds localhost only when `-addr` is not given.
- The counts report labels its last row `Total` on both daemons (the original server said `Totals`); the report views are recreated on every start so an older database picks that up.
- A ticket numbered 0 is not the winner of the baskets not drawn yet: the report views join a winner only when a basket has a winning ticket (the original's views take the 0 of an undrawn basket as ticket 0).
- The server password, whether for the admin page or for creating keys (`TAM-PW`), takes five wrong guesses per address and then waits 30 seconds; the original checks every guess.

## Layout

```
cmd/tam-client, cmd/tam-server   entry points
internal/env                     data directory from TAM_DATA_DIR, log file
internal/db                      SQLite open + schema (tables and views of the original)
internal/store                   every query, typed models
internal/config                  settings.json
internal/httpx                   JSON helpers, request guards
internal/remote                  HTTP client for tam-server
internal/server                  tam-server API
internal/client                  tam-client API and web app serving
internal/desktop                 browser opening, console title, Ctrl+C handling, Windows notification-area icon
internal/sync                    connection state, heartbeat, outbox replay, mirror pull (remote mode)
internal/tlscert                 the self-signed certificate of tam-server -tls
internal/discovery               mDNS announce (server) and browse (client)
internal/admin                   the server's login-protected admin pages and password file
internal/guard                   the limit on password guesses, shared by the admin login and the API
scripts/compat                   the compatibility run against the original tam
scripts/loadtest                 the load test: a whole event through one server and many clients
flake.nix, nix/                  the Nix package, the NixOS module for both services and its NixOS test
frontend/                        SvelteKit single-page app (built into cmd/tam-client/dist)
```

## License

MIT, see [LICENSE.md](LICENSE.md).

## Verified

On 2026-09-25, on Windows 11 with Go 1.27.1 and pnpm 12:

- `go vet`, `gofmt -l` and `go test ./...` are clean. The client tests drive the real server handler as the remote, so the proxy contract is tested end to end.
- Standalone, through the browser: adding prefixes, selecting a prefix on the main menu, loading a ticket range and saving names, saving basket descriptions, entering a winning ticket with the buyer looked up live, winners by name, winners by basket, ticket counts, ticket search, print sheets, saving settings.
- Remote mode, through the browser: pointing the client at `tam-server dev` with `TAM_PWD=testpw`; the main menu shows Remote, Authenticated and Healthy; a wrong password on Auth Keys is rejected; creating a key and pressing Use; pushing prefixes, tickets and baskets; entering tickets against the server (confirmed on the server with curl and the key); remote backup download.
- Remote mode over HTTPS: `tam-server -tls dev` creating its certificate on first start, and the client with Remote TLS on and port 8443 reporting Authenticated and Healthy.

A second, adversarial pass then reviewed the package against the original with the original FastAPI server and the original SvelteKit client running as oracles, sending identical request suites to both implementations, and mixed the daemons (Go client against the original server, original client against the Go server). It found and led to fixes for: full page loads on navigation (the original's `data-sveltekit-reload`, without which pending rows were lost when leaving a form), a push that the original server rejected because unused lists were sent as `null`, a new connection pool per proxied request, a settings save that could be read half-written, numeric ids sent as strings by the original client, executable bits lost in the zip, and the smaller wire and documentation differences listed above. Attacks that held up: SQL injection through every parameter, path traversal on the web app, cross-site writes, oversized and malformed bodies, hundreds of concurrent and same-row writes, key deletion and server outage in remote mode.

Remote mode v2 (2026-09-26, Windows 11): `scripts/compat/run.sh` passed locally, the Go client against the original FastAPI server (commit `19eab77`) and the original SvelteKit client together with the Go client against `tam-server`, 25 cross-checks in `drive.py`. Live, with the built executables: the server's first start in setup mode and its password set from the admin page; the client finding the server by name on the network, pairing with the password, and the bar reading Connected; the server closed from its window, the bar turning Reconnecting within five seconds and Offline after thirty, a ticket saved meanwhile answered with `X-TAM-Queued` and shown on the tickets page from the client's copy; the server started again, the bar back to Connected within five seconds and the queued ticket present on the server. Size checked at 9,000 tickets and 400 baskets: every call about 0.2 s, the localhost floor on Windows.

Load test (2026-09-27, `scripts/loadtest`): its first runs found three problems, each now fixed and pinned by a test. A client that saw its server again while saves were still queued sent new saves straight to the server, so an older queued save of the same ticket could land after a newer one and win, and a sheet opened meanwhile showed the server's older rows; now a client works from its own copy and queues new saves behind the old ones until its queue is empty. The refresh after a reconnect could copy the server's older rows over saves made while it downloaded; now it leaves those rows alone, and waits until the queue has been sent. On a Linux server whose disk is slow to flush, 50 clients pairing at once made SQLite give up on a write (`database is locked`), because its busy wait is not first come, first served; now the writes of each program take turns.

Harder tests (2026-09-27): a review of the pairing code, the relay that drops a client's Wi-Fi silently and delivers late, crashes of clients with saves queued, and fuzz tests of every route found more, each fixed with a test that failed first. Pairing again, as the bar asks when the server refuses a client's key, dropped every save queued meanwhile; now the queue is sent, or set aside in the failed list when the client pairs with another server. A save queued offline was written to the client and to its queue in two transactions, so a client stopping in between kept a save it would never send; now both happen in one. A page reading from a server that went silent waited ten seconds; now four. A save the client gave up on while its Wi-Fi was gone could still reach the server seconds later, after its replay and after newer saves, and undo them; now the client numbers its saves and the server skips a late copy (see API). A range of ids ending at the largest number made the client allocate until it ran out of memory; search missed text after a NUL and failed on very long fragments; the prefix names `.` and `..` were accepted but unreachable; and one prefix name from the original that today's form refuses (`A/B`) made a restore, and every client's copy of its server's data, fail. With the fixes every run on this branch passes all its checks (24; the two that read the admin page's status as JSON are skipped, since this server's status page is HTML only), on Windows 11 (Ryzen 9 7900X3D, NVMe): 50 clients with 9,000 tickets and 1,000 baskets, and 20 clients over HTTPS with the server down for 45 s (all 209 saves queued meanwhile sent 5.2 s after it was back). No request failed and the programs logged no error. Throughput, with every save changing every row of its sheet (a save of unchanged rows writes nothing in SQLite): 1,794 saves (44,858 rows) a second from 50 clients, median 28 ms, the server peaking at 103 MB. The same sync and store code in the fork's `all-systems` build also passed with 100 clients on the Windows machine using a server on an Ubuntu 24.04 machine over the LAN, where each commit waits about 12 ms for its SATA SSD and the server took 68 saves (1,700 rows) a second, and in a 45-minute soak with 25 clients and 9,000 tickets that kept the server at 46 MB and about 470 open handles from the first rounds to the last. A real event saves a sheet every half minute or so per client, so even the slow disk leaves a large margin. CI runs the unit tests and a 12-client load test with the race detector on every push.

NixOS (2026-09-27): in a `nixos/nix` container with the build sandbox on and KVM, `nix build` built the web app and both programs with nixpkgs' Go 1.27.1, Node 24 and pnpm 12 and passed every unit test inside the sandbox, and `nix flake check` passed, including the NixOS test with three machines. The package and module also evaluate for aarch64-linux and aarch64-darwin; those builds were not run.
