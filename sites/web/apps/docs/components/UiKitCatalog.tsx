'use client';

import * as React from 'react';
import { useEffect, useMemo, useState } from 'react';
import { ConfigProvider, theme } from 'antd';
import { Check, Copy, House, Search, Settings, ShieldCheck } from 'lucide-react';
import { uiKitCatalog } from '@kb-labs/sdk/studio/catalog';
import * as UIKit from '@kb-labs/studio-ui-kit';
import { MemoryRouter } from 'react-router-dom';
import { HighlightedCode } from './HighlightedCode';
import { useTheme } from './ThemeProvider';
import './UiKitPreview.css';

const categoryTitles: Record<string, string> = {
  primitives: 'Primitives', core: 'Core', layout: 'Layout', form: 'Forms',
  navigation: 'Navigation', composite: 'Composite', feedback: 'Feedback',
  overlay: 'Overlays', content: 'Content',
};

function componentSlug(name: string): string {
  return name.replace(/^UI/, '').replace(/([a-z0-9])([A-Z])/g, '$1-$2').toLowerCase();
}

export function UiKitCategories() {
  const categories = [...new Set(uiKitCatalog.map((component) => component.category))];
  return (
    <ul className="uiKitDocsList">
      {categories.map((category) => <li key={category}><a href={`/sdk/plugin-api/studio-${category}`}><span className="uiKitDocsList__name">{categoryTitles[category] ?? category}</span><span className="uiKitDocsList__count">{uiKitCatalog.filter((component) => component.category === category).length}</span></a></li>)}
    </ul>
  );
}

/** Category landing: a standard documentation list, with full pages nested below it. */
export function UiKitCatalog({ category }: { category?: string }) {
  const components = uiKitCatalog.filter((component) => !category || component.category === category);
  return (
    <ul className="uiKitDocsList">
      {components.map((component) => (
        <li key={component.name}>
          <a href={`/sdk/plugin-api/studio-${component.category}/${componentSlug(component.name)}`}>
            <span className="uiKitDocsList__name">{component.name}</span><span className="uiKitDocsList__description">{component.description}</span>
          </a>
        </li>
      ))}
    </ul>
  );
}

type PropManifest = {
  name: string;
  type: string;
  required: boolean;
  description: string;
  kind: 'string' | 'number' | 'boolean' | 'enum' | 'json' | 'node' | 'callback';
  options: readonly string[];
};
type Manifest = { name: string; category: string; description: string; props: readonly PropManifest[]; examples: readonly string[]; args: Readonly<Record<string, unknown>> };

