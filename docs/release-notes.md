Ticket Auction Manager is the ticket, basket and drawing bookkeeping for an in-person benefit auction. This is the Go version: one self-contained program per machine, no runtime to install and no container needed. Each client at the event runs **tam-client**, the web app it opens in the browser. One machine runs **tam-server**, the shared database the clients pair with; a client keeps working when the server is out of reach and catches up when it is back, a change made on an older copy never overwrites what another client saved since, and the server's admin page lists every client, whether it is connected, and when it last saved. The full manual is the [README at this version](https://github.com/{REPO}/blob/{TAG}/README.md).

## Which file to download

| Machine | File |
|---|---|
| Windows client computer | `tam-client-{VERSION}-windows-amd64.exe` (the `.zip` of the same name adds the README) |
| Windows machine hosting the server | `tam-server-{VERSION}-windows-amd64.exe` (`-windows-arm64` for a Snapdragon machine) |
| Debian, Ubuntu, Mint and their relatives | the `.deb` of the program, `_amd64.deb` or `_arm64.deb` |
| Fedora, RHEL, Rocky, Alma and their relatives | the `.rpm` of the program, `.x86_64.rpm` or `.aarch64.rpm` |
| Any other Linux | the `-linux-amd64.tar.gz` (or `-arm64`) of the program: it holds the program, an installer script for systemd, and the menu entry for the client |
| macOS | the `-darwin-arm64.tar.gz` (Apple silicon) or `-darwin-amd64.tar.gz` (Intel) of the program, with a launchd file |
| NixOS | nothing: the repository's flake builds both programs, and its NixOS module runs them as services (see the README) |
| Docker | build from source with `deploy/docker` in the repository; see the README |

## First start

1. **The server.** Windows: put the exe in a folder of its own and double-click it. Debian or Ubuntu: `sudo apt install ./tam-server_*.deb`; Fedora or RHEL: `sudo dnf install ./tam-server-*.rpm`; the service starts at once. Tarball: `sudo ./install.sh` in the extracted folder, or just run `./tam-server`. Then open `http://<that machine>:8000/admin` and set the server password; that page is also where you see the clients and take backups.
2. **The clients.** Run tam-client the same way. It opens the browser; the `.deb` and `.rpm` run it as a service on `http://localhost:3080/` instead, which the Ticket Auction Manager entry in the application menu opens. Press `Alt+A`, open Settings, pick the server from the list (it announces itself on the network), enter the password once and press **Pair**. The bar at the top says Connected from then on. A client with no server at all works on its own with its data on the client.

## Upgrading

The data stays where it is. Upgrade the server and the clients of an event together: an earlier client still saves, but only an updated one keeps another client's newer value from being overwritten and sees what was kept out.

- Windows, macOS, or a program run by hand: stop it, replace the file with the new one, and start it again.
- `.deb` or `.rpm`: install the new package the same way as the first one. A running service restarts with the new program, and a service you turned off stays off.
- Tarball installed with `install.sh`: extract the new archive and run `sudo ./install.sh` in its folder again. It replaces the program and the unit and restarts the service; a service you turned off stays off.
- Local settings are kept by all of these as long as they live outside the units: the server password as `TAM_PWD=...` in `/etc/default/tam-server`, anything else in a drop-in from `sudo systemctl edit tam-server` (or `tam-client`).

Upgrading from 1.0.0-rc1 on Linux:

- The rc1 packages turned the service off during every upgrade, and the new ones put that right. The `.rpm` leaves each service enabled or disabled as it was before the upgrade. The `.deb` cannot see that, so it enables and starts the service again (rc1 did so at every install anyway): turn off again a service that should stay off (`sudo systemctl disable --now tam-client`).
- rc1's `install.sh` put the units into `/etc/systemd/system`. The new one installs them into `/usr/local/lib/systemd/system` and removes the old copies; a `TAM_PWD` line uncommented in the old unit moves to `/etc/default/tam-server`. An old unit with other changes stays in effect, and `install.sh` says how to move them.
- The Linux client service now listens on `localhost:3080`, this computer only, like the other systems; the README (Deployment, Linux) shows the drop-in that opens it to the network again.

## Switching from the original (Linux, Docker) version

The data files open as they are: `tam-remote.db` for the server, `tam-local.db` and `settings.json` for the client. Point `TAM_DATA_DIR` at the old data folder (the `/data` of the old containers) or copy those files into the program's `data` folder, and start it; a backup file from the original restores too. The original programs do not talk to these, so switch the server and every client of an event together.

## Good to know

- `SHA256SUMS` lists the SHA-256 checksum of every file of the release; `sha256sum -c --ignore-missing SHA256SUMS` in the download folder checks the ones you took.
- The programs are not signed. Windows SmartScreen asks once (More info, then Run anyway); on macOS clear the quarantine flag once with `xattr -dr com.apple.quarantine tam-client` or `tam-server`.
- GitHub shows a package with a pre-release version as `1.0.0.rc1` in the file name (`tam-server_1.0.0.rc1-1_amd64.deb`, `tam-server-1.0.0.rc1-1.x86_64.rpm`, where `-1` is the package revision); the package inside is `1.0.0~rc1` and installs under any file name.
- The Windows programs keep a small icon in the notification area while they run; right-click it to shut them down. On Linux and macOS, Ctrl+C or the Shut Down button on the main menu does that.
