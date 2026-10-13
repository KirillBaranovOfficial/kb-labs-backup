import { execFileSync, type ExecFileSyncOptions } from 'node:child_process'

export function runKbCommand(
  path: readonly string[],
  options: ExecFileSyncOptions = {},
): Buffer | string {
  if (path.length === 0 || path.some((part) => part.length === 0)) {
    throw new Error('kb command requires a non-empty path and arguments')
  }
  return execFileSync('kb', [...path], options)
}
