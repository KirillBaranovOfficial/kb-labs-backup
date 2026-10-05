/**
 * UIPopover component - Popover overlay
 *
 * Wraps Ant Design Popover for floating content.
 * @demoArgs {"title":"Service status","content":"Healthy · Last checked just now","trigger":"click","children":"Status"}
 */

import * as React from 'react';
import { Popover as AntPopover, type PopoverProps as AntPopoverProps } from 'antd';

export interface UIPopoverProps extends AntPopoverProps {}

export function UIPopover(props: UIPopoverProps) {
  return <AntPopover {...props} />;
}
