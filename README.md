# devdash

A live dashboard for a remote development box, viewed from your Mac. One
static binary, no runtime, standard library only.

It answers two questions: **where is my memory going**, and **what can I
stop**. Everything is attributed to a git checkout: the Herdr agents sitting
in it, the processes whose working directory is inside it (with their
listening ports as clickable links), and the Docker Compose stack started
from it, summed into one number per checkout and ranked.

- Memory, CPU, load, disk, and the kernel's memory-pressure (PSI) signal,
  with a live sparkline and a 24-hour chart
- Per-checkout cards with **View** buttons that open each web app through
  the tunnel, collapsed process and container tables, and a 24-hour growth
  badge
- A **Reclaim** strip: a deterministic ranking of what is safe to stop
  (orphaned and detached stacks first; checkouts with a working agent are
  never candidates), with a one-click **Stop everything**
- Forecast of time-to-exhaustion from the five-minute trend, and macOS
  notifications from the tab when thresholds are crossed
- Stop a stack, stop a container, kill a process, read container logs,
  prune volume-only Compose projects and the build cache: each confirmed,
  each written to an audit log
- Nothing listens outside loopback on either machine

## Install

On your Mac (Apple Silicon or Intel), with `ssh <your-devbox>` already
working non-interactively:

```sh
curl -fsSL https://raw.githubusercontent.com/clay-coffman/devdash/main/install.sh | sh
devdash setup <your-devbox>
```

`setup` runs the same installer on the devbox over SSH, enables a persistent
`systemd --user` service there (and turns on `loginctl` linger so it
survives logout), waits for it to answer, then opens the dashboard in a
dedicated Chrome window. The two machines never need to share anything but
that SSH alias.

From then on:

```sh
devdash open <your-devbox>                              # the dashboard
devdash open <your-devbox> http://localhost:5173/       # any app on the box
devdash open <your-devbox> --check                      # just test the tunnel
```

`open` keeps an `ssh -N -D` SOCKS tunnel to the host on a local port
derived from the alias (override with `-port`), and launches Chrome with a
per-host profile under `~/Library/Application Support/devdash/<host>` and
`--proxy-bypass-list=<-loopback>`, so `localhost` inside that window means
the devbox. The window is isolated from your normal Chrome and from other
hosts. A dropped tunnel makes requests fail rather than fall through to your
Mac; run `open` again to reconnect. Tunnel logs are in
`~/Library/Logs/devdash/`.

### Updating

Re-run the two install lines. `setup` is safe to repeat; it rewrites the
service unit and restarts it.

### Removing

```sh
ssh <your-devbox> '~/.local/bin/devdash uninstall-service'
rm ~/.local/bin/devdash
rm -rf "~/Library/Application Support/devdash" ~/Library/Logs/devdash
```

## On the devbox

`devdash serve` binds `127.0.0.1:9900` and reads:

| Source | Used for |
|---|---|
| `/proc`, `/sys/fs/cgroup` | memory, CPU, PSI, your processes and their ports, the Herdr cgroup total |
| `docker ps`, `docker stats`, `docker system df` | containers, memory against limits, published ports, disk |
| Compose labels | project → working directory → checkout |
| `git` | branch per checkout |
| `herdr agent list` (optional) | agents per checkout with status and PR |
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

## Reclaim scoring

| Condition | Score |
|---|---|
| any agent working | never |
| stack orphaned (checkout directory is gone) | 100 |
| stack detached (nothing in the checkout uses it) | 80 |
| no agent in the checkout | 70 |
| all agents done | 60 |
| agents idle | 40 |
| an agent is blocked waiting on you | 30 |

Ties break by memory. **Stop everything** brings the checkout's stacks down
(volumes kept) and sends `SIGTERM` to its processes, except `pi` agent
processes, which are left to Herdr.

## Development

```sh
go test ./...
go build ./cmd/devdash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o devdash-linux-amd64 ./cmd/devdash
devdash setup -binary ./devdash-linux-amd64 <your-devbox>   # install a local build
```

`testdata/` holds captured `/proc`, Docker, Herdr, and reaper output from a
real devbox; the collector and attribution tests run against it. Releases
are built by GitHub Actions on a `v*` tag.
