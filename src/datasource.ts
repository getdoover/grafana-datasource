import { DataSourceInstanceSettings, CoreApp, ScopedVars, MetricFindValue } from '@grafana/data';
import { DataSourceWithBackend, getTemplateSrv } from '@grafana/runtime';

import { MyQuery, MyDataSourceOptions, DEFAULT_QUERY } from './types';

export class DataSource extends DataSourceWithBackend<MyQuery, MyDataSourceOptions> {
  constructor(instanceSettings: DataSourceInstanceSettings<MyDataSourceOptions>) {
    super(instanceSettings);
  }

  getDefaultQuery(_: CoreApp): Partial<MyQuery> {
    return DEFAULT_QUERY;
  }

  applyTemplateVariables(query: MyQuery, scopedVars: ScopedVars) {
    const templateSrv = getTemplateSrv();
    return {
      ...query,
      agentId: templateSrv.replace(query.agentId, scopedVars),
      // csv format so multi-value variables interpolate as "1,2,3".
      agentIds: templateSrv.replace(query.agentIds, scopedVars, 'csv'),
      channelName: templateSrv.replace(query.channelName, scopedVars),
      fieldNames: templateSrv.replace(query.fieldNames, scopedVars, 'csv'),
    };
  }

  filterQuery(query: MyQuery): boolean {
    // Don't hit the backend until the query is complete.
    const agent = query.queryType?.startsWith('fleet') ? query.agentIds : query.agentId;
    return !!(agent && query.channelName && query.fieldNames);
  }

  /**
   * Powers "Query" dashboard variables. Syntax:
   *   channels(<agentId>)          — channel names for an agent
   *   fields(<agentId>, <channel>) — field keys for a channel
   * Variables may nest, e.g. fields($agent, tag_values).
   */
  async metricFindQuery(query: string, options?: { scopedVars?: ScopedVars }): Promise<MetricFindValue[]> {
    const interpolated = getTemplateSrv().replace(query.trim(), options?.scopedVars);

    const channels = interpolated.match(/^channels\(\s*(\d+)\s*\)$/);
    if (channels) {
      const opts = await this.getResource('channels', { agentId: channels[1] });
      return opts.map((o: { value: string }) => ({ text: o.value }));
    }

    const fields = interpolated.match(/^fields\(\s*(\d+)\s*,\s*([^)]+?)\s*\)$/);
    if (fields) {
      const opts = await this.getResource('fields', { agentId: fields[1], channel: fields[2] });
      return opts.map((o: { value: string }) => ({ text: o.value }));
    }

    throw new Error(`Unrecognised variable query "${interpolated}". Use channels(<agentId>) or fields(<agentId>, <channel>).`);
  }
}
