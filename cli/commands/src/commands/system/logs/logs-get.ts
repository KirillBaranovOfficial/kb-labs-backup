/**
 * logs get — Get single log by ID with optional related logs
 */

import { defineSystemCommand, throwSystemCommandError,
  type CommandResult,
} from "@kb-labs/shared-command-kit";
import { generateExamples } from "../../../utils/generate-examples";
import { platform } from "@kb-labs/core-runtime";
import type { LogRecord } from "@kb-labs/core-platform";
import {
  formatLogLine,
  formatLogJson,
  findRelatedLogs,
  extractCorrelationKeys,
  type CorrelationKeys,
} from "./logs-utils";

type Flags = {
  related: { type: "boolean"; description?: string };
  json: { type: "boolean"; description?: string };
};

interface RelatedLogEntry {
  time: string;
  level: string;
  source: string;
  msg: string;
}

type LogGetResult = CommandResult & {
  data: { log: Record<string, unknown>; correlationKeys: CorrelationKeys; related?: RelatedLogEntry[] };
  _rawLog?: LogRecord;
  _rawRelated?: RelatedLogEntry[];
};

export const logsGet = defineSystemCommand<Flags, LogGetResult>({
  name: "get",
  scope: 'platform',
  jsonTest: { safeArgs: ["__fixture_log_id__"] },
  description: "Get single log by ID (with optional related logs)",
  category: "logs",
  examples: generateExamples("logs get", "kb", [
    { flags: {}, description: "<log-id>" },
    { flags: { related: true, json: true }, description: "<log-id>" },
  ]),
  flags: {
    related: {
      type: "boolean",
      description: "Include related logs (same trace/execution/request)",
    },
    json: { type: "boolean", description: "Output in JSON format" },
  },
  async handler(_ctx, argv, flags) {
    const reader = platform.logs;
    if (!reader) {
      throwSystemCommandError("Log reader not available. Ensure platform is initialized.", "Run `kb logs get --help` to review valid inputs and recovery steps, then retry.");
    }

    const id = argv[0];
    if (!id) {
      throwSystemCommandError("Log ID required. Usage: kb logs get <id>", "Run `kb logs get --help` to review valid inputs and recovery steps, then retry.");
    }

    const log = await reader.getById(id);
    if (!log) {
      throwSystemCommandError(`Log not found: ${id}`, "Run `kb logs get --help` to review valid inputs and recovery steps, then retry.");
    }

    let related: RelatedLogEntry[] | undefined;
    if (flags.related) {
      const relatedLogs = await findRelatedLogs(reader, log);
      related = relatedLogs.map(formatLogJson) as RelatedLogEntry[];
    }

    const data = {
      ok: true as const,
      data: {
        log: formatLogJson(log) as Record<string, unknown>,
        correlationKeys: extractCorrelationKeys(log),
        related,
      },
      _rawLog: log,
      _rawRelated: flags.related ? related : undefined,
    };
    return data;
  },
  formatter(result, ctx, flags) {
    const rawLog = result._rawLog;
    const logJson = result.data.log;
    const keys = result.data.correlationKeys;

    if (!rawLog || !logJson || !keys) {
      return;
    }

    // Detailed view of the log
    ctx.ui.write(formatLogLine(rawLog) + "\n\n");

    const details = [
      `ID: ${logJson["id"]}`,
      `Time: ${logJson["time"]}`,
      `Level: ${logJson["level"]}`,
      `Source: ${logJson["source"]}`,
    ];

    if (keys.traceId) {
      details.push(`Trace ID: ${keys.traceId}`);
    }
    if (keys.executionId) {
      details.push(`Execution ID: ${keys.executionId}`);
    }
    if (keys.requestId) {
      details.push(`Request ID: ${keys.requestId}`);
    }
    if (keys.sessionId) {
      details.push(`Session ID: ${keys.sessionId}`);
    }

    // Show fields
    const fieldsToShow = { ...rawLog.fields };
    delete fieldsToShow["level"];
    delete fieldsToShow["time"];
    delete fieldsToShow["traceId"];
    delete fieldsToShow["executionId"];
    delete fieldsToShow["requestId"];
    delete fieldsToShow["sessionId"];

    if (Object.keys(fieldsToShow).length > 0) {
      details.push(`Fields: ${JSON.stringify(fieldsToShow, null, 2)}`);
    }

    ctx.ui.success("Log Detail", {
      sections: [{ header: "Info", items: details }],
    });

    // Related logs
    const { related } = result.data;
    if (related && related.length > 0) {
      ctx.ui.write(`\nRelated Logs (${related.length}):\n`);
      for (const r of related) {
        ctx.ui.write(
          `  ${r.time} ${r.level.toUpperCase().padEnd(5)} [${r.source}] ${r.msg}\n`,
        );
      }
    }
  },
});
