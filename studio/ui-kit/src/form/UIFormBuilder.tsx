/**
 * UIFormBuilder - typed declarative forms for common Studio workflows.
 *
 * Build validated forms from UI kit fields while retaining UIForm for custom layouts.
 * Use `custom` fields when a control needs bespoke React rendering.
 *
 * @example
 * ```tsx
 * type ProjectValues = { name: string; scope: 'user' | 'workspace'; path?: string };
 *
 * const definition: UIFormDefinition<ProjectValues> = {
 *   sections: [{
 *     title: 'Project',
 *     fields: [
 *       { name: 'name', type: 'text', label: 'Name', required: true },
 *       {
 *         name: 'scope', type: 'select', label: 'Scope',
 *         options: [
 *           { label: 'Personal', value: 'user' },
 *           { label: 'Workspace', value: 'workspace' },
 *         ],
 *       },
 *       {
 *         name: 'path', type: 'text', label: 'Workspace path',
 *         visibleWhen: { field: 'scope', op: 'equals', value: 'workspace' },
 *         requiredWhen: { field: 'scope', op: 'equals', value: 'workspace' },
 *       },
 *     ],
 *   }],
 * };
 *
 * <UIModal
 *   open={open}
 *   title="Create project"
 *   description="Choose where the project belongs."
 *   footer={<UIButton htmlType="submit" form="project-form" variant="primary">Create project</UIButton>}
 * >
 *   <UIFormBuilder id="project-form" definition={definition} onSubmit={(values) => saveProject(values)} />
 * </UIModal>
 * ```
 * @demoArgs {"definition":{"sections":[{"title":"Gateway access","description":"Configure access for this environment.","columns":1,"fields":[{"name":"environment","label":"Environment","type":"select","options":[{"label":"Development","value":"development"},{"label":"Production","value":"production"}],"required":true},{"name":"issuerUrl","label":"Issuer URL","type":"text","placeholder":"https://auth.example.com"}]}]},"initialValues":{"environment":"development","issuerUrl":"https://auth.example.com"},"layout":"vertical"}
 */

import * as React from 'react';
import { Form as AntForm } from 'antd';
import type { FormInstance, FormItemProps } from 'antd';
import { UIForm, UIFormItem } from './UIForm';
import { UIInput, UIInputTextArea } from './UIInput';
import type { UIInputProps } from './UIInput';
import { UIInputNumber } from './UIInputNumber';
import type { UIInputNumberProps } from './UIInputNumber';
import { UISelect } from './UISelect';
import type { UISelectOption } from './UISelect';
import { UICheckbox } from './UICheckbox';
import { UISwitch } from './UISwitch';
import type { UISwitchProps } from './UISwitch';
import { UIDatePicker } from './UIDatePicker';
import type { UIDatePickerProps } from './UIDatePicker';
import { UIRadioGroup } from './UIRadio';
import { UIAlert } from '../feedback/UIAlert';
import styles from './UIFormBuilder.module.css';

export type UIFormBuilderName<TValues extends object> = Extract<keyof TValues, string>;
export type UIFormBuilderRules = FormItemProps['rules'];

export type UIFormBuilderCondition<TValues extends object> = {
  [TName in UIFormBuilderName<TValues>]:
    | { field: TName; op: 'equals' | 'notEquals'; value: TValues[TName] }
    | { field: TName; op: 'in' | 'notIn'; value: readonly TValues[TName][] }
    | { field: TName; op: 'truthy' | 'falsy' }
}[UIFormBuilderName<TValues>];

interface UIFormBuilderFieldBase<TValues extends object> {
  name: UIFormBuilderName<TValues>;
  label: React.ReactNode;
  description?: React.ReactNode;
  placeholder?: string;
  required?: boolean | string;
  rules?: UIFormBuilderRules;
  visibleWhen?: UIFormBuilderCondition<TValues>;
  disabledWhen?: UIFormBuilderCondition<TValues>;
  requiredWhen?: UIFormBuilderCondition<TValues>;
  disabled?: boolean;
  preserve?: boolean;
  span?: 'full';
}

