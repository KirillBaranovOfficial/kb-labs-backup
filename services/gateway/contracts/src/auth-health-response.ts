import { z } from 'zod';
import { gatewayAuthHealthIssueSeverities, gatewayBootstrapStatuses } from './auth-health-response.enums.js';

export const GatewayBootstrapStatusSchema = z.enum(gatewayBootstrapStatuses);

export const GatewayAuthHealthIssueSchema = z.object({
  code: z.string(),
  severity: z.enum(gatewayAuthHealthIssueSeverities),
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
