# Optional Herdr work view in devdash

## Goal

Keep herdr-board as a separate, optional installation. Add a Work view to
devdash that shows board workstreams when available and factual Work in
progress from Herdr otherwise. Keep the existing Resources view and its app
links, fixed ordering, charts, and confirmed resource controls intact.

This is an implementation plan for review, not authorization to change
herdr-board, dotfiles, catalogs, or running agents.

## User experience

- Two top-level views: **Work** and **Resources**. Start on Work when a work
  provider is available; otherwise Resources. Remember the user's selection
  and never switch views because a poll changes availability.
- Work has a compact host-health summary rather than the four full resource
  tiles. Repository filtering and search apply to the work list.
- With board data, group workstreams by the board's supplied sections: Now,
  Follow-up, Parked, Completed/canceled, and Legacy. Keep the latter three
  collapsed initially. Preserve warnings and source labels; don't rederive
  the board's status rules in devdash.
- Workstream rows show name, repository, board-reported stage, and a short
  summary. Expand a stream for its registered tasks. Catalog tasks use their
  real IDs; legacy rows without task structure remain workstream rows.
- Selecting a stream/task opens a detail panel: **Summary**, **Agents**, and
  **Resources**. On narrow screens it stacks below the selection. Show plan
  progress once per plan path, PR/check facts with their observation times,
  board warnings, and explicit checkout mappings. Resources reuses the
  existing checkout cards and app buttons, with independent disclosure keys.
- **Work in progress** groups live Herdr agents by their observed checkout,
  with repository and workspace labels, status, context, and reported PR
  metadata. It is not a task or completion classifier. Agents without a Git
  checkout remain visible with their working directory/workspace.
- Work in progress is the full fallback without board data. With board data,
  it contains live agents not mapped to a declared stream. Do not duplicate
  the board's synthetic unfiled rows as invented workstreams.
- Keep row order, expanded sections, selection, search focus, and detail tab
  stable through polling. Update values in place; newly discovered items
  append. Explicit navigation may reveal a selected row, not reorder others.
- No cleanup recommendations, inferred stop-safety labels, bulk checkout
  stopping, agent-control buttons, or catalog editing.

## Data and refresh contract

- Extend the existing Herdr collector with pane identity and explicit
  collection status/timestamps. Distinguish an empty successful result from
  a missing command, stopped server, malformed response, or failed refresh.
- Auto-detect `herdr-board` on the devdash service's PATH. In a separate
  background loop, run exactly `herdr-board json --no-sweep`, initially and
  every 15 seconds. Bound execution time (10 seconds), output (8 MiB), and
  diagnostics. Never run a shell command constructed from board data.
- Honor the existing `HERDR_SOCKET_PATH` convention so both tools address
  the same Herdr server. Document the service-environment requirement for a
  nondefault socket; do not change another service's environment automatically.
- Parse into a small, versioned devdash work projection, exposed through a
  separate `/api/work` endpoint. HTTP reads only return cached projections;
  no external command runs on the request path. Slow board reads must not
  delay process/memory collection or the existing resource endpoint.
- Retain the last successful board result after a failed read, labeled with
  the failure and original timestamps. Display cache-read time separately
  from board sweep, agent collection, PR observation, and assessment times.
  Missing timestamps are unknown, not fresh. Stale/missing data never proves
  inactivity or completion. Unsupported derived versions are explicit warnings.
- Board owns collection and assessment. Devdash reads its existing cache;
  it never calls `sweep`, forces an assessment, sends intercom messages, or
  writes a workstream catalog. Re-reading the cache does not freshen it.
  If no board pane/background collector is refreshing it, show that limitation
  and leave refresh ownership with herdr-board. No new daemon in this change.
- Join on canonical checkout paths and server-scoped pane IDs, never branch
  labels, agent names, basename matches, or guessed task names. Keep a missing
  resource match explicit. Use live Herdr observations for the fallback and
  distinguish them from older board observations in detail views.
- Drop cleanup records, raw user/assistant messages, tool-call history,
  session-file paths, and credential/config fields from the browser payload.
  Allow only UI-needed identity, progress, PR, observation, and assessment
  summaries. Render all source text as text, and validate outbound URLs.
- Display existing AI assessments only in agent details, clearly marked as
  estimates with timestamps. Do not implement new thresholds or AI-derived
  task grouping. No new TypeSafe key or dependency is required by devdash.

## Implementation steps

- [ ] Add synthetic contract fixtures and parser/projection tests for catalog,
  legacy, zero-agent, unfiled, unknown-version, and malformed board output.
- [ ] Implement bounded optional board collection and enrich Herdr identity,
  freshness, and failure handling. Test timeout/output limits and recovery.
- [ ] Add the cached work endpoint and exact identity joins. Test that raw
  conversations/cleanup data cannot leak through and stale reads stay stale.
- [ ] Add Work/Resources navigation, compact health summary, workstreams,
  search/filter, selection details, and the Herdr-only fallback. Preserve
  Resources behavior and stable UI state across polls.
- [ ] Verify browser behavior with synthetic board+Herdr, Herdr-only,
  neither-installed, empty-board, stale-board, and failing-board scenarios,
  including keyboard use, narrow layouts, and light/dark modes.
- [ ] Update generic README documentation for optional installation, fallback,
  data sources, refresh ownership, privacy, and troubleshooting. Run Go tests,
  race tests, vet, and Node UI tests.
- [ ] Build and deploy devdash only to the existing AWS devbox service for
  acceptance, preserving the previous binary. Verify through the existing
  SSH tunnel. Do not reinstall, restart, or modify Herdr or herdr-board.

## Done means

The installed dashboard presents declared workstreams when board data is
available, real Herdr activity when it is not, and still works normally with
neither integration installed. Nothing invents task membership, masks stale
observations, reorders rows unexpectedly, or triggers board-side actions.
Existing resource controls still require confirmation. The earlier uncommitted
README and reclaim-removal changes are preserved. No commit, push, or release
is included unless separately requested.
