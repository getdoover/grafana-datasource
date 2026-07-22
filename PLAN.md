# Doover Grafana Datasource — Project Plan

**Goal:** a dedicated Grafana datasource plugin for Doover — install it in any Grafana, paste M2M credentials, and browse/graph/alert on any agent's channel data directly from `data.doover.com`. No intermediate TSDB, no separate service.

**Repo:** suggest `getdoover/grafana-datasource` (public, ecosystem-facing). Plugin ID: `getdoover-doover-datasource`.

**Companion project (separate, not in this repo):** the TSDB exporter processor for long-horizon history/alerting at fleet scale. This plugin covers direct, ad-hoc, per-agent access; the exporter covers heavy analytics. They share only the M2M credential setup.

---

## 1. Architecture

Grafana **backend datasource plugin** — one plugin zip containing:

- **Go backend** (`grafana-plugin-sdk-go`): runs as a Grafana-managed subprocess. Holds credentials, does OAuth2, snowflake math, window chunking, pagination, downsampling. Enables Grafana **alerting**.
- **React/TS frontend**: config editor + query editor with discovery pickers. Never sees credentials; discovery calls go through backend resource handlers.

Scaffold with `npx @grafana/create-plugin@latest` (datasource + backend). Build: `mage` (Go) + `npm run dev` (frontend), bundled dev Grafana via docker-compose.

### Why backend (not frontend-only, not Infinity)
- CORS is actually wide open on doover-data (`CorsLayer::very_permissive()`, `rest_server/src/main.rs`), so a browser plugin *would* work — but a backend plugin keeps `client_secret` server-side, supports alerting, and hosts the non-trivial query logic (snowflake conversion, chunk/drain, downsampling) in Go rather than per-browser.
- Infinity can't express the snowflake time math (see §3) — usable only for fixed-window smoke tests.

---

## 2. Auth (verified against `rest_server/src/routes/oauth/token.rs`)

- `POST {base}/api/oauth2/token`, grant `client_credentials` **only**, client auth via **HTTP Basic** (`base64(client_id:client_secret)`).
- Token: JWT, **1 hour** (`TOKEN_SESSION_TIMEOUT = 3600s`). Response `{access_token, expires_in, token_type: "Bearer"}`. Scopes in the request are echoed but not encoded in the JWT — authorization is roles/Cedar, not OAuth scopes.
- OAuth clients are created via `POST /api/oauth2/client` `{doover_id, roles}` — **bound to one agent** (`doover_id`) + role strings; needs `Permission::OAuthClientCreate`. Fleet-wide read comes from the roles attached to that client's agent.
- Send **`X-Doover-Organisation: <org_id>`** on data requests — it selects the org's history table (`dd-org-{org_id}-history`) and feeds permission checks. Make org ID a datasource config field.
- No rate limiting server-side; backpressure is Redis cache + DynamoDB cost. Be a polite client anyway (§4 cache snapping).

**Go implementation:** `golang.org/x/oauth2/clientcredentials` with `AuthStyle: AuthStyleInHeader` (Basic), token source cached per datasource instance.

### Config editor fields
| Field | Storage | Notes |
|---|---|---|
| Base URL | jsonData | default `https://data.doover.com`; staging `https://data.staging.udoover.com`; sandbox `https://data.sandbox.udoover.com`. Paths are under `/api` (no version prefix). |
| Organisation ID | jsonData | sent as `X-Doover-Organisation` |
| Client ID | jsonData | |
| Client Secret | **secureJsonData** | |
| Control API URL + credentials (optional) | jsonData/secure | only needed for the agent-name picker (§6) |

`CheckHealth`: fetch token + `GET /api/agents/{probe}/channels` (or just token success if no probe agent configured).

---

## 3. Time handling — the #1 footgun

Server accepts `before`/`after` **only as snowflake u64** (`doover_data_common/src/snowflake.rs`; strings must be decimal u64). It does **not** accept epoch-ms or ISO — and raw epoch-ms is silently misread as a tiny snowflake near 2025-01-01. No error, just wrong data.

```
DOOVER_EPOCH = 1735689600000            // 2025-01-01 UTC, ms
snowflake    = (epoch_ms - DOOVER_EPOCH) << 22
epoch_ms     = (snowflake >> 22) + DOOVER_EPOCH
```

Port of pydoover's `_to_snowflake` / `generate_snowflake_id_at` (`pydoover/pydoover/utils/snowflake.py`). Point timestamps come from each result's `message_id` via the reverse shift. Unit-test both directions against known pydoover values.

