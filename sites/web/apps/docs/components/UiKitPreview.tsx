'use client';

import { useEffect, useState } from 'react';
import { ConfigProvider, theme } from 'antd';
import { UIButton, UISegmented, UITabs } from '@kb-labs/sdk/studio';
import { MemoryRouter } from 'react-router-dom';
import './UiKitPreview.css';

const tabs = [
  { key: 'overview', label: 'Overview', children: <p>Overview content</p> },
  { key: 'api', label: 'API', children: <p>API content</p> },
  { key: 'examples', label: 'Examples', children: <p>Examples content</p> },
];

export function UiKitPreview({ name }: { name: 'buttons' | 'tabs' | 'segmented' }) {
  const [isDark, setIsDark] = useState(false);

  useEffect(() => {
    setIsDark(document.documentElement.dataset.theme === 'dark');
  }, []);

  return (
    <div className="uiKitPreview" data-theme={isDark ? 'dark' : 'light'}>
      <div className="uiKitPreview__toolbar">
        <span>Interactive preview</span>
        <button type="button" onClick={() => setIsDark((value) => !value)}>
          {isDark ? 'Dark' : 'Light'} theme
        </button>
      </div>
      <ConfigProvider theme={{ algorithm: isDark ? theme.darkAlgorithm : theme.defaultAlgorithm }}>
        <div className="uiKitPreview__stage">
          {name === 'buttons' && (
            <div className="uiKitPreview__row">
              {(['sm', 'md', 'lg'] as const).map((size) => (
                <UIButton key={size} size={size} variant="primary">Button {size}</UIButton>
              ))}
            </div>
          )}
          {name === 'segmented' && (
            <div className="uiKitPreview__stack">
              {(['sm', 'md', 'lg'] as const).map((size) => (
                <UISegmented key={size} size={size} defaultValue="Design" options={['Design', 'Build', 'Ship']} />
              ))}
            </div>
          )}
          {name === 'tabs' && (
            <MemoryRouter>
              <div className="uiKitPreview__stack">
                <UITabs items={tabs} variant="underline" size="sm" />
                <UITabs items={tabs} variant="card" size="lg" />
              </div>
            </MemoryRouter>
          )}
        </div>
      </ConfigProvider>
    </div>
  );
}
