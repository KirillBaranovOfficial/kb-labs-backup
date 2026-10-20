# @kb-labs/studio-ui-kit

Professional React UI components using Ant Design and design tokens from `@kb-labs/studio-ui-core`.

## 🎯 Key Principles

- ✅ **NO hardcoded colors** - All colors from Ant Design tokens via `theme.useToken()`
- ✅ **NO inline styles** - All spacing/typography from design tokens
- ✅ **Type-safe** - Full TypeScript support
- ✅ **Composable** - Build complex components from primitives

## 📦 Installation

```bash
pnpm add @kb-labs/studio-ui-kit
```

## 🚀 Usage

```tsx
import { UIText, UIBox, UIFlex, UIMetricCard, UIBadge } from '@kb-labs/studio-ui-kit';

function MyComponent() {
  return (
    <UIBox p={4}>
      <UIText size="2xl" weight="bold">Hello World</UIText>

      <UIFlex gap={2} align="center">
        <UIBadge variant="success">Active</UIBadge>
        <UIText size="sm" color="secondary">User is online</UIText>
      </UIFlex>

      <UIMetricCard
        label="Active Users"
        value={1234}
        trend="up"
        delta={15.3}
        unit="users"
      />
    </UIBox>
  );
}
```

## Typed form builder

Use `UIFormBuilder` for common forms whose fields, validation, and visibility rules can be described as data. Keep `UIForm` for bespoke layouts. Pair the builder with `UIModal` by placing a submit button in the modal footer and linking it to the form with its `id`.

```tsx
import * as React from 'react';
import {
  UIButton,
  UIFormBuilder,
  type UIFormDefinition,
  UIModal,
} from '@kb-labs/studio-ui-kit';

type ProjectValues = {
  name: string;
  scope: 'user' | 'workspace';
  path?: string;
};

const projectForm: UIFormDefinition<ProjectValues> = {
  sections: [{
    title: 'Project',
    fields: [
      { name: 'name', type: 'text', label: 'Name', required: true },
      {
        name: 'scope',
        type: 'select',
        label: 'Scope',
        required: true,
        options: [
          { label: 'Personal', value: 'user' },
          { label: 'Workspace', value: 'workspace' },
        ],
      },
      {
        name: 'path',
        type: 'text',
        label: 'Workspace path',
        visibleWhen: { field: 'scope', op: 'equals', value: 'workspace' },
        requiredWhen: { field: 'scope', op: 'equals', value: 'workspace' },
      },
    ],
  }],
};

export function CreateProjectDialog() {
  const [open, setOpen] = React.useState(true);
  const [submitting, setSubmitting] = React.useState(false);

  return (
    <UIModal
      open={open}
      title="Create project"
      description="Choose where the project belongs."
      onCancel={() => setOpen(false)}
      footer={(
        <>
          <UIButton onClick={() => setOpen(false)}>Cancel</UIButton>
          <UIButton form="project-form" htmlType="submit" loading={submitting} variant="primary">Create project</UIButton>
        </>
      )}
    >
      <UIFormBuilder
        id="project-form"
        definition={projectForm}
        onSubmittingChange={setSubmitting}
        onSubmit={(values) => console.info('Project values', values)}
      />
    </UIModal>
  );
}
```

Field rules use Ant Design Form validation rules. `visibleWhen`, `disabledWhen`, and `requiredWhen` support `equals`, `notEquals`, `in`, `notIn`, `truthy`, and `falsy` conditions. Use a `custom` field when a control needs its own React rendering.

<!-- AUTO-GENERATED:START -->

## 📊 Component Stats

- **Total Components:** 50
- **With Tests:** 6/50 (12%)

**By Category:**

- Primitives: 4
- Core: 9
- Layout: 1
- Form: 11
- Navigation: 7
- Composite: 4
- Feedback: 6
- Overlay: 5
- Content: 3

## 📚 Component Reference

### Primitives

Foundation components for layout and typography

