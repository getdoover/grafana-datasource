# DEVLOG

Working notes: progress, what's verified vs assumed, and refactors that may fall
out of live testing. Companion to [PLAN.md](./PLAN.md). Newest entries at the top
within each section; keep this honest — it's the checklist for the first real
smoke test.

## Status (2026-07-22)

- **M1 (walking skeleton): code-complete, NOT live-verified.** Auth flow, snowflake
  conversion, timeseries fetch + pagination, frame building, CheckHealth.
- **M2 (real editor): code-complete.** Downsampling to MaxDataPoints (mean +
  min/max envelope), resource handlers + channel/field comboboxes, `latest` query
  type, error mapping, and variable queries via `metricFindQuery` — syntax
  `channels(<agentId>)` and `fields(<agentId>, <channel>)`, nestable with other
  variables. Multi-value variables interpolate as CSV into `agentIds`/`fieldNames`.
- **M3 (fleet): query types code-complete; alerting + annotations not started.**
  `fleet-latest` (multi-agent aggregates → table frame, one row per agent, real
  `last_updated` timestamps) and `fleet-timeseries` (one field across agents, one
  frame/series per agent with an `agent` label, ≤7-day window chunking newest-first
  capped at 5 windows, per-window `next` drain, truncation notices). Timeseries
  queries also emit truncation notices now.
- Dev Grafana runs at **localhost:3005** (3000/3001 are the local doover-data
  proxies). `docker compose up -d`; creds via `DOOVER_ORG_ID` / `DOOVER_CLIENT_ID`
  / `DOOVER_CLIENT_SECRET` env vars, provisioned against **staging**.

## Verified so far (without credentials)

- Unit tests: snowflake ↔ time (vectors cross-generated from pydoover), downsample
  bucketing (means, min/max, sparse fields, empty), payload extraction (flattened
  object, bare scalar, non-numeric drop), aggregate key flattening, per-query error
  responses.
- Grafana 13.1.0 loads the plugin, starts the backend subprocess, provisions the
  datasource, executes CheckHealth ("credentials required" path), and routes
  `resources/channels` → plugin → staging token endpoint (fails at auth as expected
  with empty creds) and 404s unknown resources.
- Both builds + typecheck + eslint clean. `go mod tidy` was required after scaffold
  (upstream go.sum gap) — remember after `create-plugin update` too.

## First smoke test checklist (needs staging OAuth client)

1. `export DOOVER_ORG_ID=… DOOVER_CLIENT_ID=… DOOVER_CLIENT_SECRET=…`, then
   `docker compose up -d --force-recreate`.
2. Datasource "Save & test" → expect "Successfully authenticated".
3. Channel combobox populates for a real agent ID; field combobox populates after
   choosing a channel.
