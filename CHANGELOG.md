# Changelog

## 1.0.0 (Unreleased)

Initial release.

- Timeseries queries over channel message history, with pagination and
  downsampling to panel resolution (mean + min/max envelope).
- Latest-value queries from channel aggregates, stamped with `last_updated`.
- Fleet queries: one field across many agents (series per agent), and
  current-values-as-table across many agents.
- Channel and field discovery pickers; dashboard variable queries
  (`channels(<agentId>)`, `fields(<agentId>, <channel>)`).
- OAuth2 client-credentials authentication against the Doover data API.