| Component | Description | Tests |
|-----------|-------------|-------|
| **UIBox** | Box component - Layout primitive with spacing tokens | ❌ |
| **UIFlex** | Flex component - Flexbox layout primitive | ❌ |
| **UIText** | Text component - Typography primitive with design tokens | ✅ |
| **UITypography** | UITypography components - Rich text display | ❌ |

### Core

Essential UI components

| Component | Description | Tests |
|-----------|-------------|-------|
| **UIAvatar** | UIAvatar component - User avatar | ❌ |
| **UIBadge** | Badge component - Status indicator with semantic colors | ❌ |
| **UIButton** | UIButton component - Action button with semantic variants | ✅ |
| **UICard** | UICard component - Card container with header/footer | ❌ |
| **UICopyButton** | UICopyButton — one-click copy with built-in visual feedback. | ❌ |
| **UIIcon** | UIIcon component - Icon wrapper with proper theming | ❌ |
| **UIShimmerText** | UIShimmerText - Animated gradient shimmer on text | ❌ |
| **UITag** | UITag component - Label/category tag with semantic colors | ✅ |
| **UITitle** | UITitle component - Page/section titles with hierarchy | ❌ |

### Layout

Layout and container components

| Component | Description | Tests |
|-----------|-------------|-------|
| **UIDivider** | UIDivider component - Visual separator | ❌ |

### Form

Form controls and inputs

| Component | Description | Tests |
|-----------|-------------|-------|
| **UICheckbox** | UICheckbox component - Checkbox input | ❌ |
| **UIDatePicker** | UIDatePicker component - Date/time picker | ❌ |
| **UIForm** | UIForm component - Form wrapper with validation | ❌ |
| **UIFormBuilder** | UIFormBuilder - typed declarative forms for common Studio workflows. | ❌ |
| **UIInput** | UIInput component - Text input with variants | ✅ |
| **UIInputNumber** | UIInputNumber component - Numeric input | ❌ |
| **UIRadio** | UIRadio component - Radio button | ❌ |
| **UISelect** | UISelect component - Dropdown select with icon support | ❌ |
| **UISlider** | UISlider component - Slider input | ❌ |
| **UISwitch** | UISwitch component - Toggle switch | ❌ |
| **UIUpload** | UIUpload component - File upload | ❌ |

### Navigation

Navigation and menu components

| Component | Description | Tests |
|-----------|-------------|-------|
| **UIAccordion** | UIAccordion component - Collapsible panels | ❌ |
| **UIBreadcrumb** | UIBreadcrumb component - Navigation breadcrumb trail | ❌ |
| **UIDropdown** | UIDropdown component - Dropdown menu | ❌ |
| **UIMenu** | UIMenu component - Navigation menu | ❌ |
| **UISegmented** | UISegmented component - Segmented control | ❌ |
| **UISteps** | UISteps component - Step indicator | ❌ |
| **UITabs** | UITabs component - Tabbed navigation | ❌ |

### Composite

Complex composed components

| Component | Description | Tests |
|-----------|-------------|-------|
| **UIHeader** | UIHeader component - Page header with title, subtitle, and breadcrumbs | ✅ |
| **UIPage** | UIPage component - Page container with padding and max-width | ❌ |
| **UIPageHeader** | UIPageHeader component - Standard page header | ❌ |
| **UIPageSection** | UIPageSection component - Section within a page | ❌ |

### Feedback

Feedback and notification components

| Component | Description | Tests |
|-----------|-------------|-------|
| **UIAlert** | UIAlert component - Alert messages and notifications | ❌ |
| **UIEmptyState** | UIEmptyState component - Empty state placeholder | ❌ |
| **UIErrorState** | UIErrorState component - Error state display | ❌ |
| **UIResult** | UIResult component - Result page | ❌ |
| **UISkeleton** | UISkeleton component - Loading skeleton | ✅ |
| **UISpin** | UISpin | ❌ |

### Overlay

Modal and overlay components

| Component | Description | Tests |
|-----------|-------------|-------|
| **UIDrawer** | UIDrawer component - Side panel overlay | ❌ |
| **UIModal** | UIModal component - Modal dialog | ❌ |
| **UIPopconfirm** | UIPopconfirm component - Confirmation popover | ❌ |
| **UIPopover** | UIPopover component - Popover overlay | ❌ |
| **UITooltip** | UITooltip component - Tooltip overlay | ❌ |

