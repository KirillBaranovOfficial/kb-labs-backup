/**
 * UIMenu component - Navigation menu
 *
 * Wraps Ant Design Menu.
  * @demoArgs {"mode":"horizontal","selectedKeys":["overview"],"items":[{"key":"overview","label":"Overview"},{"key":"routes","label":"Routes"},{"key":"access","label":"Access"}]}
 */

import * as React from 'react';
import { Menu as AntMenu, type MenuProps as AntMenuProps } from 'antd';

export interface UIMenuProps extends AntMenuProps {}

export function UIMenu(props: UIMenuProps) {
  return <AntMenu {...props} />;
}
