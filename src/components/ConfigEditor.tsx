import React, { ChangeEvent } from 'react';
import { InlineField, Input, SecretInput } from '@grafana/ui';
import { DataSourcePluginOptionsEditorProps } from '@grafana/data';
import { MyDataSourceOptions, MySecureJsonData } from '../types';

interface Props extends DataSourcePluginOptionsEditorProps<MyDataSourceOptions, MySecureJsonData> {}

export function ConfigEditor(props: Props) {
  const { onOptionsChange, options } = props;
  const { jsonData, secureJsonFields, secureJsonData } = options;

  const onJsonDataChange = (key: keyof MyDataSourceOptions) => (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...jsonData,
        [key]: event.target.value,
      },
    });
  };

  // Secure field (only sent to the backend)
  const onClientSecretChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({
      ...options,
      secureJsonData: {
        clientSecret: event.target.value,
      },
    });
  };

  const onResetClientSecret = () => {
    onOptionsChange({
      ...options,
      secureJsonFields: {
        ...options.secureJsonFields,
        clientSecret: false,
      },
      secureJsonData: {
        ...options.secureJsonData,
        clientSecret: '',
      },
    });
  };

  return (
    <>
      <InlineField
        label="Base URL"
        labelWidth={20}
        interactive
        tooltip={'Doover data API base URL, without the /api suffix. Leave empty for https://data.doover.com'}
      >
        <Input
          id="config-editor-base-url"
          onChange={onJsonDataChange('baseUrl')}
          value={jsonData.baseUrl}
          placeholder="https://data.doover.com"
          width={40}
        />
      </InlineField>
      <InlineField
        label="Organisation ID"
        labelWidth={20}
        interactive
        tooltip={'Doover organisation ID, sent as the X-Doover-Organisation header'}
      >
        <Input
          id="config-editor-org-id"
          onChange={onJsonDataChange('orgId')}
          value={jsonData.orgId}
          placeholder="Organisation snowflake ID"
          width={40}
        />
      </InlineField>
      <InlineField label="Client ID" labelWidth={20} interactive tooltip={'OAuth2 client ID (client-credentials grant)'}>
        <Input
          id="config-editor-client-id"
          onChange={onJsonDataChange('clientId')}
          value={jsonData.clientId}
          placeholder="OAuth2 client ID"
          width={40}
        />
      </InlineField>
      <InlineField label="Client Secret" labelWidth={20} interactive tooltip={'OAuth2 client secret (stored securely)'}>
        <SecretInput
          required
          id="config-editor-client-secret"
          isConfigured={secureJsonFields.clientSecret}
          value={secureJsonData?.clientSecret}
          placeholder="OAuth2 client secret"
          width={40}
          onReset={onResetClientSecret}
          onChange={onClientSecretChange}
        />
      </InlineField>
    </>
  );
}
