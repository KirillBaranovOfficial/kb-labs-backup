/**
 * UIDropdown component - Dropdown menu
 *
 * Wraps Ant Design Dropdown.
 * @demoArgs {"menu":{"items":[{"key":"settings","label":"Settings"},{"key":"disable","label":"Disable"}]},"trigger":["click"],"children":"Actions"}
 */

import * as React from 'react';
import { Dropdown as AntDropdown, type DropdownProps as AntDropdownProps } from 'antd';

export interface UIDropdownProps extends AntDropdownProps {}

export function UIDropdown(props: UIDropdownProps) {
  return <AntDropdown {...props} />;
}

export function UIDropdownButton(props: React.ComponentProps<typeof AntDropdown.Button>) {
  return <AntDropdown.Button {...props} />;
}
