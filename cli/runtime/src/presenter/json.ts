import type { Presenter } from "./types";
import type { SystemContext } from "../cli-context";
import { toErrorEnvelope } from '@kb-labs/core-platform';

export function createJsonPresenter(): Presenter & {
  setContext(context: SystemContext): void;
} {
  let _context: SystemContext | null = null;

  return {
    isTTY: false,
    isQuiet: false,
    isJSON: true,
    write: (_line) => {},
    info: (_line) => {},
    warn: (_line) => {},
    error: (line) => console.log(JSON.stringify({ ok: false, error: toErrorEnvelope(new Error(line)) })),
    json: (payload) => {
      console.log(JSON.stringify(payload));
      // System commands use simple JSON output
    },
    setContext(ctx: SystemContext) {
      _context = ctx;
    },
  };
}

export type JsonPresenter = ReturnType<typeof createJsonPresenter>;
