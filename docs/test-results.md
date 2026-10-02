# Test results

The printed results of the tests, as the tools print them. CI prints the same for every push, on the run's page on GitHub (Actions), and keeps them as files under Artifacts. [Running the tests](../README.md#running-the-tests) has the commands.

- Date: 2026-10-01
- Code: commit `27286de`, every test below
- Where: Linux containers (Docker Desktop on Windows 11, AMD Ryzen 9 7900X3D, 24 threads), so that no test program listens on the Windows host
- Tools: go1.27.1 linux/amd64, Node 24.20.0, pnpm 12.6.0, Chromium from Playwright 1.63.0; the race detector with gcc 12
- Not run here: the Nix build and NixOS test, and the release build with its package tests; CI runs both on every push.

## Unit tests

`go test -count=1 ./...`

```
?   	ticket-auction-manager/tam-go/build/validation/ci-report-fix	[no test files]
?   	ticket-auction-manager/tam-go/cmd/tam-client	[no test files]
?   	ticket-auction-manager/tam-go/cmd/tam-server	[no test files]
ok  	ticket-auction-manager/tam-go/internal/admin	2.502s
ok  	ticket-auction-manager/tam-go/internal/client	20.050s
ok  	ticket-auction-manager/tam-go/internal/config	1.116s
ok  	ticket-auction-manager/tam-go/internal/db	0.646s
?   	ticket-auction-manager/tam-go/internal/desktop	[no test files]
ok  	ticket-auction-manager/tam-go/internal/discovery	0.012s
?   	ticket-auction-manager/tam-go/internal/env	[no test files]
ok  	ticket-auction-manager/tam-go/internal/guard	0.102s
ok  	ticket-auction-manager/tam-go/internal/httpx	0.006s
ok  	ticket-auction-manager/tam-go/internal/presence	0.009s
ok  	ticket-auction-manager/tam-go/internal/remote	0.075s
ok  	ticket-auction-manager/tam-go/internal/server	4.028s
ok  	ticket-auction-manager/tam-go/internal/store	5.186s
ok  	ticket-auction-manager/tam-go/internal/sync	5.842s
ok  	ticket-auction-manager/tam-go/internal/tlscert	0.080s
?   	ticket-auction-manager/tam-go/internal/version	[no test files]
?   	ticket-auction-manager/tam-go/scripts/loadtest	[no test files]
ok  	ticket-auction-manager/tam-go/scripts/shutdown	8.923s
```

## Unit tests with the race detector

`CGO_ENABLED=1 go test -race -count=1 ./...`: no data race reported.

```
?   	ticket-auction-manager/tam-go/build/validation/ci-report-fix	[no test files]
?   	ticket-auction-manager/tam-go/cmd/tam-client	[no test files]
?   	ticket-auction-manager/tam-go/cmd/tam-server	[no test files]
ok  	ticket-auction-manager/tam-go/internal/admin	13.432s
ok  	ticket-auction-manager/tam-go/internal/client	33.553s
ok  	ticket-auction-manager/tam-go/internal/config	1.905s
ok  	ticket-auction-manager/tam-go/internal/db	1.754s
?   	ticket-auction-manager/tam-go/internal/desktop	[no test files]
ok  	ticket-auction-manager/tam-go/internal/discovery	1.033s
?   	ticket-auction-manager/tam-go/internal/env	[no test files]
ok  	ticket-auction-manager/tam-go/internal/guard	1.145s
ok  	ticket-auction-manager/tam-go/internal/httpx	1.046s
ok  	ticket-auction-manager/tam-go/internal/presence	1.050s
ok  	ticket-auction-manager/tam-go/internal/remote	1.225s
ok  	ticket-auction-manager/tam-go/internal/server	6.222s
ok  	ticket-auction-manager/tam-go/internal/store	7.615s
ok  	ticket-auction-manager/tam-go/internal/sync	7.263s
ok  	ticket-auction-manager/tam-go/internal/tlscert	1.220s
?   	ticket-auction-manager/tam-go/internal/version	[no test files]
?   	ticket-auction-manager/tam-go/scripts/loadtest	[no test files]
ok  	ticket-auction-manager/tam-go/scripts/shutdown	10.668s
```

## Every release target

`go vet ./...` and `go build ./...` with `CGO_ENABLED=0` for each target.

```
windows/amd64 ok
windows/arm64 ok
linux/amd64 ok
linux/arm64 ok
darwin/amd64 ok
darwin/arm64 ok
```

## Browser tests

`pnpm test` in `scripts/browser`, against the Linux programs of this commit.