### Content

Content display components

| Component | Description | Tests |
|-----------|-------------|-------|
| **UIDiffViewer** | UIDiffViewer | ❌ |
| **UIJsonViewer** | UIJsonViewer component - JSON content viewer | ❌ |
| **UIMarkdownViewer** | @demoArgs {"content":"# Architecture note\n\nThe **gateway** validates access tokens before routing requests.\n\n- REST API\n- MCP API"} | ❌ |

<!-- AUTO-GENERATED:END -->

## 🎨 Design Tokens

### Component sizing

Common controls, tables, accordions, and tabs use the shared `sm` / `md` / `lg` size scale. Existing Ant Design names (`small`, `middle`, `large`; plus `default` where supported) remain valid during migration. Text and icon sizes keep their finer typographic scales; controls with a native two-size limit retain that limit.

```tsx
<UIButton size="sm">Compact</UIButton>
<UIInput size="md" placeholder="Standard control" />
<UISelect size="lg" options={options} />
<UICard size="lg" title="Roomier panel">...</UICard>
```

### Tabs and segmented choices

Use `UITabs` for page or panel navigation. `variant="underline"` is the default section-navigation style; `variant="card"` gives each tab a framed surface. Use `UISegmented` when the user is choosing one of a few short modes, such as a time range.

```tsx
<UITabs variant="underline" size="md" items={pageTabs} />
<UITabs variant="card" size="sm" items={panelTabs} />
<UISegmented size="sm" options={['1 min', '5 min', '30 min']} />
```

All components use design tokens from `@kb-labs/studio-ui-core`:

### Typography
```tsx
<UIText size="xs">Extra small text</UIText>
<UIText size="sm">Small text</UIText>
<UIText size="md">Base text</UIText>
<UIText size="base">Base text</UIText>
<UIText size="lg">Large text</UIText>
<UIText size="xl">Extra large text</UIText>
<UIText size="2xl">2X large text</UIText>
```

### Colors
```tsx
<UIText color="primary">Primary text</UIText>
<UIText color="secondary">Secondary text</UIText>
<UIText color="success">Success text</UIText>
<UIText color="warning">Warning text</UIText>
<UIText color="error">Error text</UIText>
<UIText color="info">Info text</UIText>
```

### Spacing
```tsx
<UIBox p={2}>Padding 0.5rem</UIBox>
<UIBox p={4}>Padding 1rem</UIBox>
<UIBox p={6}>Padding 1.5rem</UIBox>
<UIBox m={2}>Margin 0.5rem</UIBox>
```

## 🏗️ Component Hierarchy

```
Primitives (Tier 1)
  ├── UIText - Typography
  ├── UIBox - Spacing
  └── UIFlex - Layout

Components (Tier 2)
  ├── UIBadge - Status indicators
  └── UIMetricCard - Metric displays
```

## 📖 Examples

### Metric Display
```tsx
<UIMetricCard
  label="Revenue"
  value={45230}
  unit="USD"
  trend="up"
  delta={12.5}
  showDelta={true}
  size="default"
/>
```

### Badge Variants
```tsx
<UIBadge variant="success">Success</UIBadge>
<UIBadge variant="warning">Warning</UIBadge>
<UIBadge variant="error">Error</UIBadge>
<UIBadge variant="info">Info</UIBadge>
<UIBadge variant="default">Default</UIBadge>
```

### Layout Composition
```tsx
<UIBox p={4} mb={3}>
  <UIFlex direction="column" gap={2}>
    <UIText size="lg" weight="bold">Header</UIText>
    <UIText size="sm" color="secondary">Description</UIText>
  </UIFlex>
</UIBox>
```

## 🔗 Dependencies

- `react` (peer) - ^18.3.1
- `react-dom` (peer) - ^18.3.1
- `antd` (peer) - ^5.22.5
- `@kb-labs/studio-ui-core` - Design tokens

## 📝 License

Private - KB Labs
