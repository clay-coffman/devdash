# devdash

A live dashboard for a remote development box, viewed from your Mac. One
static binary, no runtime, standard library only.

The **Overview** starts with current agents, checkout memory, and direct
**Open ↗** app buttons. **Workstreams** provides optional declared work
from herdr-board and factual Herdr activity. **Resources** shows processes,
Docker Compose stacks, and detailed host metrics. Neither work integration
is required; running checkout apps remain visible without them.

- Memory, CPU, load, disk, and the kernel's memory-pressure (PSI) signal,
  with a live sparkline and a 24-hour chart
- Per-checkout cards with **Open ↗** buttons that launch apps in new tabs
  through the tunnel, memory trends, and expandable **Resources** details
- Stable checkout ordering during live updates; **Sort by memory ↓**
  explicitly reorders the list
- Forecast of time-to-exhaustion from the five-minute trend, and macOS
  notifications from the tab when thresholds are crossed
- Stop a stack, stop a container, kill a process, read container logs,
  prune volume-only Compose projects and the build cache: each confirmed,
  each written to an audit log
- Nothing listens outside loopback on either machine

## Install

Run setup from your **Mac**, not from the remote host. No Go installation
or source checkout is needed. The devbox can be a cloud VM or any Linux
machine you can reach over SSH.

### Prerequisites

- **Mac:** Apple Silicon or Intel, with Google Chrome installed at
  `/Applications/Google Chrome.app`.
- **Devbox:** Linux (x86_64 or ARM64), with `systemd --user`, `curl`, and
  `git` available. Both machines need access to GitHub to download releases.
- **SSH:** key-based access to your normal development account on the devbox,
  without an interactive password prompt. Use the same account that owns
  your checkouts and runs your development processes.
- **Containers:** Docker and the Compose plugin are needed for container
  monitoring and stack actions; the SSH user must have Docker access.
- **Optional:** Herdr adds observed agent activity; herdr-board adds declared
  workstreams and cached evidence. Install them separately using their own
  instructions. `compose-stack-reaper` adds stack classification. None is
  required to get started.

The examples below use `devbox` as an SSH alias configured in
`~/.ssh/config`. Replace it with your own alias. First check access from
Terminal on the Mac:

```sh
ssh -o BatchMode=yes devbox 'echo connected'
```

If this fails, fix SSH access before continuing.

### Set up both machines

Run on the Mac:

```sh
curl -fsSL https://raw.githubusercontent.com/clay-coffman/devdash/main/install.sh | sh
export PATH="$HOME/.local/bin:$PATH"
devdash setup devbox
```

The installer downloads the matching binary, verifies its checksum, and
puts it in `~/.local/bin`. If that directory is not already on your PATH,
add the `export PATH` line to your shell configuration (usually `~/.zshrc`)
so `devdash` is also available in new terminals.

`setup` installs the matching release on the devbox over SSH, enables a
persistent user service, checks that it responds, and opens the dashboard
in a dedicated Chrome window. It also tries to enable **linger**, which
keeps the service running after you log out. If setup prints a linger
warning, run the suggested `sudo loginctl enable-linger <username>` command
on the devbox; this may require an administrator.

**No additional inbound ports or cloud firewall/security-group changes are
needed.** The dashboard binds to loopback and uses your existing SSH
connection, including for app links.

The installer downloads the **latest published release**, not the current
`main` branch. Changes on `main` become available through the installer
when a new release is published.

## Everyday use

Run on the Mac:

```sh
devdash open devbox                          # open the dashboard
devdash open devbox http://localhost:5173/    # open an app on the devbox
devdash open devbox --check                   # check the SSH tunnel
```

`open` keeps an `ssh -N -D` SOCKS tunnel to the host on a local port
derived from the alias (override with `-port`), and launches Chrome with a
per-host profile under `~/Library/Application Support/devdash/<host>` and
`--proxy-bypass-list=<-loopback>`, so `localhost` inside that window means
the devbox. The window is isolated from your normal Chrome and from other
hosts. A dropped tunnel makes requests fail rather than fall through to your
Mac; run `open` again to reconnect. Tunnel logs are in
`~/Library/Logs/devdash/`.

### Overview and optional Workstreams

Overview is the landing page. The old saved Work choice migrates to Overview;
an explicitly saved Resources choice is retained. Choosing a view is remembered
in this browser, and polling never switches it. The compact host-health header,
live status/app counts, search and short repository filter precede exact checkout
rows. App buttons open in one click, including checkouts without agents.
Non-Git agents remain visible by working directory and workspace.

Rows initially prioritize agents needing input, working agents, then apps.
Idle/agent-finished checkouts without apps and resource-only rows sit under
**Show other checkouts**. Admitted rows stay in place as statuses change;
new activity appends. **Refresh list** reapplies priority and folds quiet rows
away. Filters, keyboard focus and inline Details remain stable through polling.
Details show agents and supplied plan/PR summaries first; full identities,
source facts, processes and container controls are collapsed.

