# devdash — plan, phase 2: share it, and make it act

> Historical implementation plan. The B1 reclaim recommendations, checkout
> verdicts, and bulk `checkout.stop` action were subsequently removed by
> product decision. The README describes the current feature set.

Phase 1 (done 2026-10-06): Go binary, live page, per-checkout attribution,
guarded actions, running as a transient unit on clay-aws-devbox. This phase
makes it installable by someone else in two commands and adds the features
that turn it from a display into a tool.

## Part A — distribution

### A1. One binary, five subcommands

| Command | Where | What |
|---|---|---|
| `devdash serve` | dev host | as today, plus the built-in sampler (A2) |
| `devdash install-service` | dev host | writes and enables a persistent user service: `~/.config/systemd/user/devdash.service` on Linux (`enable --now`, warns if `loginctl` linger is off), a launch agent on macOS |
| `devdash open <ssh-host> [url…]` | Mac | ensures a SOCKS tunnel to the host (starts `ssh -N -D 127.0.0.1:<port> <ssh-host>` detached if nothing listens; port derived from the host name, overridable), then opens Chrome with a per-host profile and `--proxy-bypass-list=<-loopback>`. Default URL is the dashboard. Replaces `remote-chrome` for anyone who doesn't have it. |
| `devdash setup <ssh-host>` | Mac | runs the installer on the host over SSH, runs `install-service` there, then `open`. The two-command onboarding. |
| `devdash version` | both | build version from the release tag |

`open` and `setup` assume `ssh <ssh-host>` already works (keys in an agent);
if it prompts, it prompts. Tunnel logs go to `~/Library/Logs/devdash/<host>.log`.

### A2. Built-in sampler (no dependency on devbox-mem-sample)

Every 60 s, `serve` appends to `~/.local/state/devdash/samples.log` in the
same line format `devbox-mem-sample` uses (so either file feeds the chart),
and to `checkouts.log` one line per checkout with its total bytes. Both are
trimmed to 7 days. `/api/history` merges devdash's log with the
devbox-mem-sample log when that exists, deduplicated by minute.

### A3. GitHub repo, releases, installer

- Public repo `github.com/clay-coffman/devdash`, MIT, with this plan,
  README (coworker instructions first), and the fixtures.
- Actions: `go test` on push; on a `v*` tag, build linux/amd64,
  linux/arm64, darwin/arm64, darwin/amd64 with `-ldflags -X main.version`,
  attach them with `SHA256SUMS` to a release.
- `install.sh` at the repo root: detects OS/arch, downloads the latest (or
  `DEVDASH_VERSION=`) release asset, verifies the checksum, installs to
  `~/.local/bin/devdash`, prints a PATH hint if needed. The same script is
  embedded in the binary so `setup` can run it on the host.
- Coworker onboarding, from his Air:
  ```
  curl -fsSL https://raw.githubusercontent.com/clay-coffman/devdash/main/install.sh | sh
  devdash setup <his-devbox-ssh-alias>
  ```
- I create the repo and push with your `gh`/git identity (SSH-signed
  commits via 1Password), tag `v0.1.0`, and confirm the release assets
  exist. Nothing else in your dotfiles changes in this phase; chezmoi
  version-pinning and retiring `remote-chrome` stay deferred.

## Part B — features

### B1. Reclaim candidates and "stop everything"

Each checkout gets a deterministic `reclaim` score with reasons, computed
server-side and shown on the card:

| Condition | Score | Reason shown |
|---|---|---|
| any agent `working` | 0, never | "agent working" |
| stack classified orphaned | 100 | "orphaned stack: checkout is gone" |
| stack classified detached | 80 | "detached: no agent or process in the checkout" |
| no agents, processes only | 70 | "no agent here" |
| all agents `done` | 60 | "all agents done" |
| any agent `blocked` | 30 | "agent blocked (waiting on you)" |
| all agents `idle` | 40 | "agents idle" |

