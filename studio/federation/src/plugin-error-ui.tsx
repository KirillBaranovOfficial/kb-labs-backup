/** Safe error UI for plugin load and runtime failures. */

import { useState } from 'react';
import { Button, Typography } from 'antd';
import {
  WarningOutlined, BugOutlined, CopyOutlined, CheckOutlined, ReloadOutlined,
} from '@ant-design/icons';
import type { StudioFailure } from './error-contract.js';

const { Text, Paragraph } = Typography;

interface PluginErrorUIProps {
  type: 'load' | 'crash';
  pluginId: string;
  pageId: string;
  failure: StudioFailure;
  onRetry: () => void;
}

export function PluginErrorUI({ type, pluginId, pageId, failure, onRetry }: PluginErrorUIProps) {
  const [copied, setCopied] = useState(false);
  const isLoad = type === 'load';

  const handleCopy = () => {
    const diagnostics = `${failure.code}\n${failure.correlationId}`;
    navigator.clipboard.writeText(diagnostics).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  };

  return (
    <div style={{
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      minHeight: 320,
      padding: '32px 24px',
    }}>
      <div style={{ maxWidth: 560, width: '100%' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 8 }}>
          {isLoad
            ? <WarningOutlined style={{ fontSize: 18, color: 'var(--warning)', flexShrink: 0 }} />
            : <BugOutlined style={{ fontSize: 18, color: 'var(--error)', flexShrink: 0 }} />
          }
          <Text strong style={{ fontSize: 16 }}>{isLoad ? 'Plugin unavailable' : 'Plugin failed'}</Text>
        </div>

        <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 12, paddingLeft: 32 }}>
          <Text type="secondary" style={{ fontSize: 12, fontFamily: 'monospace' }}>{pluginId}</Text>
          <Text type="secondary" style={{ fontSize: 12 }}>›</Text>
          <Text type="secondary" style={{ fontSize: 12, fontFamily: 'monospace' }}>{pageId}</Text>
        </div>

        <Paragraph type="secondary" style={{ fontSize: 13, marginBottom: 8, paddingLeft: 32 }}>
          {failure.message}
        </Paragraph>
        <Paragraph type="secondary" style={{ fontSize: 13, marginBottom: 16, paddingLeft: 32 }}>
          {failure.hint}
        </Paragraph>
        <div style={{ display: 'flex', gap: 8, paddingLeft: 32, marginBottom: 16 }}>
          <Button size="small" icon={<ReloadOutlined />} onClick={onRetry}>Retry</Button>
          <Button size="small" icon={copied ? <CheckOutlined /> : <CopyOutlined />} onClick={handleCopy}>
            {copied ? 'Copied' : 'Copy diagnostics'}
          </Button>
        </div>
        <div style={{ paddingLeft: 32 }}>
          <Text type="secondary" style={{ fontSize: 12 }}>Code: {failure.code}</Text>
          <br />
          <Text type="secondary" style={{ fontSize: 12 }}>Correlation ID: {failure.correlationId}</Text>
        </div>
      </div>
    </div>
  );
}
