/**
 * UISegmented component - Segmented control
 *
 * Wraps Ant Design Segmented for tab-like selection.
 * @demoArgs {"size":"md","options":["Overview","Routes","Access"],"defaultValue":"Overview"}
 */

import * as React from 'react';
import { Segmented as AntSegmented, type SegmentedProps as AntSegmentedProps } from 'antd';
import { toAntSize } from '../core/UISize';
import type { UICompatibleSize } from '../core/UISize';

export interface UISegmentedProps extends Omit<AntSegmentedProps, 'size'> {
  /** Shared size scale. Ant Design values remain accepted as aliases. */
  size?: UICompatibleSize;
}

export function UISegmented({ size, ...props }: UISegmentedProps) {
  return <AntSegmented {...props} size={toAntSize(size)} />;
}