Ties break by bytes. A **Reclaim** strip above the cards lists the top
three with bytes and reason; it is always present but turns red when
available memory is under 15% or memory PSI `some` exceeds 5%. Each
candidate has **Stop everything**: `docker compose down` for its stacks
(volumes kept) and `SIGTERM` to its processes, **except `pi` agent
processes, which are left to Herdr** so an agent is never killed out from
under its tab. Refused outright if any agent in the checkout is `working`.
Action type `checkout.stop`, audited like the others.

### B2. OOM forecast and notifications

From the last five minutes of live points, a least-squares slope of
available memory. If it is falling, the memory tile and the header glance
show "≈ N min to 4 GiB free at this rate". A **Notify** button asks for
browser notification permission; once granted, the tab fires a macOS
notification when any of these holds, with a ten-minute cooldown: forecast
under 15 minutes, available under 4 GiB, or memory PSI `some` over 10%.
Thresholds live in `localStorage` and are editable from a small settings
popover.

### B3. Per-checkout history

Using `checkouts.log` from A2: each card shows a 24-hour sparkline of its
total and a delta badge ("+2.1 GiB / 1 h") coloured when growth exceeds
512 MiB per hour. `/api/history` returns the per-checkout series alongside
the host series.

### B4. Small items

- **PR state on agent chips**: Herdr already reports `pr` (`#522 ✗`); show
  it on the chip, and expose it in the reclaim reasons when present.
- **Logs drawer**: a "logs" link on each container opens a drawer with
  `docker logs --tail 200` (`GET /api/logs?container=…`), refreshed on
  demand, not polled.
- **Docker cleanup**: in the Docker tile, "Remove N volume-only projects
  (size)" and "Prune build cache (size)". The first removes volumes
  labelled with those Compose projects; Docker refuses any volume still in
  use, so nothing attached can be removed. Both confirm with the list and
  sizes first, action types `docker.prune_volumes` and
  `docker.prune_build_cache`, audited.

## Steps

- [x] 1. A2 sampler + history merge, per-checkout log; tests on the log
      round-trip.
- [x] 2. B1 reclaim score in `state`, with table-driven tests; the
      `checkout.stop` action; the Reclaim strip.
- [x] 3. B3 per-checkout history endpoint and card sparklines/deltas.
- [x] 4. B2 forecast, Notify button, settings popover.
- [x] 5. B4: PR chips, logs drawer, docker cleanup actions.
- [x] 6. A1 subcommands: `install-service`, `open`, `setup`, `version`.
      Port the `remote-chrome` launcher logic to Go (`open`), test on
      this Air against clay-aws-devbox with its own tunnel port.
- [x] 7. A3: `install.sh`, Actions workflow, README, LICENSE; `git init`,
      create the public repo with `gh`, push, tag `v0.1.0`, verify the
      release has four binaries plus checksums; run `install.sh` here and
      on the devbox from the real release.
- [x] 8. Replace the transient unit on clay-aws-devbox with
      `devdash install-service` from the released binary; verify after
      `systemctl --user restart`; verify `devdash setup clay-aws-devbox`
      from the Air end to end as the coworker would run it.
- [ ] 9. Update README with anything the real run taught; final report
      with the exact two lines to send your coworker.

## Definition of done

- A fresh Mac with only `ssh <alias>` working can run the two onboarding
  commands and get the dashboard open in a dedicated Chrome window.
- The devbox service survives `systemctl --user restart` and a logout
  (linger), and starts on boot.
- The Reclaim strip ranks a detached stack above a live one and refuses to
  stop a checkout with a working agent (unit test plus a live check on a
  throwaway stack, as in phase 1).
- The forecast line appears while available memory is falling and a
  notification fires when a threshold is crossed (tested by lowering the
  threshold in settings).
- Cards show a 24-hour sparkline once an hour of samples exists.
- `go test ./...` passes in Actions; release `v0.1.0` exists with
  checksums; `install.sh` from the real URL installs on both machines.

## Still deferred

- chezmoi integration (version pin, launchd/systemd units from dotfiles,
  retiring `remote-chrome`), cloud-hil-1 and the work Mac.
- Any model-driven ranking; the reclaim score stays deterministic until
  the logged decisions say otherwise.
