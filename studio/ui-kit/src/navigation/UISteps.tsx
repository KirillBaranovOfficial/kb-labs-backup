/**
 * UISteps component - Step indicator
 *
 * Wraps Ant Design Steps for multi-step processes.
  * @demoArgs {"current":1,"items":[{"title":"Design"},{"title":"Configure"},{"title":"Deploy"}]}
 */

import * as React from 'react';
import { Steps as AntSteps, type StepsProps as AntStepsProps } from 'antd';

export interface UIStepsProps extends AntStepsProps {}

export function UISteps(props: UIStepsProps) {
  return <AntSteps {...props} />;
}
