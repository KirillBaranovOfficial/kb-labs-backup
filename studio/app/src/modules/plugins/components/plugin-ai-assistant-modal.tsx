/**
 * AI Assistant Modal for Plugin Inspector
 *
 * Allows users to ask questions about a plugin and get AI-generated answers
 * based on the plugin's manifest.
 */

import React, { useState } from 'react';
import {
  UIModal,
  UIInputTextArea,
  UIButton,
  UISpace,
  UITypographyText,
  UICard,
  UISpin,
  UIAlert,
  UITag,
  UIDivider,
  UIIcon,
} from '@kb-labs/studio-ui-kit';
import type { PluginAskResponse } from '@kb-labs/studio-data-client';
import { UIMarkdownViewer } from '@kb-labs/studio-ui-kit';
import styles from './plugin-ai-assistant-modal.module.css';

interface PluginAIAssistantModalProps {
  open: boolean;
  onClose: () => void;
  pluginId: string;
  pluginName: string;
  onAsk: (question: string) => Promise<PluginAskResponse>;
}

interface QAItem {
  question: string;
  answer: string;
  timestamp: Date;
  usage: {
    promptTokens: number;
    completionTokens: number;
  };
}

const QUESTION_TEMPLATES = [
  { label: 'What permissions?', value: 'What permissions does this plugin need and why?' },
  { label: 'Show CLI commands', value: 'What CLI commands does this plugin provide? List them with their flags and usage.' },
  { label: 'Show REST APIs', value: 'What REST API endpoints does this plugin expose? Describe their paths, methods, and purpose.' },
  { label: 'What does it do?', value: 'What does this plugin do? Explain its main features and use cases.' },
  { label: 'How to configure?', value: 'How do I configure this plugin? What options are available?' },
  { label: 'What files created?', value: 'What files or directories does this plugin create or modify?' },
];

export function PluginAIAssistantModal({
  open,
  onClose,
  pluginId,
  pluginName,
  onAsk,
}: PluginAIAssistantModalProps) {
  const [customQuestion, setCustomQuestion] = useState('');
  const [history, setHistory] = useState<QAItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleAsk = async (question: string) => {
    if (!question.trim()) {return;}

    setLoading(true);
    setError(null);

    try {
      const response = await onAsk(question);

      setHistory((prev) => [
        {
          question,
          answer: response.answer,
          timestamp: new Date(),
          usage: response.usage,
        },
        ...prev,
      ]);
      setCustomQuestion('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to get AI response');
    } finally {
      setLoading(false);
    }
  };

  const handleTemplateClick = (question: string) => {
    setCustomQuestion(question);
  };

  const handleSubmit = () => {
    handleAsk(customQuestion);
  };

  return (
    <UIModal
      description="Ask about this plugin's capabilities, permissions, and configuration."
      title={
        <UISpace>
          <UIIcon name="RobotOutlined" />
          <span>AI Assistant</span>
        </UISpace>
      }
      open={open}
      onCancel={onClose}
      width={800}
      className={styles.modal}
      footer={[
        <UIButton key="cancel" onClick={onClose}>
          Close
        </UIButton>,
        <UIButton
          key="ask"
          variant="primary"
          icon={<UIIcon name="ThunderboltOutlined" />}
          loading={loading}
          onClick={handleSubmit}
          disabled={!customQuestion.trim()}
        >
          Ask Question
        </UIButton>,
      ]}
    >
      <div className={styles.content}>
        {/* Plugin Preview */}
        <UICard size="small" className={styles.contextCard}>
          <div className={styles.contextDetails}>
            <div className={styles.contextItem}>
              <UITypographyText type="secondary">Plugin</UITypographyText>
              <UITypographyText strong>{pluginName}</UITypographyText>
            </div>
            <div className={styles.contextItem}>
              <UITypographyText type="secondary">Plugin ID</UITypographyText>
              <UITypographyText code className={styles.pluginId}>{pluginId}</UITypographyText>
            </div>
          </div>
        </UICard>

        {/* Quick Templates */}
        <section className={styles.section}>
          <UITypographyText strong>Quick Questions:</UITypographyText>
          <div className={styles.quickQuestions}>
            {QUESTION_TEMPLATES.map((template) => (
              <UIButton
                key={template.label}
                size="small"
                onClick={() => handleTemplateClick(template.value)}
              >
                {template.label}
              </UIButton>
            ))}
          </div>
        </section>

        <UIDivider className={styles.divider} />

        {/* Custom Question */}
        <section className={styles.section}>
          <UITypographyText strong>Your Question:</UITypographyText>
          <UIInputTextArea
            value={customQuestion}
            onChange={(e) => setCustomQuestion(e.target.value)}
            placeholder="Ask anything about this plugin... (e.g., 'How do I use this plugin?')"
            rows={3}
            className={styles.questionInput}
            onPressEnter={(e) => {
              if (e.ctrlKey || e.metaKey) {
                handleSubmit();
              }
            }}
          />
          <UITypographyText type="secondary" style={{ fontSize: 12, marginTop: 4, display: 'block' }}>
            Press Ctrl/Cmd + Enter to ask
          </UITypographyText>
        </section>

        {/* Error Display */}
        {error && (
          <UIAlert
            message="Error"
            description={error}
            variant="error"
            closable
            onClose={() => setError(null)}
            showIcon
          />
        )}

        {/* Loading State */}
        {loading && (
          <div className={styles.loading}>
            <UISpin tip="AI is thinking..." size="large" />
          </div>
        )}

        {/* Q&A History */}
        {history.length > 0 && !loading && (
          <section className={styles.section}>
            <UIDivider orientation="left" style={{ margin: '16px 0 8px 0' }}>
              <UITypographyText strong>Conversation History</UITypographyText>
            </UIDivider>
            <div className={styles.historyList}>
              {history.map((item, index) => (
                <UICard
                  key={`${item.timestamp.getTime()}-${index}`}
                  size="small"
                  className={styles.historyCard}
                >
                  {/* Question */}
                  <div className={styles.historyQuestion}>
                    <UISpace>
                      <UIIcon name="QuestionCircleOutlined" className={styles.questionIcon} />
                      <UITypographyText strong>{item.question}</UITypographyText>
                    </UISpace>
                  </div>

                  {/* Answer */}
                  <div className={styles.answer}>
                    <UIMarkdownViewer content={item.answer} className={styles.markdown} />
                  </div>

                  {/* Metadata */}
                  <div className={styles.metadata}>
                    <UISpace size="small">
                      <UITag icon={<UIIcon name="ClockCircleOutlined" />} color="blue">
                        {item.timestamp.toLocaleTimeString()}
                      </UITag>
                      <UITag color="green">
                        {item.usage.promptTokens + item.usage.completionTokens} tokens
                      </UITag>
                    </UISpace>
                  </div>
                </UICard>
              ))}
            </div>
          </section>
        )}

        {/* Empty State */}
        {history.length === 0 && !loading && (
          <div className={styles.emptyState}>
            <UIIcon name="RobotOutlined" className={styles.emptyIcon} />
            <div className={styles.emptyText}>
              Select a quick question or type your own to get started
            </div>
          </div>
        )}
      </div>
    </UIModal>
  );
}
