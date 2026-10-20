/**
 * UIDatePicker component - Date/time picker
 *
 * Wraps Ant Design DatePicker with variants.
 * NO hardcoded colors, uses Ant Design theme.
  * @demoArgs {"placeholder":"Select a date","format":"YYYY-MM-DD","allowClear":true,"showTime":false,"size":"middle"}
 */

import * as React from 'react';
import { DatePicker as AntDatePicker, TimePicker as AntTimePicker } from 'antd';
import type { DatePickerProps as AntDatePickerProps, TimePickerProps as AntTimePickerProps } from 'antd';
import type { Dayjs } from 'dayjs';
import { toAntSize } from '../core/UISize';
import type { UICompatibleSize } from '../core/UISize';

export interface UIDatePickerProps extends Omit<AntDatePickerProps, 'onChange' | 'size'> {
  /** Selected date */
  value?: Dayjs | null;
  /** Default date */
  defaultValue?: Dayjs;
  /** Placeholder */
  placeholder?: string;
  /** Disabled state */
  disabled?: boolean;
  /** Date format */
  format?: string;
  /** Allow clear */
  allowClear?: boolean;
  /** Show time picker */
  showTime?: boolean;
  /** Change handler (returns Dayjs or null) */
  onChange?: (date: Dayjs | null) => void;
  /** Shared size scale. Ant Design values remain accepted as aliases. */
  size?: UICompatibleSize;
}

export interface UITimePickerProps extends Omit<AntTimePickerProps, 'onChange' | 'size'> {
  /** Selected time */
  value?: Dayjs | null;
  /** Default time */
  defaultValue?: Dayjs;
  /** Placeholder */
  placeholder?: string;
  /** Disabled state */
  disabled?: boolean;
  /** Time format */
  format?: string;
  /** Allow clear */
  allowClear?: boolean;
  /** Change handler (returns Dayjs or null) */
  onChange?: (time: Dayjs | null) => void;
  /** Shared size scale. Ant Design values remain accepted as aliases. */
  size?: UICompatibleSize;
}

/**
 * UIDatePicker - Date picker
 *
 * @example
 * ```tsx
 * <UIDatePicker placeholder="Select date" />
 * <UIDatePicker showTime format="YYYY-MM-DD HH:mm:ss" />
 * <UIDatePicker
 *   onChange={(date) => console.log(date?.format('YYYY-MM-DD'))}
 * />
 * ```
 */
export function UIDatePicker({
  value,
  defaultValue,
  placeholder,
  disabled,
  format,
  allowClear = true,
  showTime,
  size,
  onChange,
  ...rest
}: UIDatePickerProps) {
  return (
    <AntDatePicker
      value={value}
      defaultValue={defaultValue}
      placeholder={placeholder}
      disabled={disabled}
      format={format}
      allowClear={allowClear}
      showTime={showTime}
      size={toAntSize(size)}
      onChange={onChange}
      {...rest}
    />
  );
}

/**
 * UITimePicker - Time picker
 *
 * @example
 * ```tsx
 * <UITimePicker placeholder="Select time" />
 * <UITimePicker format="HH:mm" />
 * ```
 */
export function UITimePicker({
  value,
  defaultValue,
  placeholder,
  disabled,
  format,
  allowClear = true,
  size,
  onChange,
  ...rest
}: UITimePickerProps) {
  return (
    <AntTimePicker
      value={value}
      defaultValue={defaultValue}
      placeholder={placeholder}
      disabled={disabled}
      format={format}
      allowClear={allowClear}
      size={toAntSize(size)}
      onChange={onChange}
      {...rest}
    />
  );
}

/**
 * UIRangePicker - Date range picker
 */
export type UIRangePickerProps = Omit<React.ComponentProps<typeof AntDatePicker.RangePicker>, 'size'> & { size?: UICompatibleSize };

export function UIRangePicker({ size, ...props }: UIRangePickerProps) {
  return <AntDatePicker.RangePicker {...props} size={toAntSize(size)} />;
}
