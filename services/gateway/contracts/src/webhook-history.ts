import { z } from 'zod';

export const WEBHOOK_HISTORY_RETENTION_LIMIT = 100;

export const WebhookHistoryOutcomeSchema = z.discriminatedUnion('type', [
  z.object({ type: z.literal('delivered') }).strict(),
  z.object({ type: z.literal('handler_error'), code: z.string().min(1) }).strict(),
  z.object({ type: z.literal('rejected'), code: z.string().min(1) }).strict(),
]);

export const WebhookHistoryRecordSchema = z.object({
  webhookId: z.string().min(1),
  project: z.string(),
  receivedAt: z.string().datetime(),
  pluginId: z.string().min(1),
  event: z.string().min(1),
  handler: z.string().min(1),
  instanceId: z.string().nullable(),
  outcome: WebhookHistoryOutcomeSchema,
  durationMs: z.number().finite().nonnegative(),
  correlationId: z.string().min(1),
}).strict();

export type WebhookHistoryOutcome = z.infer<typeof WebhookHistoryOutcomeSchema>;
export type WebhookHistoryRecord = z.infer<typeof WebhookHistoryRecordSchema>;