**Window rules (server-enforced, `timeseries.rs` / `filters.rs`):**
- At least one of `before`/`after` required → 400 otherwise.
- Missing bound auto-filled to exactly **1 week** from the other.
- Multi-agent endpoint: hard **7-day** max window → 400 beyond.

**Consequence:** the backend must **chunk any Grafana time range into ≤7-day windows** and drain each via pagination.

---

## 4. Query engine (Go backend)

### Primary query type: `timeseries`
Query model: `{agentId, channelName, fieldNames[], queryType}`.

`GET /api/agents/{agent}/channels/{channel}/messages/timeseries?field_name=a&field_name=b&before=&after=&limit=1500`

Verified server behavior (`rest_server/src/routes/channels/messages/timeseries.rs`):
- `limit`: default 50, **max 1500** (400 above). Always send 1500.
- Repeated `field_name` params supported (Vec) — one request serves a multi-field panel.
- Response `{count, results: [{value, message_id}], next}`. `value` is the message flattened with **dot-joined keys** (nested `a.b.c`). `next` is a **snowflake id** (oldest returned) — paginate with `before=<next>` until `next == null`.

Engine loop per query:
1. Snap Grafana `from`/`to` to the minute (see cache note), convert to snowflakes.
2. Split into ≤7-day chunks, newest-first.
3. Per chunk: request limit=1500, follow `next` until drained; concatenate.
4. Downsample to `MaxDataPoints`: bucketed mean + min/max fields (Grafana renders bands). No server aggregation exists — no interval/bucket/step params, confirmed.
5. Emit one wide frame: time (from message_id) + one field per `field_name`.

**Cache friendliness (verified):** responses are Redis-cached keyed on exact `{agent}:{channel}:{before}:{after}:{fields}:{limit}` — TTL 24h (15min when `before` is in the future). Un-snapped ms-precision bounds bust the cache on every refresh. **Snap bounds to the minute** (configurable) so auto-refreshing dashboards hit cache.

**Safety valves:** cap total points fetched per query (config, e.g. 100k) and log/warn in the frame when truncated. Sparse `field_name` over long history triggers 10× over-fetch scans server-side (`messages_read.rs`) — document this; surface elapsed/point counts in query inspector metadata.

### Secondary query types (same plugin, dropdown in editor)
- **`latest`** — `GET .../channels/{name}/aggregate` (supports `field_name` projection). Cheap; powers stat/gauge panels without history walk.
- **`fleet-latest`** — `GET /api/agents/channels/{channel}/aggregates` (multi-agent aggregates): "current value per agent" tables/geomaps.
- **`fleet-timeseries`** — `GET /api/agents/channels/{channel}/messages` with repeated `agent_id`. Server caps: **500 agents**, per-agent limit 1500, **50,000 total msgs**, **7-day window** (400 beyond). Paginate via `next_cursors: {agent_id: oldest_id}` (not the legacy `next`).
- **`annotations`** — alarms endpoints (`.../channels/{name}/alarms`) mapped to Grafana annotation events. (M3+)

### Data gotchas to handle
- **Retention:** per-message DynamoDB TTL; resolution order explicit `ttl` → channel `default_ttl` → org `retention_period_days`; global default **2 years**. Old ranges return empty, not errors.
- **`history_since` clamp:** every read clamps `after` to the channel's `history_since` — data before it is hidden even if present. Silently truncated ranges are *expected behavior*; consider a frame notice when the requested range predates first returned data.

---

## 5. Query editor (React)

- Cascading pickers: **Agent → Channel → Field(s)**, plus query-type selector.
- All discovery through backend **resource handlers** (`CallResource`), so the browser stays credential-free:
  - `resources/agents` → control API `/agents/` (doover-data has no usable public listing: its only agents route is control-plane-authorized, and its public `Agent` model omits `name`/`display_name`). Fallback: manual agent-ID entry so the plugin works without control-API credentials.
  - `resources/channels?agent=` → `GET /api/agents/{id}/channels` (`include_aggregate=false&include_daily_summaries=false` for speed; no pagination server-side — returns all).
  - `resources/fields?agent=&channel=` → fetch channel aggregate, flatten keys dot-joined (matches the timeseries flattening) → field list.
- **Template variables:** expose agents/channels/fields as variable queries so one dashboard serves the fleet (`$agent` in queries). Multi-value `$agent` + fleet-timeseries for overlay panels.

---

