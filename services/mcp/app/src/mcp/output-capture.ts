/**
 * AsyncLocalStorage singleton for per-call plugin output capture.
 *
 * bootstrap.ts wires a uiProvider that reads from this store so each
 * callTool() invocation captures plugin UI output into its own isolated
 * buffer — concurrent calls never mix their output.
 *
 * Usage:
 *   // producer (callTool):
 *   const output = { text: [], structured: [] };
 *   await callOutput.run(output, () => executeCommandV3(...));
 *   return output.text.join('\n');
 *
 *   // consumer (uiProvider in bootstrap):
 *   uiProvider: () => {
 *     const output = callOutput.getStore();
 *     return output ? createBufferedUI((text, model) => { ... }).ui : noopUI;
 *   }
 */

import { AsyncLocalStorage } from 'node:async_hooks';
import type { UIOutput } from '@kb-labs/plugin-contracts';

export interface CallOutput {
  text: string[];
  structured: UIOutput[];
}

export const callOutput = new AsyncLocalStorage<CallOutput>();