export type UIFormBuilderField<TValues extends object> =
  UIFormBuilderFieldBase<TValues> & (
    | { type: 'text'; inputProps?: Omit<UIInputProps, 'value' | 'defaultValue' | 'onChange'> }
    | { type: 'textarea'; inputProps?: Omit<React.ComponentProps<typeof UIInputTextArea>, 'value' | 'defaultValue' | 'onChange'> }
    | { type: 'number'; inputProps?: Omit<UIInputNumberProps, 'value' | 'defaultValue' | 'onChange'> }
    | { type: 'select'; options: UISelectOption[]; loadingOptions?: boolean; inputProps?: Omit<React.ComponentProps<typeof UISelect>, 'options' | 'value' | 'defaultValue' | 'onChange'> }
    | { type: 'checkbox'; inputProps?: Omit<React.ComponentProps<typeof UICheckbox>, 'checked' | 'defaultChecked' | 'onChange' | 'children'> }
    | { type: 'switch'; inputProps?: Omit<UISwitchProps, 'checked' | 'defaultChecked' | 'onChange' | 'label'> }
    | { type: 'date'; inputProps?: Omit<UIDatePickerProps, 'value' | 'defaultValue' | 'onChange'> }
    | { type: 'radio'; options: Array<{ label: string; value: string | number; disabled?: boolean }> }
    | {
      type: 'custom';
      render: (context: { form: FormInstance<TValues>; values: Partial<TValues>; disabled: boolean }) => React.ReactNode;
    }
  );

export interface UIFormBuilderSection<TValues extends object> {
  title?: React.ReactNode;
  description?: React.ReactNode;
  columns?: 1 | 2;
  fields: UIFormBuilderField<TValues>[];
}

export interface UIFormDefinition<TValues extends object> {
  sections: UIFormBuilderSection<TValues>[];
}

export interface UIFormBuilderProps<TValues extends object> {
  definition: UIFormDefinition<TValues>;
  id?: string;
  form?: FormInstance<TValues>;
  initialValues?: Partial<TValues>;
  layout?: 'horizontal' | 'vertical' | 'inline';
  serverErrors?: Partial<Record<UIFormBuilderName<TValues>, string | readonly string[]>>;
  onSubmit: (values: TValues) => void | Promise<void>;
  onSubmitError?: (error: unknown) => void;
  onSubmittingChange?: (submitting: boolean) => void;
  onValuesChange?: (changedValues: Partial<TValues>, values: TValues) => void;
}

function matchesCondition<TValues extends object>(
  condition: UIFormBuilderCondition<TValues> | undefined,
  values: Partial<TValues>,
): boolean {
  if (!condition) return false;
  const currentValue = values[condition.field];

  switch (condition.op) {
    case 'equals': return Object.is(currentValue, condition.value);
    case 'notEquals': return !Object.is(currentValue, condition.value);
    case 'in': return condition.value.some((value) => Object.is(currentValue, value));
    case 'notIn': return !condition.value.some((value) => Object.is(currentValue, value));
    case 'truthy': return Boolean(currentValue);
    case 'falsy': return !currentValue;
  }
}

function getFieldRules<TValues extends object>(
  field: UIFormBuilderField<TValues>,
  isConditionallyRequired: boolean,
): UIFormBuilderRules {
  const rules = [...(field.rules ?? [])];
  const required = field.required === true || typeof field.required === 'string' || isConditionallyRequired;

  if (required && !rules.some((rule) => typeof rule === 'object' && rule.required)) {
    const message = typeof field.required === 'string'
      ? field.required
      : `${String(field.label)} is required`;
    rules.unshift({ required: true, message });
  }

  return rules;
}

function renderControl<TValues extends object>(
  field: UIFormBuilderField<TValues>,
  disabled: boolean,
) {
  switch (field.type) {
    case 'text':
      return <UIInput {...field.inputProps} placeholder={field.placeholder ?? field.inputProps?.placeholder} disabled={disabled} />;
    case 'textarea':
      return <UIInputTextArea {...field.inputProps} placeholder={field.placeholder ?? field.inputProps?.placeholder} disabled={disabled} />;
    case 'number':
      return <UIInputNumber {...field.inputProps} placeholder={field.placeholder ?? field.inputProps?.placeholder} disabled={disabled} />;
    case 'select':
      return <UISelect {...field.inputProps} options={field.options} loading={field.loadingOptions ?? field.inputProps?.loading} placeholder={field.placeholder ?? field.inputProps?.placeholder} disabled={disabled} />;
    case 'checkbox':
      return <UICheckbox {...field.inputProps} disabled={disabled} />;
    case 'switch':
      return <UISwitch {...field.inputProps} disabled={disabled} />;
    case 'date':
      return <UIDatePicker {...field.inputProps} placeholder={field.placeholder ?? field.inputProps?.placeholder} disabled={disabled} />;
    case 'radio':
      return <UIRadioGroup options={field.options} disabled={disabled} />;
    case 'custom':
      return null;
  }
}

