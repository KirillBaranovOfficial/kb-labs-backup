/**
 * UIDrawer component - Side panel overlay
 *
 * Wraps Ant Design Drawer for sliding panels.
  * @demoArgs {"title":"Gateway details","placement":"right","width":360,"children":"Authentication and routes for this gateway."}
 */

import * as React from 'react';
import { Drawer as AntDrawer, type DrawerProps as AntDrawerProps } from 'antd';

export interface UIDrawerProps extends AntDrawerProps {}

export function UIDrawer(props: UIDrawerProps) {
  return <AntDrawer {...props} />;
}
