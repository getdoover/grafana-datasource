# Doover data source for Grafana

Query device data from the [Doover](https://doover.com) IoT platform directly in
Grafana — channel history as timeseries, current values for stat panels, and
fleet-wide queries across many devices at once.

## Features

- **Timeseries** — graph any field of any channel over the dashboard time range,
  with automatic pagination and server-cache-friendly requests. Dense data is
  downsampled to the panel resolution with min/max envelopes.
- **Latest** — the current value of a channel (from its aggregate), stamped with
  the real last-updated time. Ideal for stat and gauge panels.
- **Fleet timeseries** — one field across many devices, one series per device.
- **Fleet latest** — current values across many devices as a table.
- **Discovery** — channel and field pickers populate from your Doover data;
  nested values are addressed with dot-joined keys (`battery.voltage`).
- **Dashboard variables** — populate variables with `channels(<agentId>)` or
  `fields(<agentId>, <channel>)`, and drive fleet queries with multi-value
  variables.
- **Alerting** — backend plugin, so Grafana alert rules work on any query.

## Installation

Install from the Grafana plugin catalog, or download a release from
[GitHub](https://github.com/getdoover/grafana-datasource/releases) and unzip it
into your Grafana plugins directory.

## Configuration

The datasource authenticates with OAuth2 client credentials against the Doover
data API.

| Setting | Description |
|---|---|
| Base URL | Doover data API base URL. Leave empty for `https://data.doover.com` |
| Organisation ID | Your Doover organisation ID (sent as `X-Doover-Organisation`) |
| Client ID / Client Secret | An OAuth2 client-credentials pair for the Doover data API |

To create an OAuth client, you need the `OAuthClientCreate` permission on the
target agent — see the Doover documentation, or contact your Doover
administrator. For dashboards spanning many devices, use a client whose roles
grant read access across your organisation.

## Writing queries

1. Pick a **query type** (Timeseries, Latest, Fleet timeseries, Fleet latest).
2. Enter the **agent ID** (device ID) — or comma-separated IDs / a multi-value
   variable like `$agent` for fleet queries.
3. Pick a **channel** (e.g. `tag_values`) and one or more **fields**. Nested
   values use dot-joined keys: `battery.voltage`.

### Dashboard variables

Create a "Query" variable with one of:

```
channels(1234567890)          # channel names for an agent
fields(1234567890, tag_values) # field keys for a channel
```

Variable references nest: `fields($agent, $channel)`.

## Notes and limits

- Very wide time ranges on dense channels are truncated (a panel warning shows
  when this happens) — narrow the range, or let downsampling work at panel
  resolution.
- Fleet queries are limited by the Doover API to 500 agents per query, and fleet
  timeseries supports one field per query.

## Development

This is a standard Grafana backend datasource plugin (Go + React). See
[PLAN.md](https://github.com/getdoover/grafana-datasource/blob/main/PLAN.md) and
[DEVLOG.md](https://github.com/getdoover/grafana-datasource/blob/main/DEVLOG.md)
for design notes.

```bash
npm install
npm run dev            # frontend, watch mode
mage -v build:linuxARM64  # backend (or buildAll)
docker compose up      # dev Grafana on localhost:3005
go test ./pkg/...      # backend tests
```

The dev Grafana provisions a datasource against Doover staging using the
`DOOVER_ORG_ID`, `DOOVER_CLIENT_ID` and `DOOVER_CLIENT_SECRET` environment
variables.
