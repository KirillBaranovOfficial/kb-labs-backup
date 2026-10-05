/**
 * UIInput component - Text input with variants
 *
 * Wraps Ant Design Input with semantic variants.
 * NO hardcoded colors, uses Ant Design theme.
  * @demoArgs {"placeholder":"Search services","defaultValue":"gateway","allowClear":true,"showCount":false,"size":"middle"}
 */

import * as React from 'react';
import { Input as AntInput } from 'antd';
import type { InputProps as AntInputProps } from 'antd';
import { toAntSize } from '../core/UISize';
import type { UICompatibleSize } from '../core/UISize';

export interface UIInputProps extends Omit<AntInputProps, 'onChange' | 'size'> {
  /** Input value */
  value?: string;
  /** Default value */
  defaultValue?: string;
  /** Placeholder text */
  placeholder?: string;
  /** Disabled state */
  disabled?: boolean;
  /** Prefix icon/element */
  prefix?: React.ReactNode;
  /** Suffix icon/element */
  suffix?: React.ReactNode;
  /** Max length */
  maxLength?: number;
  /** Show character count */
  showCount?: boolean;
  /** Allow clear */
  allowClear?: boolean;
  /** Change handler (returns value string, not event) */
  onChange?: (value: string) => void;
  /** Enter key handler */
  onPressEnter?: () => void;
  /** Shared size scale. Ant Design values remain accepted as aliases. */
  size?: UICompatibleSize;
}

/**
 * UIInput - Text input
 *
 * @example
 * ```tsx
 * <UIInput placeholder="Enter name" />
 * <UIInput prefix={<SearchIcon />} placeholder="Search..." />
 * <UIInput allowClear showCount maxLength={100} />
 * ```
 */
export function UIInput({
  value,
  defaultValue,
  placeholder,
  disabled,
  prefix,
  suffix,
  maxLength,
  showCount,
  allowClear,
  onChange,
  onPressEnter,
  size,
  ...rest
}: UIInputProps) {
  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    onChange?.(e.target.value);
  };

  return (
    <AntInput
      value={value}
      defaultValue={defaultValue}
      placeholder={placeholder}
      disabled={disabled}
      prefix={prefix}
      suffix={suffix}
      maxLength={maxLength}
      showCount={showCount}
      allowClear={allowClear}
      onChange={handleChange}
      onPressEnter={onPressEnter}
      size={toAntSize(size)}
      {...rest}
    />
  );
}

/**
 * UIInputPassword - Password input with toggle visibility
 */
export type UIInputPasswordProps = Omit<React.ComponentProps<typeof AntInput.Password>, 'size'> & { size?: UICompatibleSize };

export function UIInputPassword({ size, ...props }: UIInputPasswordProps) {
  return <AntInput.Password {...props} size={toAntSize(size)} />;
}

/**
 * UIInputTextArea - Multiline text input
 */
export type UIInputTextAreaProps = Omit<React.ComponentProps<typeof AntInput.TextArea>, 'size'> & { size?: UICompatibleSize };

export function UIInputTextArea({ size, ...props }: UIInputTextAreaProps) {
  return <AntInput.TextArea {...props} size={toAntSize(size)} />;
}

/**
 * UIInputSearch - Search input with search button
 */
export type UIInputSearchProps = Omit<React.ComponentProps<typeof AntInput.Search>, 'size'> & { size?: UICompatibleSize };

export function UIInputSearch({ size, ...props }: UIInputSearchProps) {
  return <AntInput.Search {...props} size={toAntSize(size)} />;
}