```
Running 18 tests using 1 worker
  ✓   1 counts.spec.js:3:1 › a prefix named Total is a row of its own beside the total (247ms)
  ✓   2 save-on-leave.spec.js:34:1 › A description saved from the Baskets form leaves a drawn winner alone (355ms)
  ✓   3 save-on-leave.spec.js:54:5 › Tickets: marked rows survive hidden (277ms)
  ✓   4 save-on-leave.spec.js:54:5 › Tickets: marked rows survive navigation (404ms)
  ✓   5 save-on-leave.spec.js:54:5 › Tickets: marked rows survive close (357ms)
  ✓   6 save-on-leave.spec.js:54:5 › Baskets: marked rows survive hidden (265ms)
  ✓   7 save-on-leave.spec.js:54:5 › Baskets: marked rows survive navigation (338ms)
  ✓   8 save-on-leave.spec.js:54:5 › Baskets: marked rows survive close (321ms)
  ✓   9 save-on-leave.spec.js:54:5 › Drawing: marked rows survive hidden (259ms)
  ✓  10 save-on-leave.spec.js:54:5 › Drawing: marked rows survive navigation (326ms)
  ✓  11 save-on-leave.spec.js:54:5 › Drawing: marked rows survive close (308ms)
  ✓  12 save-on-leave.spec.js:54:5 › Search: marked rows survive hidden (208ms)
  ✓  13 save-on-leave.spec.js:54:5 › Search: marked rows survive navigation (309ms)
  ✓  14 save-on-leave.spec.js:54:5 › Search: marked rows survive close (269ms)
  ✓  15 stale-saves.spec.js:46:1 › a page saving over a newer value shows the newer value and says so (1.6s)
  ✓  16 stale-saves.spec.js:75:1 › an offline save that arrives after a newer one waits in Settings for Retry (7.5s)
  ✓  17 stale-saves.spec.js:99:1 › a winner from a page loaded before another winner was entered does not replace it (1.5s)
  ✓  18 stale-saves.spec.js:121:1 › reports read from this computer's copy while the server is away say so (7.2s)
  18 passed (28.0s)
```

## Load test: 50 clients

`go run ./scripts/loadtest -clients 50 -tickets 9000 -baskets 1000`