## 6. Milestones

**M0 — smoke test (no repo needed, ~half day)**
Infinity or plain curl against **staging** with a real client_credentials pair. Hand-compute two snowflake bounds for a fixed window. Validates: token flow, org header, timeseries response shape, real retention depth on a live channel. (Infinity can't do dynamic time ranges — snowflake math — so this is throwaway validation only.)

**M1 — walking skeleton (few days)**
Scaffold; config editor + secure secret; Go token source; hardcoded-ish query (typed agent/channel/field IDs, no pickers); snowflake conversion + single-window fetch + frame building; CheckHealth. *Exit: a real staging channel graphs correctly in dev Grafana with correct timestamps.*

**M2 — real editor (about a week)**
Chunk/drain engine (≤7-day windows, `next` pagination, 1500/page); minute-snapping; MaxDataPoints downsampling (mean/min/max); resource handlers + cascading pickers; `latest` query type; template variables; error surfaces (400s from window rules mapped to friendly messages).

**M3 — fleet + alerting (about a week)**
`fleet-latest` + `fleet-timeseries` with cap handling; alerting verified end-to-end (backend plugin gives it free, but test window rules under alert evaluation); alarm annotations; query inspector metadata (pages fetched, points, truncation); point-cap safety valve.

**M4 — packaging**
Private plugin signature (Grafana Cloud account) for internal rollout; provisioning example YAML; README with credential-creation walkthrough (`POST /api/oauth2/client`); CI (mage build, golangci-lint, e2e against sandbox). Optional later: community signing + Grafana catalog submission under the Doover name.

**M5 — stretch**
Grafana Live streaming backed by `wss_server` (separate host, Identify + ChannelSubscribe handshake → `message_create` events); dashboard-JSON templates shipped with the plugin; `data_series` endpoint support for v1-era parity if needed.

---

## 7. Asks for the backend team (none blocking)

1. **Bug:** `get_org_retention_secs` (`rest_server/src/state.rs:163-191`) mixes units — Redis-cache branch returns seconds, DDB branch returns raw *days*, fallback double-converts. Message TTLs differ by cache warmth. Read-path unaffected but worth fixing.
2. **Expose `name`/`display_name` on the public `Agent` model** (present in DB model, dropped in serialization) and/or a "list agents visible to this credential" endpoint in doover-data — would remove the control-API dependency for the agent picker.
3. Nice-to-have: accept epoch-ms or ISO on `before`/`after` (tiny change, helps every generic HTTP client; plugin doesn't need it).
4. Future: server-side bucketed aggregation (min/max/avg per interval) — the single biggest lever for long-range dashboard performance; daily summaries don't help (counts + first/last id only, no stats — verified `models/db/message.rs`).
5. Guidance: recommended roles for a **read-only, fleet-wide** OAuth client (client is bound to one `doover_id` + roles; what role set grants org-wide channel read without write?).

## 8. Risks / open items

- **Wrong-time silent failures** — mitigated by snowflake unit tests + M0 validation against known data.
- **Long-range queries are inherently expensive** (raw points, 1500/page, 7-day chunks). Mitigations: snapping, point cap, downsampling; real fix is backend aggregation (ask #4) or the exporter+TSDB for heavy analytics.
- **Control-API dependency for agent names** — optional config; manual ID entry always works; ask #2 removes it.
- **Auth client provisioning UX** — creating OAuth clients requires `OAuthClientCreate` permission; document the flow, consider a `doover` CLI helper later.
- Multi-agent `field_name` note: plain `/messages` list endpoint comments say only one field is really supported there — fleet-timeseries should send a single field per query.

## 9. Key source references

- Snowflake: `pydoover/pydoover/utils/snowflake.py`; `doover-data/doover_data_common/src/snowflake.rs`
- Timeseries route: `doover-data/rest_server/src/routes/channels/messages/timeseries.rs` (+ `filters.rs` for limits/window)
- Read path / scan behavior: `doover-data/ddb_client/src/channels/messages_read.rs`
- Auth: `doover-data/rest_server/src/routes/oauth/token.rs`, `create_client.rs`; org header `doover-data/doover_auth/src/org_header.rs`
- Multi-agent: `doover-data/rest_server/src/routes/agents/multi_agents_messages.rs`
- Python reference client: `pydoover/pydoover/api/data/_sync.py` (+ `_iterators.py` for the 2-day chunk pattern)
- Widget/API precedents: `doover-apps/powerbi-push`, `doover-apps/power-management`
