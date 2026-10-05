/**
 * UIPopconfirm component - Confirmation popover
 *
 * Wraps Ant Design Popconfirm for inline confirmations.
 * @demoArgs {"title":"Disable gateway?","description":"Incoming requests will be rejected.","okText":"Disable","cancelText":"Keep enabled","children":"Disable"}
 */

import * as React from 'react';
import { Popconfirm as AntPopconfirm, type PopconfirmProps as AntPopconfirmProps } from 'antd';

export interface UIPopconfirmProps extends AntPopconfirmProps {}

export function UIPopconfirm(props: UIPopconfirmProps) {
  return <AntPopconfirm {...props} />;
}