```
tam load test: 50 clients, 9000 tickets and 1000 baskets in 5 prefixes, sheets of 25 rows (linux/amd64, 24 CPUs)
built tam-server and tam-client in 1.2s
tam-server 0.0.1 answering on http://127.0.0.1:46431
50 tam-client programs answering after 0.2s
Pairing...
Setup...
Ticket entry...
  server killed after 93 of 360 sheets; starting it again in 8s
  2 clients crashed and started again
  server started again
  Wi-Fi of 12 clients dropping for 12s
  Wi-Fi back everywhere, queues sent
Corrections...
A client's key deleted...
Everyone on the same tickets...
Baskets...
Drawing...
Reports and searches...
Rush...

Phases
  Pairing: 0.3s, 50 requests (160 a second)
    action                           count    rows   median      p95      p99      max errors queued
    pair with the server                50            106ms    237ms    242ms    242ms      0      0
  Setup: 0.0s, 51 requests (1517 a second), 5 rows saved in 1 saves (149 rows and 30 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save prefixes                        1       5    8.8ms    8.8ms    8.8ms    8.8ms      0      0
    list prefixes                       50             21ms     24ms     24ms     24ms      0      0
  Ticket entry: 40.0s, 1748 requests (44 a second), 9179 rows saved in 539 saves (229 rows and 13 saves a second)
    360 sheets, one every 5000ms on each client; server killed at 5.2s, back at 13.2s (2 clients crashed and restarted meanwhile); all 139 queued saves sent 4.7s after that; the Wi-Fi of 12 clients dropped at 20.1s for 17.0s (12 saves hung until queued), all caught up 0.7s after it was back
    action                           count    rows   median      p95      p99      max errors queued
    open ticket sheet                  445            4.1ms     29ms     34ms     38ms      0      0
    save ticket sheet                  360    9000     49ms    191ms   5010ms   5013ms      0    133
    fix a typo                          60      60     25ms     88ms     96ms    113ms      0     25
    status bar                         650            0.6ms    1.3ms    1.6ms    1.7ms      0      0
    admin status page                    7            0.8ms    1.1ms    1.1ms    1.1ms      0      0
    open a sheet saved offline         107            0.4ms    0.7ms    1.2ms    1.2ms      0      0
    correct a sheet saved offline      107     107    5.3ms    7.2ms    8.1ms    8.5ms      0    107
    type a row again after a slow save      12      12    5.9ms    6.5ms    6.8ms    6.8ms      0     12
  Corrections: 0.1s, 50 requests (612 a second), 225 rows saved in 50 saves (2754 rows and 612 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save corrections                    50     225     50ms     79ms     81ms     81ms      0      0
  A client's key deleted: 0.1s, 4 requests (35 a second), 50 rows saved in 2 saves (432 rows and 17 saves a second)
    client-26's key deleted on the server; 50 tickets corrected meanwhile, then paired again
    action                           count    rows   median      p95      p99      max errors queued
    open Settings                        1            0.2ms    0.2ms    0.2ms    0.2ms      0      0
    save while the key is refused        2      50    3.2ms    3.8ms    3.8ms    3.8ms      0      2
    pair again                           1            4.7ms    4.7ms    4.7ms    4.7ms      0      0
  Everyone on the same tickets: 5.0s, 4068 requests (806 a second), 3467 rows saved in 3467 saves (687 rows and 687 saves a second)
    50 clients saving the same 10 tickets for 5.0s
    action                           count    rows   median      p95      p99      max errors queued
    save a ticket everyone saves      3467    3467     72ms     82ms     91ms    100ms      0      0
    status bar                         100            0.3ms    0.4ms    0.4ms    0.5ms      0      0
    admin status page                    1            0.6ms    0.6ms    0.6ms    0.6ms      0      0
    open a ticket everyone saved       500             21ms     25ms     25ms     25ms      0      0
  Baskets: 0.1s, 80 requests (744 a second), 1000 rows saved in 40 saves (9295 rows and 372 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open basket sheet                   40             16ms     21ms     22ms     22ms      0      0
    save basket sheet                   40    1000     57ms     83ms     85ms     85ms      0      0
  Drawing: 0.4s, 1080 requests (2973 a second), 1000 rows saved in 40 saves (2753 rows and 110 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open drawing sheet                  40             22ms     37ms     43ms     43ms      0      0
    look up the winner                1000            4.7ms     25ms     37ms     45ms      0      0
    save drawing sheet                  40    1000     69ms     93ms    103ms    103ms      0      0
  Reports and searches: 1.8s, 1000 requests (550 a second)
    action                           count    rows   median      p95      p99      max errors queued
    counts report                       50            173ms    195ms    196ms    196ms      0      0
    report by basket                   250             34ms     49ms     56ms     70ms      0      0
    report by name                     250             34ms     65ms     73ms     86ms      0      0
    drawing results                    250             38ms     59ms     81ms     85ms      0      0
    search by last name                150            314ms    364ms    387ms    387ms      0      0
    status bar                          50            0.5ms    5.2ms     14ms     14ms      0      0
  Rush: 10.1s, 4790 requests (475 a second), 115950 rows saved in 4638 saves (11487 rows and 459 saves a second)
    every save changes every row of its sheet
    action                           count    rows   median      p95      p99      max errors queued
    save a changed ticket sheet       4638  115950    108ms    119ms    125ms    200ms      0      0
    admin status page                    2            0.8ms    0.9ms    0.9ms    0.9ms      0      0
    status bar                         150            0.3ms    0.4ms    0.5ms    0.6ms      0      0

Programs
  tam-server 0.0.1: CPU 27.6s over 2 runs, peak memory 126 MB, database 5 MB
  tam-client x50: CPU 33.3s in all (0.7s each on average), peak memory 26 MB for the largest
  this machine: linux/amd64, 24 CPUs; the test ran 60.5s

Checks
  PASS  every client paired and showed Connected: 50 clients, in 0.3s
  PASS  the server holds every ticket as last saved (after the drawing): 9000 tickets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every basket and winner as saved (after the drawing): 1000 baskets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every prefix (after the drawing): 5 saved, 5 on the server
  PASS  every client's own copy shows what it saved: 50 clients; 0 rows differ on 0 of them
  PASS  the server holds every ticket as last saved (after the rush): 9000 tickets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every basket and winner as saved (after the rush): 1000 baskets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every prefix (after the rush): 5 saved, 5 on the server
  PASS  every client ends connected with nothing waiting or refused: 50 clients: 0 not connected, 0 saves waiting, 0 refused by the server
  PASS  the admin page's Clients table lists every client as connected and caught up: 50 rows for 50 clients: 50 connected, 50 with a last update, 50 with nothing queued
  PASS  the admin page counts every prefix, ticket and basket: 5 prefixes, 9000 tickets, 1000 baskets (saved: 5, 9000, 1000)
  PASS  every request was answered: 12921 requests, 0 failed
  PASS  the server started again after being killed
  PASS  saves were queued only while the server was out of reach: 279 saves queued, 0 of them while the client could reach the server
  PASS  no page action waited more than 6 s: the slowest: save ticket sheet in Ticket entry, 5013ms
  PASS  every client saw the server again after the restart: 50 of 50 clients went back to their offline sheets while saves were still queued
  PASS  every sheet showed all its rows
  PASS  a sheet opened again showed what was saved
  PASS  reports, searches and winner lookups match the data
  PASS  the clients sent everything they queued
  PASS  every crashed client came back with its queue: 2 crashed
  PASS  every client whose Wi-Fi dropped sent what it queued: clients [4 8 12 16 20 24 28 32 36 40 44 48]
  PASS  a client whose key was deleted said so, and pairing again sent its queue
  PASS  tickets everyone saved at once end whole, and every client shows them
  PASS  every client shut down cleanly when asked: 50 of 50
  PASS  no errors in what the programs wrote: 51 programs, 1710 lines, 0 errors

PASSED: all 26 checks
```

