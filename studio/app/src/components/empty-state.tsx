import * as React from 'react';
import { UIEmptyState as UIEmptyStateBase, UIButton } from '@kb-labs/studio-ui-kit';

export interface EmptyStateProps {
  kind: EmptyStateKind;
  icon?: React.ReactNode;
  title: string;
  description: string;
  action?: { label: string; onClick: () => void };
}

export type EmptyStateKind = 'empty' | 'loading' | 'failed' | 'unconfigured' | 'unavailable';

export function EmptyState({ kind, icon, title, description, action }: EmptyStateProps) {
  // Convert icon component to React element if it's a component
  const iconElement = React.isValidElement(icon)
    ? icon
    : icon && typeof icon === 'function'
    ? React.createElement(icon as React.ComponentType)
    : icon;

  return (
    <section data-state={kind} aria-label={`${kind} state`}>
      <UIEmptyStateBase
        icon={iconElement}
        title={title}
        description={description}
        action={action ? <UIButton onClick={action.onClick}>{action.label}</UIButton> : undefined}
      />
    </section>
  );
}
