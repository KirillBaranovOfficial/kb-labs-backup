import { execFileSync, type ExecFileSyncOptions } from 'node:child_process'

export function runProcessSync(
  executable: string,
  args: readonly string[],
  options: Omit<ExecFileSyncOptions, 'encoding'> = {},
): string {
  return execFileSync(executable, [...args], { ...options, encoding: 'utf8' })
}

export function runKbCommand(
  path: readonly string[],
  options: Omit<ExecFileSyncOptions, 'encoding'> = {},
  executable = 'kb',
): string {
  if (path.length === 0 || path.some((part) => part.length === 0)) {
    throw new Error('kb command requires a non-empty path and arguments')
  }
  return runProcessSync(executable, path, options)
}
