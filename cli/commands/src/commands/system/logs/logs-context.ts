/**
 * logs context — Show full timeline for an execution/trace/request.
 * Agent-first: "What happened in this workflow run?"
 */

import {
  defineSystemCommand,
  throwSystemCommandError,
  type CommandResult,
} from "@kb-labs/shared-command-kit";
import { generateExamples } from "../../../utils/generate-examples";
import { platform } from "@kb-labs/core-runtime";
import { formatLogLine, formatLogJson } from "./logs-utils";
import type { LogRecord } from "@kb-labs/core-platform";

type Flags = {
  "execution-id": { type: "string"; description?: string };
  "trace-id": { type: "string"; description?: string };
  "request-id": { type: "string"; description?: string };
  limit: { type: "number"; description?: string };
  json: { type: "boolean"; description?: string };
};

type LogContextResult = CommandResult & {
  data: {
    correlationKey: Record<string, string | undefined>;
    timeline: { total: number; from: string | null; to: string | null; durationMs: number };
    byLevel: Record<string, number>;
    bySource: Record<string, number>;
    logs: ReturnType<typeof formatLogJson>[];
  };
  _raw?: LogRecord[];
};

export const logsContext = defineSystemCommand<Flags, LogContextResult>({
  name: "context",
  scope: 'platform',
  jsonTest: { safeArgs: ["--request-id", "__fixture_request_id__"] },
  description: "Show full timeline for an execution, trace, or request",
  category: "logs",
  examples: generateExamples("logs context", "kb", [
    {
      flags: { "execution-id": '"exec-abc123"', json: true },
      description: "Workflow execution timeline",
    },
    {
      flags: { "trace-id": '"trace-xyz"' },
      description: "Distributed trace timeline",
    },
  ]),
  flags: {
    "execution-id": { type: "string", description: "Filter by execution ID" },
    "trace-id": { type: "string", description: "Filter by trace ID" },
    "request-id": { type: "string", description: "Filter by request ID" },
    limit: { type: "number", description: "Max records (default: 200)" },
    json: { type: "boolean", description: "Output in JSON format" },
  },
  async handler(_ctx, _argv, flags) {
    const reader = platform.logs;
    if (!reader) {
      throwSystemCommandError("Log reader not available. Ensure platform is initialized.", "Run `kb logs context --help` to review valid inputs and recovery steps, then retry.");
    }

    const executionId = flags["execution-id"];
    const traceId = flags["trace-id"];
    const requestId = flags["request-id"];

    if (!executionId && !traceId && !requestId) {
      throwSystemCommandError("At least one correlation key required: --execution-id, --trace-id, or --request-id", "Run `kb logs context --help` to review valid inputs and recovery steps, then retry.");
    }

    const maxLogs = flags.limit ?? 200;
    const result = await reader.query(
      { executionId, traceId, requestId },
      { limit: maxLogs, sortOrder: "asc" },
    );
    const matched = result.logs;

    // Compute timeline metadata
    const firstTs = matched.length > 0 ? matched[0]!.timestamp : 0;
    const lastTs =
      matched.length > 0 ? matched[matched.length - 1]!.timestamp : 0;

    const levelCounts: Record<string, number> = {};
    const sourceCounts: Record<string, number> = {};
    for (const log of matched) {
      levelCounts[log.level] = (levelCounts[log.level] || 0) + 1;
      sourceCounts[log.source] = (sourceCounts[log.source] || 0) + 1;
    }

    return {
      ok: true as const,
      data: {
        correlationKey: executionId
          ? { executionId }
          : traceId
            ? { traceId }
            : { requestId },
        timeline: {
          total: matched.length,
          from: firstTs ? new Date(firstTs).toISOString() : null,
          to: lastTs ? new Date(lastTs).toISOString() : null,
          durationMs: lastTs - firstTs,
        },
        byLevel: levelCounts,
        bySource: sourceCounts,
        logs: matched.map(formatLogJson),
      },
      _raw: matched,
    };
  },
  formatter(result, ctx, flags) {
    const { data } = result;
    const { timeline, correlationKey, byLevel } = data;
    const raw = result._raw ?? [];

    // Header
    const keyStr = correlationKey
      ? Object.entries(correlationKey)
          .map(([k, v]) => `${k}=${v}`)
          .join(", ")
      : "";
    const durationStr =
      timeline && timeline.durationMs > 0
        ? `${timeline.durationMs}ms`
        : "instant";

    ctx.ui.success("Execution Context", {
      sections: [
        {
          header: `${keyStr}`,
          items: [
            `Total events: ${timeline?.total ?? 0}`,
            `Duration: ${durationStr}`,
            `Time: ${timeline?.from ?? "N/A"} → ${timeline?.to ?? "N/A"}`,
            `Levels: ${
              byLevel
                ? Object.entries(byLevel)
                    .map(([k, v]) => `${k}=${v}`)
                    .join(", ")
                : ""
            }`,
          ],
        },
      ],
    });

    // Timeline
    if (raw.length > 0) {
      ctx.ui.write("\nTimeline:\n");
      for (const log of raw) {
        ctx.ui.write("  " + formatLogLine(log) + "\n");
      }
    } else {
      ctx.ui.write("\nNo logs found for this correlation key.\n");
    }
  },
});
