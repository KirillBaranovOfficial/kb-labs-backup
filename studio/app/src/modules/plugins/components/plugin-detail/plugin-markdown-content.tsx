import { UISkeleton, UIAlert, UICard, UIEmptyState, UIMarkdownViewer } from '@kb-labs/studio-ui-kit';

export interface PluginMarkdownContentProps {
  content: string | null;
  loading: boolean;
  error: string | null;
  emptyText?: string;
}

export function PluginMarkdownContent({
  content,
  loading,
  error,
  emptyText = 'No content available',
}: PluginMarkdownContentProps) {
  if (loading) {
    return (
      <UISkeleton active lines={6} />
    );
  }
  if (error) {
    return <UIAlert variant="error" message="Failed to load content" description={error} showIcon />;
  }
  if (content === null) {
    return <UIEmptyState description={emptyText} />;
  }
  return (
    <UICard>
      <UIMarkdownViewer content={content} />
    </UICard>
  );
}