**Data sources** exposes timestamps and errors without a diagnostic wall.
Unavailable Herdr means unknown counts, not zero working or zero needs input.
Last-known rows/apps are retained on failures; only successful fresh observations
remove absent entities. Fresh, unambiguous API mappings may supply task labels. A binding is scoped by
catalog, task, checkout, registered plan and Herdr server (reviewer bindings
instead use the registered agent); pane and checkout location must agree with
the board's scoped observation. Paths and pane IDs remain physical identity.
A checkout shared by several plans remains one resource row without a guessed
task, even when one pane seems to match. Idle/agent finished never means task complete,
and board estimates never create live input status.

Workstreams defaults to **Live**: only observed working/blocked agents and
running checkout apps appear. Fresh, exact, unambiguous board declarations
can group this activity; everything else appears under **Ungrouped activity**,
including app-only checkouts and non-Git agents. Stale or failed board reads
cannot hide agents or apps. Last-known observations are marked uncertain on
source failures; a fresh successful absence removes them. Cached lifecycle
names and estimates do not establish current activity or task completion.

**All registered** exposes the entire supplied catalog and its tasks,
including parked, completed, canceled, and unverified records. Coordination
and Standalone sessions have folded secondary sections, not inferred task
ownership. A host catalog may span repositories; its directory is not treated
as a repository, and a checkout's board `repo` is only a display label. A stale or
unreconciled Now declaration appears under **Activity unverified**, not as
live work or a request for input. These labels change only the presentation,
not catalog intent. Source labels, warnings, plan counts, checkout facts,
and observed PR/check/queue facts remain in the detail inspector. A merged PR
is not evidence of deployment or accepted completion. Unsupported cached
projection versions remain inspectable without consuming their task structure.
Exact checkout paths and pane IDs are used, never branch names, display titles,
or directory basenames. Foreign-server declarations keep their Git and plan
facts but cannot borrow local panes or claim local app-only activity. Reviewer
facts are observational. Multiple bindings can inspect the same physical
resource card; its memory and app controls are counted only once.

Workstreams retains search, repository filtering and Summary, Agents and
Resources selection tabs. For a multi-repository host stream, All registered
filters its individual tasks through exact declared checkout paths and
resolvable local Git repository identities, including app-only checkouts;
repeated display labels and unknown repositories never serve as join keys. Selection details expand beneath their row, without
a permanently empty inspector column.
Overview's inline resource details and the Workstreams Resources tab reuse
the usual checkout cards and confirmed individual controls. If a declared
checkout has no resource card, it says so rather than showing another
checkout's app. There are no agent controls, board actions, catalog editing,
or stop-safety recommendations. Resource Open buttons share the same exact
process/checkout/port detection as Overview.

#### Sources, freshness, and privacy

- `/api/work` is a versioned, typed, cached projection separate from
  `/api/state`. HTTP reads run no external commands. Resource sampling stays
  on its two-second loop; Herdr is collected separately every five seconds.
- Devdash auto-detects `herdr-board` and runs exactly
  `herdr-board json --no-sweep` at startup and every 15 seconds, in a separate
  loop. Reads have a ten-second timeout and an 8 MiB output limit. No shell
  is constructed from board data, and command diagnostics are bounded.
- **Devdash reads the board's existing cache; it does not refresh that cache.**
  Observation and assessment refresh belongs to herdr-board. If its collector
  is stopped, old evidence remains old. The page shows the board sweep,
  individual observation, PR, and assessment times separately from the time
  devdash read the cache. Missing timestamps mean unknown age, not fresh data.
- Failed, malformed, oversized, or timed-out reads retain the last good data
  with an explicit error/stale label and original observation times.
  Successful empty results clear old entities. Missing tools and a stopped
  Herdr server are not interpreted as empty successful collections.
- Both commands receive the same explicit child-command socket context. An
  off-request-path read-only `herdr status server --json` check verifies the
  CLI-reported endpoint; a matching board/server fingerprint alone does not
  prove routing. For a nondefault Herdr server, set `HERDR_SOCKET_PATH` in
  **devdash's service environment** to the same socket used by Herdr and the
  board. An interactive shell export does not change an already-running
  service. Missing/malformed/mismatched identities or failed endpoint checks
  withhold modern membership while retaining observed resources. Older
  server-less payloads retain their legacy same-server contract. Devdash does
  not modify another service's environment or start a board daemon.
