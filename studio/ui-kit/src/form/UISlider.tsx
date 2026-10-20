/**
 * UISlider component - Slider input
 *
 * Wraps Ant Design Slider.
  * @demoArgs {"defaultValue":64,"min":0,"max":100,"step":1}
 */

import * as React from 'react';
import { Slider as AntSlider, type SliderSingleProps } from 'antd';

export interface UISliderProps extends SliderSingleProps {}

export function UISlider(props: UISliderProps) {
  return <AntSlider {...props} />;
}
