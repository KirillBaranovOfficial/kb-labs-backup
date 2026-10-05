/**
 * UIResult component - Result page
 *
 * Wraps Ant Design Result for success/error/info pages.
  * @demoArgs {"status":"success","title":"Deployment complete","subTitle":"Gateway is available and healthy"}
 */

import * as React from 'react';
import { Result as AntResult, type ResultProps as AntResultProps } from 'antd';

export interface UIResultProps extends AntResultProps {}

export function UIResult(props: UIResultProps) {
  return <AntResult {...props} />;
}
