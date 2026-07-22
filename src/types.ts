import { DataSourceJsonData } from '@grafana/data';
import { DataQuery } from '@grafana/schema';

/**
 * Doover IDs (agents, organisations) are snowflakes that exceed JavaScript's
 * safe-integer range, so they are carried as strings everywhere in the frontend
 * and only parsed to integers in the Go backend.
 */
export interface MyQuery extends DataQuery {
  agentId?: string;
  /** Comma-separated agent IDs for the fleet-* query types; supports multi-value variables. */
  agentIds?: string;
  channelName?: string;
  /** Comma-separated field names; dot-joined keys address nested values (a.b.c). */
  fieldNames?: string;
}

export const DEFAULT_QUERY: Partial<MyQuery> = {
  channelName: 'tag_values',
  queryType: 'timeseries',
};

/**
 * These are options configured for each DataSource instance
 */
export interface MyDataSourceOptions extends DataSourceJsonData {
  /** Doover data API base URL, without the /api suffix. */
  baseUrl?: string;
  /** Organisation snowflake ID, sent as X-Doover-Organisation. */
  orgId?: string;
  clientId?: string;
}

/**
 * Value that is used in the backend, but never sent over HTTP to the frontend
 */
export interface MySecureJsonData {
  clientSecret?: string;
}
