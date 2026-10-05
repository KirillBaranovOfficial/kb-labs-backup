/**
 * UISpin - A quiet, theme-aware loading indicator.
  * @demoArgs {"size":"large"}
 */

import * as React from 'react';
import { Spin as AntSpin, type SpinProps as AntSpinProps } from 'antd';
import styles from './UISpin.module.css';
import type { UICompatibleSize } from '../core/UISize';

export type UISpinSize = UICompatibleSize;

function resolveSpinSize(size: UISpinSize): 'small' | 'default' | 'large' {
  if (size === 'sm' || size === 'small') return 'small';
  if (size === 'lg' || size === 'large') return 'large';
  return 'default';
}

export interface UISpinIndicatorProps {
  size?: UISpinSize;
  /** Inherit the foreground color when used inside a button. */
  tone?: 'primary' | 'inherit';
}

export function UISpinIndicator({ size = 'default', tone = 'primary' }: UISpinIndicatorProps) {
  const resolvedSize = resolveSpinSize(size);
  return (
    <span
      className={`${styles.indicator} ${styles[resolvedSize]} ${tone === 'inherit' ? styles.inherit : ''}`}
      aria-hidden="true"
    >
      <svg viewBox="0 0 24 24" focusable="false">
        <circle className={styles.track} cx="12" cy="12" r="9" />
        <circle className={styles.arc} cx="12" cy="12" r="9" />
      </svg>
    </span>
  );
}

export interface UISpinProps extends Omit<AntSpinProps, 'indicator' | 'size'> {
  indicator?: AntSpinProps['indicator'];
  size?: UISpinSize;
}

export function UISpin({ size = 'default', indicator, ...props }: UISpinProps) {
  return (
    <AntSpin
      {...props}
      size={resolveSpinSize(size)}
      indicator={indicator ?? <UISpinIndicator size={size} />}
    />
  );
}
