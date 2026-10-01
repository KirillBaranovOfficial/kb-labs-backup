/**
 * @module @kb-labs/studio-app/modules/projects/components/add-project-modal
 *
 * "+ Add project" dialog: registers a folder path through the host API. The
 * project repository is not inspected or modified by registration.
 */

import * as React from 'react';
import { UIModal, UIForm, UIFormItem, UIInput, UIMessage, useUIForm } from '@kb-labs/studio-ui-kit';
import { useDataSources } from '@/providers/data-sources-provider';
import { useAddProject } from '@kb-labs/studio-data-client';

export interface AddProjectModalProps {
  open: boolean;
  onClose: () => void;
  onAdded: () => void;
}

interface FormValues {
  path: string;
  name?: string;
}

export function AddProjectModal({ open, onClose, onAdded }: AddProjectModalProps) {
  const { projects: projectsSource } = useDataSources();
  const addProject = useAddProject(projectsSource);
  const [form] = useUIForm<FormValues>();

  const reset = React.useCallback(() => {
    form.resetFields();
  }, [form]);

  const handleClose = React.useCallback(() => {
    reset();
    onClose();
  }, [reset, onClose]);

  const submit = React.useCallback(
    async (values: FormValues) => {
      try {
        await addProject.mutateAsync({ path: values.path, name: values.name || undefined });
        UIMessage.success(`Registered ${values.path}`);
        reset();
        onAdded();
      } catch (error) {
        UIMessage.error(error instanceof Error ? error.message : 'Failed to register the project');
      }
    },
    [addProject, reset, onAdded],
  );

  const handleOk = React.useCallback(() => {
    form.validateFields().then(submit).catch(() => undefined);
  }, [form, submit]);

  return (
    <UIModal
      open={open}
      title="Add project"
      okText="Register"
      onOk={handleOk}
      onCancel={handleClose}
      confirmLoading={addProject.isPending}
    >
      <UIForm form={form} layout="vertical">
        <UIFormItem
          label="Folder path"
          name="path"
          rules={[{ required: true, message: 'Enter the absolute path of the project folder' }]}
        >
          <UIInput placeholder="/absolute/path/to/project" autoFocus />
        </UIFormItem>
        <UIFormItem label="Name (optional)" name="name">
          <UIInput placeholder="Defaults to the folder name" />
        </UIFormItem>
      </UIForm>
    </UIModal>
  );
}