- The browser receives only an allowlisted subset of identity, progress,
  checkout, PR, timestamp, and assessment facts. Cleanup records, conversation
  text, tool history, session-file paths, credentials, and configuration are
  omitted. Host-catalog scope is retained only in the backend resolver, never
  forwarded as raw `derived.host_catalog`, `observation.host_catalog`, the
  host stream's source/directory or an embedded path in its public keys.
  Modern catalog keys use an opaque digest of their source scope. Known host
  source paths in producer diagnostics are replaced at the host-row boundary;
  repository-catalog and legacy source labels retain their existing meaning.
  `/api/work` adds the producer server fingerprint, local routing verdict,
  binding keys, scoped agent-task keys and validated physical-checkout memberships. `/api/state`
  additionally supplies a checkout's exact Git repository when resolvable,
  allowing app-only repository filtering without guessing from its label.
  These additions do not add raw brief, host-configuration or session paths.
  An explicit registered plan mismatch invalidates membership; an unavailable
  plan read is not itself proof of a match or mismatch when independently
  scoped task evidence is exact. Registered reviewers can participate in an
  established owner assignment but cannot establish ownership alone. Supplied text is rendered
  as text; outbound source links permit
  only HTTP(S). Existing AI assessments appear only in agent details, labeled
  as estimates with their assessment times; devdash makes no AI calls.

### Troubleshooting

- **`devdash: command not found`:** add `~/.local/bin` to your PATH, or run
  `~/.local/bin/devdash` directly.
- **Chrome not found:** install Google Chrome in `/Applications`.
- **Cannot connect:** verify SSH with the command above, then run
  `devdash open devbox --check`. This checks the tunnel, not the dashboard
  service itself.
- **Dashboard service not responding:** inspect it on the devbox:
  ```sh
  ssh devbox 'systemctl --user status devdash --no-pager'
  ssh devbox 'journalctl --user -u devdash -n 50 --no-pager'
  ```
- **Docker data is missing:** check that `ssh devbox 'docker ps'` works as
  the same user, without `sudo`.
- **Work provider is missing:** check the service's PATH, not just your shell's.
  The installed service includes `~/.local/bin` and the usual runtime shims.
  Resources still works without either optional provider.
- **Board is stale:** compare the sweep/observation times with the cache-read
  time. Ensure herdr-board's own collector is refreshing its cache under the
  same user/socket. Reopening devdash does not perform a sweep.
- **Herdr read failed or the wrong agents appear:** verify the Herdr server is
  running and both providers use the service's `HERDR_SOCKET_PATH`. A failed
  refresh retains previous observations, explicitly labeled stale.
- **Unknown schema or invalid output:** update the optional provider separately
  or inspect its bounded read failure. Devdash does not silently reinterpret
  unsupported derived task versions.

### Updating

Re-run the installer and `devdash setup devbox` on the Mac. This updates
both machines to the latest published release. `setup` is safe to repeat;
it rewrites the service unit and restarts it.

### Removing

```sh
ssh devbox '~/.local/bin/devdash uninstall-service'
rm ~/.local/bin/devdash
rm -rf "$HOME/Library/Application Support/devdash" "$HOME/Library/Logs/devdash"
```

## On the devbox

`devdash serve` binds `127.0.0.1:9900` and reads:

| Source | Used for |
|---|---|
| `/proc`, `/sys/fs/cgroup` | memory, CPU, PSI, your processes and their ports, the Herdr cgroup total |
| `docker ps`, `docker stats`, `docker system df` | containers, memory against limits, published ports, disk |
| Compose labels | project → working directory → checkout |
| `git` | branch per checkout |
| `herdr agent list` (optional) | pane identities, checkout activity, reported status/context/PR metadata |
| `herdr-board json --no-sweep` (optional) | cached declarations, evidence, observation and assessment timestamps |
| `compose-stack-reaper` (optional) | live / detached / orphaned / foreign per stack |
| `~/.local/state/devbox-mem/samples.log` (optional) | extra history for the 24-hour chart |

Without Herdr the agent rows are simply absent; without the reaper every
stack under your home directory is "unknown" and anything outside it is
"foreign" and gets no buttons. devdash writes its own one-minute samples to
`~/.local/state/devdash/samples.log` and `checkouts.log` (kept for seven
days) and its action log to `~/.local/state/devdash/actions.log`.

Actions are `POST /api/action` with a per-process token the page carries,
so a stray page on another origin cannot fire them. This is accident
prevention, not a security boundary: the service runs as you, on loopback,
behind your SSH key.

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
node --test internal/web/*_test.cjs     # UI regression tests; requires Node.js for development only
node --check internal/web/static/app.js
node --check internal/web/static/work.js
go build ./cmd/devdash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o devdash-linux-amd64 ./cmd/devdash
devdash setup -binary ./devdash-linux-amd64 devbox   # install a local build
```

`testdata/` holds captured `/proc`, Docker, Herdr, and reaper output from a
real devbox; the collector and attribution tests run against it. New optional
board fixtures in `internal/work/testdata/` are synthetic and contain no live
host, project, or session data. Releases are built by GitHub Actions on a `v*` tag.
