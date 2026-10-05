/**
 * UIInputNumber component - Numeric input
 *
 * Wraps Ant Design InputNumber for numeric value entry.
 * @demoArgs {"size":"middle","defaultValue":8,"min":0,"max":24,"placeholder":"Replicas"}
 */

import * as React from 'react';
import { InputNumber as AntInputNumber, type InputNumberProps as AntInputNumberProps } from 'antd';
import { toAntSize } from '../core/UISize';
import type { UICompatibleSize } from '../core/UISize';

export interface UIInputNumberProps extends Omit<AntInputNumberProps, 'size'> {
  /** Shared size scale. Ant Design values remain accepted as aliases. */
  size?: UICompatibleSize;
}

export function UIInputNumber({ size, ...props }: UIInputNumberProps) {
  return <AntInputNumber {...props} size={toAntSize(size)} />;
}
