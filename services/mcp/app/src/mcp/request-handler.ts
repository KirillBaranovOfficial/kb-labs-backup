/**
 * Request handler logic for the MCP endpoint, extracted from the Fastify/MCP-SDK
 * shell so every authorization and caching branch is unit-testable without
 * spinning up an HTTP server or a transport.
 *
 * server.ts wires these pure(ish) functions to the MCP SDK request handlers;
 * the SDK/transport plumbing itself is exercised by the e2e suite.
 */

import { createHash } from 'node:crypto';
import type { CallToolResult } from '@modelcontextprotocol/sdk/types.js';
import { createErrorEnvelope, type ICache, type IAnalytics } from '@kb-labs/core-platform';
import type { IEntityRegistry } from '@kb-labs/core-registry';
import { filterTools, type McpTool } from './tool-builder.js';
import { callTool, type PlatformResolver } from './tool-router.js';
import type { Permits } from './authz.js';
import type { McpObservabilityCollector } from '../observability/collector.js';

/** How long a per-identity tool listing stays cached. */
export const TOOLS_CACHE_TTL_MS = 60_000;

/** Build a text-only MCP tool result (the only content shape this daemon emits). */
function textResult(text: string, isError: boolean): CallToolResult {
  return { content: [{ type: 'text', text }], isError };
}

function toolError(code: string, details: Record<string, string>): CallToolResult {
  const envelope = createErrorEnvelope(code, { details });
  return {
    content: [{ type: 'text', text: `${envelope.code}: ${envelope.message} — ${envelope.hint}` }],
    structuredContent: { ...envelope },
    isError: true,
  };
}

interface JsonSchema {
  type?: string;
  required?: string[];
  properties?: Record<string, JsonSchema>;
  items?: JsonSchema;
}

function matchesSchemaType(value: unknown, type: string): boolean {
  switch (type) {
    case 'array': return Array.isArray(value);
    case 'object': return typeof value === 'object' && value !== null && !Array.isArray(value);
    case 'integer': return typeof value === 'number' && Number.isInteger(value);
    case 'number': return typeof value === 'number' && Number.isFinite(value);
    case 'boolean': return typeof value === 'boolean';
    case 'string': return typeof value === 'string';
    case 'null': return value === null;
    default: return true;
  }
}

function argumentError(schema: JsonSchema, value: unknown, path = ''): { path: string; expected: string } | null {
  if (schema.type && !matchesSchemaType(value, schema.type)) {
    return { path: path || 'arguments', expected: schema.type };
  }
  if (schema.type === 'object' && typeof value === 'object' && value !== null && !Array.isArray(value)) {
    const objectValue = value as Record<string, unknown>;
    for (const required of schema.required ?? []) {
      if (!(required in objectValue)) {
        return { path: path ? `${path}.${required}` : required, expected: 'required' };
      }
    }
    for (const [key, propertySchema] of Object.entries(schema.properties ?? {})) {
      if (key in objectValue) {
        const error = argumentError(propertySchema, objectValue[key], path ? `${path}.${key}` : key);
        if (error) return error;
      }
    }
  }
  if (schema.type === 'array' && Array.isArray(value) && schema.items) {
    for (const [index, item] of value.entries()) {
      const error = argumentError(schema.items, item, `${path}[${index}]`);
      if (error) return error;
    }
  }
  return null;
}

/**
 * Cache key for an identity's visible tool list. Authenticated identities are
 * keyed by a hash of their Authorization header (per-token, per-tenant
 * isolation); everything else shares the single anonymous bucket.
 */
export function toolsCacheKey(authHeader: string | null | undefined): string {
  if (!authHeader) {
    return 'mcp:tools:anonymous';
  }
  return `mcp:tools:${createHash('sha256').update(authHeader).digest('hex').slice(0, 16)}`;
}

export interface ResolveVisibleToolsArgs {
  /** Identity-bound authorization predicate, or null for anonymous callers. */
  permits: Permits | null;
  /** Raw Authorization header — only used to derive the per-token cache key. */
  authHeader: string | null | undefined;
  registry: IEntityRegistry;
  cache: ICache;
  /** Platform analytics — emits mcp.tools.list event. Optional for tests. */
  analytics?: IAnalytics | null;
  /** Observability collector — records mcp.tools.list operation. Optional for tests. */
  collector?: McpObservabilityCollector;
  /** Tenant ID for analytics context. */
  tenantId?: string;
}

/**
 * Resolve the tools an identity may see. Cached per identity for 60s; the cache
 * is a LISTING optimization only — it is never the authority for execution
 * (executeToolCall re-checks the live policy). Anonymous callers (permits=null)
 * always get an empty list and never touch the registry.
 */