## Load test: 20 clients over HTTPS, the server down for 45 s

`go run ./scripts/loadtest -clients 20 -tickets 9000 -baskets 1000 -tls -outage 45s`

```
tam load test: 20 clients, 9000 tickets and 1000 baskets in 5 prefixes, sheets of 25 rows (linux/amd64, 24 CPUs)
built tam-server and tam-client in 1.1s
tam-server 0.0.1 answering on https://127.0.0.1:41245
20 tam-client programs answering after 0.1s
Pairing...
Setup...
Ticket entry...
  server killed after 91 of 360 sheets; starting it again in 45s
  Wi-Fi of 5 clients dropping for 12s
  2 clients crashed and started again
  server started again
  Wi-Fi back everywhere, queues sent
Corrections...
A client's key deleted...
Everyone on the same tickets...
Baskets...
Drawing...
Reports and searches...
Rush...

Phases
  Pairing: 0.2s, 20 requests (107 a second)
    action                           count    rows   median      p95      p99      max errors queued
    pair with the server                20             78ms    108ms    127ms    127ms      0      0
  Setup: 0.0s, 21 requests (1102 a second), 5 rows saved in 1 saves (262 rows and 52 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save prefixes                        1       5    6.9ms    6.9ms    6.9ms    6.9ms      0      0
    list prefixes                       20            8.8ms     12ms     12ms     12ms      0      0
  Ticket entry: 59.4s, 1452 requests (24 a second), 9155 rows saved in 515 saves (154 rows and 9 saves a second)
    360 sheets, one every 2222ms on each client; server killed at 8.9s, back at 54.0s (2 clients crashed and restarted meanwhile); all 323 queued saves sent 5.3s after that; the Wi-Fi of 5 clients dropped at 22.2s for 14.2s (0 saves hung until queued), all caught up 22.9s after it was back
    action                           count    rows   median      p95      p99      max errors queued
    open ticket sheet                  455            0.9ms    8.5ms    9.7ms     10ms      0      0
    save ticket sheet                  360    9000    9.6ms     62ms     80ms     85ms      0    269
    fix a typo                          55      55    4.9ms     27ms     32ms     42ms      0     47
    status bar                         380            0.6ms    1.1ms    1.5ms    1.7ms      0      0
    admin status page                    2            0.3ms    0.9ms    0.9ms    0.9ms      0      0
    open a sheet saved offline         100            0.4ms    1.0ms    1.7ms    2.2ms      0      0
    correct a sheet saved offline      100     100    4.8ms    8.8ms    9.5ms     10ms      0    100
  Corrections: 0.0s, 20 requests (440 a second), 225 rows saved in 20 saves (4945 rows and 440 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save corrections                    20     225     25ms     42ms     45ms     45ms      0      0
  A client's key deleted: 0.1s, 4 requests (33 a second), 50 rows saved in 2 saves (419 rows and 17 saves a second)
    client-11's key deleted on the server; 50 tickets corrected meanwhile, then paired again
    action                           count    rows   median      p95      p99      max errors queued
    open Settings                        1            0.2ms    0.2ms    0.2ms    0.2ms      0      0
    save while the key is refused        2      50    3.5ms    4.2ms    4.2ms    4.2ms      0      2
    pair again                           1            7.5ms    7.5ms    7.5ms    7.5ms      0      0
  Everyone on the same tickets: 5.0s, 4136 requests (825 a second), 3895 rows saved in 3895 saves (777 rows and 777 saves a second)
    20 clients saving the same 10 tickets for 5.0s
    action                           count    rows   median      p95      p99      max errors queued
    save a ticket everyone saves      3895    3895     25ms     33ms     36ms     42ms      0      0
    admin status page                    1            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    status bar                          40            0.3ms    0.4ms    0.4ms    0.4ms      0      0
    open a ticket everyone saved       200            5.8ms    8.1ms    8.3ms    8.4ms      0      0
  Baskets: 0.1s, 80 requests (934 a second), 1000 rows saved in 40 saves (11677 rows and 467 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open basket sheet                   40            1.0ms    5.9ms    6.5ms    6.5ms      0      0
    save basket sheet                   40    1000     34ms     43ms     47ms     47ms      0      0
  Drawing: 0.3s, 1081 requests (4272 a second), 1000 rows saved in 40 saves (3952 rows and 158 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open drawing sheet                  40            3.8ms     13ms     16ms     16ms      0      0
    look up the winner                1000            1.3ms    6.2ms    9.6ms     17ms      0      0
    save drawing sheet                  40    1000     55ms     80ms     82ms     82ms      0      0
    admin status page                    1            0.5ms    0.5ms    0.5ms    0.5ms      0      0
  Reports and searches: 0.6s, 380 requests (640 a second)
    action                           count    rows   median      p95      p99      max errors queued
    counts report                       20             71ms     81ms     81ms     81ms      0      0
    report by basket                   100            6.5ms     14ms     18ms     20ms      0      0
    report by name                     100            6.6ms     13ms     15ms     19ms      0      0
    drawing results                    100            9.8ms     17ms     20ms     22ms      0      0
    search by last name                 60            115ms    134ms    136ms    139ms      0      0
  Rush: 10.0s, 5107 requests (509 a second), 125625 rows saved in 5025 saves (12517 rows and 501 saves a second)
    every save changes every row of its sheet
    action                           count    rows   median      p95      p99      max errors queued
    save a changed ticket sheet       5025  125625     40ms     44ms     48ms     57ms      0      0
    status bar                          80            0.3ms    0.4ms    0.4ms    0.5ms      0      0
    admin status page                    2            0.5ms    0.8ms    0.8ms    0.8ms      0      0

Programs
  tam-server 0.0.1: CPU 17.2s over 2 runs, peak memory 103 MB, database 5 MB
  tam-client x20: CPU 24.6s in all (1.2s each on average), peak memory 32 MB for the largest
  this machine: linux/amd64, 24 CPUs; the test ran 77.7s

Checks
  PASS  every client paired and showed Connected: 20 clients, in 0.2s
  PASS  the server holds every ticket as last saved (after the drawing): 9000 tickets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every basket and winner as saved (after the drawing): 1000 baskets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every prefix (after the drawing): 5 saved, 5 on the server
  PASS  every client's own copy shows what it saved: 20 clients; 0 rows differ on 0 of them
  PASS  the server holds every ticket as last saved (after the rush): 9000 tickets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every basket and winner as saved (after the rush): 1000 baskets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every prefix (after the rush): 5 saved, 5 on the server
  PASS  every client ends connected with nothing waiting or refused: 20 clients: 0 not connected, 0 saves waiting, 0 refused by the server
  PASS  the admin page's Clients table lists every client as connected and caught up: 20 rows for 20 clients: 20 connected, 20 with a last update, 20 with nothing queued
  PASS  the admin page counts every prefix, ticket and basket: 5 prefixes, 9000 tickets, 1000 baskets (saved: 5, 9000, 1000)
  PASS  every request was answered: 12301 requests, 0 failed
  PASS  the server started again after being killed
  PASS  saves were queued only while the server was out of reach: 418 saves queued, 0 of them while the client could reach the server
  PASS  no page action waited more than 6 s: the slowest: search by last name in Reports and searches, 139ms
  PASS  every client saw the server again after the restart: 20 of 20 clients went back to their offline sheets while saves were still queued
  PASS  every sheet showed all its rows
  PASS  a sheet opened again showed what was saved
  PASS  reports, searches and winner lookups match the data
  PASS  the clients sent everything they queued
  PASS  every crashed client came back with its queue: 2 crashed
  PASS  every client whose Wi-Fi dropped sent what it queued: clients [4 8 12 16 20]
  PASS  a client whose key was deleted said so, and pairing again sent its queue
  PASS  tickets everyone saved at once end whole, and every client shows them
  PASS  every client shut down cleanly when asked: 20 of 20
  PASS  no errors in what the programs wrote: 21 programs, 1041 lines, 0 errors

PASSED: all 26 checks
```