/** Render a typed, validated form definition using the Studio UI kit controls. */
export function UIFormBuilder<TValues extends object>({
  definition,
  id,
  form: providedForm,
  initialValues,
  layout = 'vertical',
  serverErrors,
  onSubmit,
  onSubmitError,
  onSubmittingChange,
  onValuesChange,
}: UIFormBuilderProps<TValues>) {
  const [internalForm] = AntForm.useForm<TValues>();
  const form = providedForm ?? internalForm;
  const watchedValues = (AntForm.useWatch([], form) as Partial<TValues> | undefined) ?? {};
  const [submitting, setSubmitting] = React.useState(false);
  const [submitError, setSubmitError] = React.useState<string | null>(null);
  const [editedServerErrorFields, setEditedServerErrorFields] = React.useState<Set<UIFormBuilderName<TValues>>>(() => new Set());
  const serverErrorFingerprint = JSON.stringify(serverErrors ?? {});

  React.useEffect(() => {
    setEditedServerErrorFields(new Set());
  }, [serverErrorFingerprint]);

  const handleValuesChange = (changedValues: Partial<TValues>, values: TValues) => {
    setSubmitError(null);
    const changedNames = Object.keys(changedValues) as UIFormBuilderName<TValues>[];
    setEditedServerErrorFields((current) => new Set([...current, ...changedNames]));
    onValuesChange?.(changedValues, values);
  };

  const handleSubmit = async (values: TValues) => {
    setSubmitting(true);
    onSubmittingChange?.(true);
    setSubmitError(null);
    setEditedServerErrorFields(new Set());
    try {
      await onSubmit(values);
    } catch (error) {
      setSubmitError(error instanceof Error ? error.message : 'Unable to save changes. Please try again.');
      onSubmitError?.(error);
    } finally {
      setSubmitting(false);
      onSubmittingChange?.(false);
    }
  };

  return (
    <UIForm
      id={id}
      form={form}
      layout={layout}
      initialValues={initialValues}
      onValuesChange={handleValuesChange}
      onFinish={handleSubmit}
      disabled={submitting}
    >
      <div className={styles.formBuilder}>
        {submitError && <UIAlert variant="error" message={submitError} showIcon />}
        {definition.sections.map((section, sectionIndex) => (
          <section className={styles.section} key={`section-${sectionIndex}`}>
            {(section.title || section.description) && (
              <header className={styles.sectionHeader}>
                {section.title && <h3 className={styles.sectionTitle}>{section.title}</h3>}
                {section.description && <div className={styles.sectionDescription}>{section.description}</div>}
              </header>
            )}
            <div className={styles.fields} style={{ '--form-columns': section.columns ?? 1 } as React.CSSProperties}>
              {section.fields.map((field) => {
                if (field.visibleWhen && !matchesCondition(field.visibleWhen, watchedValues)) return null;

                const requiredByCondition = Boolean(field.requiredWhen && matchesCondition(field.requiredWhen, watchedValues));
                const disabled = field.disabled || Boolean(field.disabledWhen && matchesCondition(field.disabledWhen, watchedValues));
                const isCheckbox = field.type === 'checkbox';
                const isSwitch = field.type === 'switch';
                const serverError = editedServerErrorFields.has(field.name) ? undefined : serverErrors?.[field.name];
                const serverErrorMessage = serverError
                  ? typeof serverError === 'string' ? serverError : serverError.join(' · ')
                  : undefined;

                return (
                  <div className={field.span === 'full' ? styles.fieldFull : undefined} key={field.name}>
                    <UIFormItem
                      name={field.name}
                      label={isCheckbox ? undefined : field.label}
                      valuePropName={isCheckbox || isSwitch ? 'checked' : 'value'}
                      rules={getFieldRules(field, requiredByCondition)}
                      required={field.required === true || typeof field.required === 'string' || requiredByCondition}
                      validateStatus={serverErrorMessage ? 'error' : undefined}
                      help={serverErrorMessage}
                      extra={field.description}
                      preserve={field.preserve ?? false}
                    >
                      {field.type === 'custom'
                        ? field.render({ form, values: watchedValues, disabled: Boolean(disabled) || submitting })
                        : isCheckbox
                          ? <UICheckbox>{field.label}</UICheckbox>
                          : renderControl(field, Boolean(disabled))}
                    </UIFormItem>
                  </div>
                );
              })}
            </div>
          </section>
        ))}
      </div>
    </UIForm>
  );
}