4. Timeseries panel draws with **sane timestamps** (the snowflake math validated
   against production reality — footgun #1 in PLAN.md).
5. Zoom to a >1-week range on a busy channel: confirm data comes back across the
   whole range (tests the both-bounds-window assumption below).
6. `latest` query type returns current values on a stat panel, with the
   aggregate's real `last_updated` timestamp.
7. Panel errors are readable for: bad agent ID (404), unauthorized channel (403).
8. `fleet-latest` with 2-3 agent IDs renders a table (one row per agent).
9. `fleet-timeseries` with 2-3 agents over >7 days: series per agent, window
   chunking works, no boundary duplicates/gaps between windows or pages.
10. Dashboard variable with query `channels(<agentId>)` populates; a second
    variable `fields(<agentId>, $channel)` chains off it; a multi-value `$agent`
    variable drives a fleet query via the Agent IDs input.

## Assumptions pending live verification (each may force a refactor)

- **Wide windows with both bounds set.** doover-data auto-fills a *missing* bound
  to ±1 week, but we assume an explicit `after`+`before` pair spanning more than a
  week is accepted and simply paginates via `next`. If the server instead clamps or
  rejects wide windows, `fetchRange` must chunk into ≤7-day windows —
  `chunkWindows()` already exists (fleet uses it), so the refactor is small.
- **Timeseries `value` shape.** Assumed `{dot.joined.field: value}` objects (from
  the server's `flatten_series`), with bare scalars possible. If the real shape
  differs (e.g. always keyed, or keyed differently for single-field queries),
  `extractRowFromPayload` changes.
- **Multi-agent messages shape.** Modeled on pydoover's `BatchMessageResponse` /
  `Message` (`{results: [{id, channel: {agent_id, name}, data}], count, next}`)
  plus `next_cursors` from the backend source. `Message.data` is assumed raw
  (nested, unflattened) — `lookupPath` handles both nested and flat — and the
  `field_name` projection is assumed to still return full message objects.
- **Fleet pagination uses legacy `next`,** not `next_cursors` (per-agent cursors
  would mean per-agent drain loops). If live testing shows `next` misbehaves for
  multi-agent (it's legacy-compat), refactor `fetchFleetRange` to walk
  `next_cursors` per agent.
- **`next` cursor loop** (single-agent): assumed `before=<next>` re-request
  semantics per `messages_read.rs`; verify no duplicate/missing boundary messages.
- **Org header**: assumed optional when the OAuth client is single-org. If staging
  demands it everywhere, make Organisation ID required in the config editor.

### Resolved (were assumptions, now grounded in pydoover models)

- ~~Aggregate response shape~~: it's `{data, attachments, last_updated?}` (the
  Rust `ChannelAggregate` struct is quoted verbatim in pydoover's
  `models/data/aggregate.py`). `FetchAggregate` parses this; field discovery
  flattens `.data`.
- ~~`latest` timestamp~~: aggregates DO carry `last_updated` (unix ms). `latest`
  and `fleet-latest` rows use it, falling back to evaluation time only when the
  server omits it.

## Potential refactors / known debt

- **Min/max envelope naming**: emitted as `field (min)` / `field (max)` sibling
  series. Nicer: same field name with `data.Field` labels + config so Grafana
  renders proper confidence bands. Revisit when we can see real panels.
- **maxPagesPerQuery=20** (≈30k raw points) is a blunt safety valve; surface a
  frame notice when truncated instead of silently stopping (planned M3 with query
  inspector metadata).
- **Downsample buckets are query-range-relative**, not wall-clock-aligned; cache
  friendliness of the *server* calls is handled by minute-snapping, but bucket
  boundaries shift as the range moves. Aligning buckets to round intervals would
  make refreshes visually stabler.
- **Agent picker** is a plain ID input. Blocked on backend ask #2 (expose agent
  `name` publicly) or wiring the control API as an optional second credential.
- **Discovery failures are silently swallowed** in the editor (combobox just shows
  no options). Fine for private channels; consider surfacing auth errors.
- Scaffold's `go test ./...` also walks a Go file inside `node_modules` (flatted);
  use `go test ./pkg/...`.

## Decisions log

- 2026-07-22 (later): variable queries via legacy `metricFindQuery` string syntax
  rather than `CustomVariableSupport` — far less code, works with the standard
  "Query" variable type; revisit if the syntax needs a real editor.
- 2026-07-22 (later): `fleet-timeseries` enforces exactly one field (the plain
  `/messages` endpoint's field projection only reliably supports one, per backend
  source comments) and paginates via legacy `next`, not `next_cursors` (see
  assumptions).
- 2026-07-22 (later): fleet window walk is newest-first and capped at 5×7-day
  windows — if truncation strikes, panels keep the recent data, which is what
  dashboards care about.

- 2026-07-22: Combobox/MultiCombobox (async options loaders) over deprecated
  Select/MultiSelect — also eliminated the setState-in-effect eslint errors.
- 2026-07-22: agent/org IDs are strings in all frontend/query JSON (snowflakes
  exceed JS safe integers); parsed to uint64 only in Go.
- 2026-07-22: dev Grafana on port 3005; `ports: !override` in docker-compose to
  avoid the doover-data proxies on 3000/3001.
- 2026-07-22: M0 smoke test deferred (no credentials to hand); M2 built against
  backend-source-derived assumptions, itemised above.