export function UiKitComponentPage({ component }: { component: Manifest }) {
  const { theme: docsTheme } = useTheme();
  const [isDark, setIsDark] = useState(false);
  const [copied, setCopied] = useState(false);
  const [showProps, setShowProps] = useState(false);
  useEffect(() => setIsDark(docsTheme === 'dark'), [docsTheme]);
  const editableProps = component.props;
  const hasEditableProps = editableProps.some((prop) => prop.kind !== 'callback' || prop.name === 'children');
  const [values, setValues] = useState<Record<string, unknown>>(() => initialProps(editableProps, component.args));
  const [showCode, setShowCode] = useState(false);
  const code = useMemo(() => componentCode(component.name, editableProps, values), [component.name, editableProps, values]);
  const copyCode = async () => {
    try {
      await navigator.clipboard?.writeText(code);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  };

  return (
    <div className="uiKitPlayground" data-theme={isDark ? 'dark' : 'light'}>
      <div className="uiKitPlayground__surface">
        <header className="uiKitPlayground__toolbar">
          <div className="uiKitPlayground__viewControls" role="tablist" aria-label="Playground view">
            <button type="button" role="tab" aria-selected={!showCode} onClick={() => setShowCode(false)}>Preview</button>
            <button type="button" role="tab" aria-selected={showCode} onClick={() => setShowCode(true)}>Code</button>
          </div>
          <div className="uiKitPlayground__toolbarActions">
            {hasEditableProps && <button type="button" aria-expanded={showProps} aria-pressed={showProps} onClick={() => setShowProps((open) => !open)}>Props</button>}
            <button type="button" aria-label={`Switch to ${isDark ? 'light' : 'dark'} theme`} onClick={() => setIsDark((value) => !value)}>{isDark ? 'Dark' : 'Light'}</button>
          </div>
        </header>
        <div className="uiKitPlayground__workspace" data-with-props={showProps}>
          <section className="uiKitPlayground__canvas">
            <ConfigProvider theme={{ algorithm: isDark ? theme.darkAlgorithm : theme.defaultAlgorithm }}>
              <MemoryRouter>
                <div className="uiKitPlayground__stage" data-theme={isDark ? 'dark' : 'light'}>
                  {showCode ? (
                    <div className="uiKitPlayground__codePanel">
                      <button className="uiKitPlayground__copyCode" type="button" aria-label={copied ? 'Code copied' : 'Copy code'} title={copied ? 'Copied' : 'Copy code'} onClick={() => {
                        void copyCode();
                      }}>{copied ? <Check size={15} /> : <Copy size={15} />}</button>
                      <HighlightedCode code={code} />
                    </div>
                  ) : <ComponentRenderer name={component.name} values={values} props={editableProps} onChange={(name, value) => setValues((current) => ({ ...current, [name]: value }))} />}
                </div>
              </MemoryRouter>
            </ConfigProvider>
          </section>
          {showProps && (
            <aside className="uiKitPlayground__editor" aria-label="Component props editor">
              <header><strong>Props</strong><span>Changes update preview and code</span></header>
              <div className="uiKitPlayground__fields">
                {editableProps.filter((prop) => prop.kind !== 'callback' && prop.name !== 'children').map((prop) => (
                  <PropEditor key={prop.name} prop={prop} value={values[prop.name]} onChange={(value) => setValues((current) => ({ ...current, [prop.name]: value }))} />
                ))}
                {editableProps.some((prop) => prop.name === 'children') && (
                  <label className="uiKitPlayground__field"><span>children</span><textarea value={String(values.children ?? '')} onChange={(event) => setValues((current) => ({ ...current, children: event.target.value }))} rows={3} /></label>
                )}
              </div>
            </aside>
          )}
        </div>
      </div>
    </div>
  );
}

function initialProps(props: readonly PropManifest[], args: Readonly<Record<string, unknown>>): Record<string, unknown> {
  const supported = new Set(props.map((prop) => prop.name));
  return Object.fromEntries(Object.entries(args)
    .filter(([name]) => supported.has(name))
    .map(([name, value]) => [name, structuredClone(value)]));
}

function PropEditor({ prop, value, onChange }: { prop: PropManifest; value: unknown; onChange: (value: unknown) => void }) {
  return (
    <label className="uiKitPlayground__field">
      <span>{prop.name}{prop.required && <i>required</i>}</span>
      {prop.description && <small>{prop.description}</small>}
      {renderPropInput(prop, value, onChange)}
    </label>
  );
}

function renderPropInput(prop: PropManifest, value: unknown, onChange: (value: unknown) => void) {
  switch (prop.kind) {
    case 'enum': return <select value={String(value ?? '')} onChange={(event) => onChange(event.target.value)}>{prop.options.map((option) => <option key={option}>{option}</option>)}</select>;
    case 'boolean': return <input type="checkbox" checked={Boolean(value)} onChange={(event) => onChange(event.target.checked)} />;
    case 'number': return <input type="number" value={String(value ?? '')} onChange={(event) => onChange(Number(event.target.value))} />;
    case 'json': return <textarea value={JSON.stringify(value ?? {}, null, 2)} rows={4} onChange={(event) => {
      try { onChange(JSON.parse(event.target.value)); } catch { /* Keep the last valid value until JSON is fixed. */ }
    }} />;
    default: return <input value={String(value ?? '')} onChange={(event) => onChange(event.target.value)} />;
  }
}

function componentCode(name: string, props: readonly PropManifest[], values: Record<string, unknown>): string {
  if (name === 'UIDrawer') {
    const title = String(values.title ?? 'Drawer title');
    const placement = String(values.placement ?? 'right');
    const width = typeof values.width === 'number' ? values.width : 360;
    const children = String(values.children ?? 'Drawer content');
    return `import { useState } from 'react';
import { UIButton, UIDrawer } from '@kb-labs/sdk/studio';

function DrawerExample() {
  const [open, setOpen] = useState(false);
  return <>
    <UIButton onClick={() => setOpen(true)}>Open drawer</UIButton>
    <UIDrawer open={open} onClose={() => setOpen(false)} title=${JSON.stringify(title)} placement=${JSON.stringify(placement)} width={${width}}>${children}</UIDrawer>
  </>;
}`;
  }
  const template = componentCodeTemplates[name];
  if (template) return template;
  const attrs = props.filter((prop) => prop.name !== 'children' && (values[prop.name] !== undefined || prop.required && prop.kind === 'callback'))
    .map((prop) => {
      const value = values[prop.name];
      if (prop.kind === 'callback' && value === undefined) return `${prop.name}={() => {}}`;
      switch (typeof value) {
        case 'boolean': return value ? prop.name : '';
        case 'number': return `${prop.name}={${value}}`;
        case 'string': return prop.options.length > 0 && prop.options.every((option) => /^\d+(?:\.\d+)?$/.test(option))
          ? `${prop.name}={${Number(value)}}`
          : `${prop.name}=${JSON.stringify(value)}`;
        case 'function': return `${prop.name}={() => {}}`;
        default: return `${prop.name}={${JSON.stringify(value)}}`;
      }
    }).filter(Boolean);
  const children = values.children;
  const opening = `<${name}${attrs.length ? ` ${attrs.join(' ')}` : ''}`;
  const element = children ? `${opening}>${String(children)}</${name}>` : `${opening} />`;
  return `import { ${name} } from '@kb-labs/sdk/studio';\n\n${element}`;
}

const componentCodeTemplates: Record<string, string> = {
  UITypography: `import { UITypographyText, UITypographyParagraph, UITypographyLink } from '@kb-labs/sdk/studio';

<>
  <UITypographyText strong>Typography text</UITypographyText>
  <UITypographyText type="secondary">Secondary text for supporting information</UITypographyText>
  <UITypographyText code>const status = "ready";</UITypographyText>
  <UITypographyParagraph>Paragraphs handle longer descriptions with the same typography system.</UITypographyParagraph>
  <UITypographyLink href="#typography-example">A typography link</UITypographyLink>
</>`,
};

function ComponentRenderer({ name, values, props, onChange }: { name: string; values: Record<string, unknown>; props: readonly PropManifest[]; onChange: (name: string, value: unknown) => void }) {
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  useEffect(() => {
    UIKit.registerIconRenderer((iconName) => {
      const Icon = documentationIcons[iconName];
      return Icon ? <Icon size={18} aria-hidden="true" /> : null;
    });
  }, []);
  if (!mounted) return <div className="uiKitPlayground__placeholder">Loading preview…</div>;
  const Preview = previewRenderers[name];
  if (Preview) return <div className="uiKitPlayground__component"><Preview values={values} onChange={onChange} /></div>;
  const candidate = UIKit[name as keyof typeof UIKit];
  const isComponent = typeof candidate === 'function'
    || (typeof candidate === 'object' && candidate !== null && '$$typeof' in candidate);
  if (!isComponent) return <p>This component has no standalone preview.</p>;
  const Component = candidate as React.ElementType;
  return <div className="uiKitPlayground__component"><PreviewBoundary><UIKitComponent component={Component} values={values} props={props} /></PreviewBoundary></div>;
}

type PreviewRendererProps = { values: Record<string, unknown>; onChange: (name: string, value: unknown) => void };

const previewRenderers: Record<string, React.ComponentType<PreviewRendererProps>> = {
  UITypography: TypographyPreview,
  UIDrawer: DrawerPreview,
};

const documentationIcons: Readonly<Record<string, React.ComponentType<{ size?: number }>>> = {
  HomeOutlined: House,
  SearchOutlined: Search,
  SettingOutlined: Settings,
  SafetyOutlined: ShieldCheck,
};

function TypographyPreview() {
  const { UITypographyText, UITypographyParagraph, UITypographyLink } = UIKit;
  return (
    <div className="uiKitPlayground__typographyDemo">
      <UITypographyText strong>Typography text</UITypographyText>
      <UITypographyText type="secondary">Secondary text for supporting information</UITypographyText>
      <UITypographyText code>const status = &quot;ready&quot;;</UITypographyText>
      <UITypographyParagraph style={{ marginBottom: 0 }}>
        Paragraphs handle longer descriptions with the same typography system.
      </UITypographyParagraph>
      <UITypographyLink href="#typography-example">A typography link</UITypographyLink>
    </div>
  );
}

function DrawerPreview({ values, onChange }: PreviewRendererProps) {
  const [internalOpen, setInternalOpen] = useState(false);
  const { UIButton, UIDrawer } = UIKit;
  const isControlled = values.open !== undefined;
  const open = isControlled ? Boolean(values.open) : internalOpen;
  const setOpen = (next: boolean) => {
    if (isControlled) onChange('open', next);
    else setInternalOpen(next);
  };

  return (
    <>
      <UIButton onClick={() => setOpen(true)}>Open drawer</UIButton>
      <UIDrawer
        open={open}
        onClose={() => setOpen(false)}
        title={String(values.title ?? 'Drawer title')}
        placement={String(values.placement ?? 'right') as 'right' | 'left' | 'top' | 'bottom'}
        width={typeof values.width === 'number' ? values.width : 360}
      >
        {String(values.children ?? 'Drawer content')}
      </UIDrawer>
    </>
  );
}

class PreviewBoundary extends React.Component<React.PropsWithChildren, { error: boolean }> {
  state = { error: false };
  static getDerivedStateFromError() { return { error: true }; }
  render() { return this.state.error ? <p role="alert">This prop combination cannot be rendered. Adjust the props to continue.</p> : this.props.children; }
}

function UIKitComponent({ component, values, props }: { component: React.ElementType; values: Record<string, unknown>; props: readonly PropManifest[] }) {
  const Component = component;
  const componentValues = { ...values };
  for (const prop of props) {
    if (prop.required && prop.kind === 'callback' && componentValues[prop.name] === undefined) {
      componentValues[prop.name] = () => undefined;
    }
  }
  return React.createElement(Component, componentValues);
}