export async function resolveVisibleTools(args: ResolveVisibleToolsArgs): Promise<McpTool[]> {
  const { permits, authHeader, registry, cache, analytics, collector, tenantId = 'anonymous' } =
    args;
  const t0 = Date.now();

  // Anonymous identities share one bucket regardless of any (invalid) header.
  const cacheKey = toolsCacheKey(permits ? authHeader : null);

  const cached = await cache.get<McpTool[]>(cacheKey).catch(() => null);
  if (cached) {
    const durationMs = Date.now() - t0;
    collector?.recordOp('mcp.tools.list', durationMs, true);
    analytics?.track('mcp.tools.list', {
      tenantId,
      toolCount: cached.length,
      cached: true,
      durationMs,
    }).catch(() => {});
    return cached;
  }

  const tools = permits ? filterTools(registry.snapshot(), permits) : [];
  await cache.set(cacheKey, tools, TOOLS_CACHE_TTL_MS).catch(() => {});

  const durationMs = Date.now() - t0;
  collector?.recordOp('mcp.tools.list', durationMs, true);
  analytics?.track('mcp.tools.list', {
    tenantId,
    toolCount: tools.length,
    cached: false,
    durationMs,
  }).catch(() => {});

  return tools;
}

export interface ExecuteToolCallArgs {
  name: string;
  args: Record<string, unknown>;
  visibleTools: McpTool[];
  /** Identity-bound predicate, or null for anonymous. */
  permits: Permits | null;
  tenantId: string;
  resolvePlatform: PlatformResolver;
  /** Platform analytics — emits mcp.tool.call events. Optional for tests. */
  analytics?: IAnalytics | null;
  /** Observability collector — records mcp.tool.call operation. Optional for tests. */
  collector?: McpObservabilityCollector;
}

/**
 * Execute a tool call. Two independent gates protect execution:
 *   1. the tool must be in the caller's visible set, and
 *   2. the LIVE policy must still permit it (re-checked here, not trusted from
 *      the possibly-stale cached listing).
 * Only then is the command run via executeCommandV3.
 */
export async function executeToolCall(args: ExecuteToolCallArgs): Promise<CallToolResult> {
  const { name, visibleTools, permits, tenantId, resolvePlatform, analytics, collector } = args;
  const t0 = Date.now();

  const tool = visibleTools.find((t) => t.name === name);
  if (!tool) {
    return toolError('KB_RUNTIME_MCP_UNKNOWN_TOOL', { tool: name });
  }

  // Authoritative gate — independent of the cached visibility list. When the
  // platform supplies a real policy, a tool that became forbidden after it was
  // cached is rejected here with no server code change.
  if (!permits || !permits(tool.operationType, tool.pluginId)) {
    return toolError('KB_RUNTIME_MCP_TOOL_FORBIDDEN', { tool: name });
  }

  const validationError = argumentError(tool.inputSchema as JsonSchema, args.args);
  if (validationError) {
    const expected = validationError.expected === 'required'
      ? 'a required value'
      : validationError.expected;
    return toolError(
      'KB_RUNTIME_MCP_INVALID_ARGUMENTS',
      { argument: validationError.path, expected },
    );
  }

  analytics?.track('mcp.tool.call.started', {
    toolName: name,
    tenantId,
    pluginId: tool.pluginId,
  }).catch(() => {});

  let result: Awaited<ReturnType<typeof callTool>>;
  try {
    result = await callTool(tool, args.args, tenantId, resolvePlatform);
  } catch {
    const durationMs = Date.now() - t0;
    collector?.recordOp('mcp.tool.call', durationMs, false);
    analytics?.track('mcp.tool.call.completed', {
      toolName: name,
      tenantId,
      pluginId: tool.pluginId,
      success: false,
      durationMs,
    }).catch(() => {});
    return toolError(
      'KB_RUNTIME_MCP_TOOL_FAILED',
      { tool: name },
    );
  }

  const durationMs = Date.now() - t0;
  collector?.recordOp('mcp.tool.call', durationMs, result.success);
  analytics?.track('mcp.tool.call.completed', {
    toolName: name,
    tenantId,
    pluginId: tool.pluginId,
    success: result.success,
    exitCode: result.exitCode,
    outputLength: result.output.length,
    durationMs,
  }).catch(() => {});

  if (!result.success) {
    return toolError(
      'KB_RUNTIME_MCP_TOOL_FAILED',
      { tool: name, exitCode: String(result.exitCode) },
    );
  }
  return textResult(result.output, false);
}
