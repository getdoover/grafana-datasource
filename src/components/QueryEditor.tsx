import React, { ChangeEvent } from 'react';
import { Combobox, ComboboxOption, InlineField, Input, MultiCombobox, RadioButtonGroup, Stack } from '@grafana/ui';
import { QueryEditorProps, SelectableValue } from '@grafana/data';
import { DataSource } from '../datasource';
import { MyDataSourceOptions, MyQuery } from '../types';

type Props = QueryEditorProps<DataSource, MyQuery, MyDataSourceOptions>;

const QUERY_TYPES: Array<SelectableValue<string>> = [
  { label: 'Timeseries', value: 'timeseries', description: 'Channel message history over the dashboard time range' },
  { label: 'Latest', value: 'latest', description: 'Current value from the channel aggregate' },
  { label: 'Fleet timeseries', value: 'fleet-timeseries', description: 'One field across many agents, one series each' },
  { label: 'Fleet latest', value: 'fleet-latest', description: 'Current values across many agents, as a table' },
];

const filterByInput = (options: Array<ComboboxOption<string>>, input: string) =>
  input ? options.filter((o) => (o.label ?? o.value).toLowerCase().includes(input.toLowerCase())) : options;

export function QueryEditor({ datasource, query, onChange, onRunQuery }: Props) {
  const { agentId, agentIds, channelName, fieldNames, queryType } = query;

  const isFleet = (queryType ?? '').startsWith('fleet');
  const singleField = queryType === 'fleet-timeseries';
  // Discovery needs some agent to inspect; fleet types use the first listed one.
  const discoveryAgent = isFleet ? (agentIds ?? '').split(',')[0]?.trim() : agentId;

  // Discovery is best-effort: on failure the pickers return no options but both
  // comboboxes accept typed values, so the editor still works when discovery is
  // unavailable (e.g. private channels, or roles without channel-list access).
  const loadChannelOptions = async (input: string): Promise<Array<ComboboxOption<string>>> => {
    if (!discoveryAgent || !/^\d+$/.test(discoveryAgent)) {
      return [];
    }
    const options = await datasource.getResource('channels', { agentId: discoveryAgent }).catch(() => []);
    return filterByInput(options, input);
  };

  const loadFieldOptions = async (input: string): Promise<Array<ComboboxOption<string>>> => {
    if (!discoveryAgent || !/^\d+$/.test(discoveryAgent) || !channelName) {
      return [];
    }
    const options = await datasource
      .getResource('fields', { agentId: discoveryAgent, channel: channelName })
      .catch(() => []);
    return filterByInput(options, input);
  };

  const onInputChange = (key: 'agentId' | 'agentIds') => (event: ChangeEvent<HTMLInputElement>) => {
    onChange({ ...query, [key]: event.target.value });
  };

  const onChannelChange = (option: ComboboxOption<string> | null) => {
    onChange({ ...query, channelName: option?.value ?? '', fieldNames: '' });
    onRunQuery();
  };

  const onFieldsChange = (options: Array<ComboboxOption<string>>) => {
    onChange({ ...query, fieldNames: options.map((o) => o.value).join(',') });
    onRunQuery();
  };

  const onSingleFieldChange = (option: ComboboxOption<string> | null) => {
    onChange({ ...query, fieldNames: option?.value ?? '' });
    onRunQuery();
  };

  const onQueryTypeChange = (value?: string) => {
    onChange({ ...query, queryType: value ?? 'timeseries' });
    onRunQuery();
  };

  const selectedFields = (fieldNames || '')
    .split(',')
    .map((f) => f.trim())
    .filter(Boolean);

  return (
    <Stack gap={0} wrap="wrap">
      <InlineField label="Query type" labelWidth={14}>
        <RadioButtonGroup options={QUERY_TYPES} value={queryType || 'timeseries'} onChange={onQueryTypeChange} />
      </InlineField>
      {isFleet ? (
        <InlineField
          label="Agent IDs"
          labelWidth={12}
          tooltip="Comma-separated Doover agent IDs, or a multi-value variable like $agent (max 500)"
        >
          <Input
            id="query-editor-agent-ids"
            onChange={onInputChange('agentIds')}
            onBlur={onRunQuery}
            value={agentIds || ''}
            required
            placeholder="123, 456 or $agent"
            width={30}
          />
        </InlineField>
      ) : (
        <InlineField label="Agent ID" labelWidth={12} tooltip="Doover agent (device) ID">
          <Input
            id="query-editor-agent-id"
            onChange={onInputChange('agentId')}
            onBlur={onRunQuery}
            value={agentId || ''}
            required
            placeholder="Agent snowflake ID"
            width={26}
          />
        </InlineField>
      )}
      <InlineField label="Channel" labelWidth={12} tooltip="Channel name, e.g. tag_values">
        <Combobox
          id="query-editor-channel"
          options={loadChannelOptions}
          value={channelName || null}
          onChange={onChannelChange}
          createCustomValue
          placeholder="tag_values"
          width={28}
        />
      </InlineField>
      <InlineField
        label={singleField ? 'Field' : 'Fields'}
        labelWidth={10}
        grow
        tooltip={
          singleField
            ? 'Fleet timeseries supports one field per query; dot-joined keys address nested values'
            : 'Field names; dot-joined keys address nested values (e.g. battery.voltage)'
        }
      >
        {singleField ? (
          <Combobox
            id="query-editor-field"
            options={loadFieldOptions}
            value={selectedFields[0] ?? null}
            onChange={onSingleFieldChange}
            createCustomValue
            placeholder="Select or type a field name"
          />
        ) : (
          <MultiCombobox
            id="query-editor-fields"
            options={loadFieldOptions}
            value={selectedFields}
            onChange={onFieldsChange}
            createCustomValue
            placeholder="Select or type field names"
          />
        )}
      </InlineField>
    </Stack>
  );
}
