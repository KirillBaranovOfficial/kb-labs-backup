import { z } from 'zod';

export const GatewayBootstrapStatusSchema = z.enum([
  'not-configured',
  'provisioned',
  'exists-active',
  'exists-non-active',
  'failed',
]);

export const GatewayAuthHealthIssueSchema = z.object({
  code: z.string(),
  severity: z.enum(['error', 'warning']),
  message: z.string(),
  hint: z.string(),
}).strict();

export const GatewayAuthHealthResponseSchema = z.object({
  ok: z.boolean(),
  authEnabled: z.boolean(),
  tenantId: z.string(),
  activeAdmins: z.number().int().nonnegative(),
  bootstrap: GatewayBootstrapStatusSchema,
  issues: z.array(GatewayAuthHealthIssueSchema),
}).strict();

export type GatewayBootstrapStatus = z.infer<typeof GatewayBootstrapStatusSchema>;
export type GatewayAuthHealthResponse = z.infer<typeof GatewayAuthHealthResponseSchema>;
