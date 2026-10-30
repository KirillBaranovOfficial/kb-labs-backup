/**
 * NO_COLOR follows https://no-color.org/: any non-empty value disables color.
 * FORCE_COLOR follows https://github.com/chalk/supports-color: "0" disables
 * and other non-empty values force color. NO_COLOR takes precedence when both
 * are present; without either override, TERM=dumb disables color and a TTY
 * gets color.
 */
export function resolveColorSupport(isTTY: boolean | undefined, noColor: string | undefined, forceColor: string | undefined, term?: string): boolean {
  if (noColor !== undefined && noColor !== '') return false;
  if (forceColor === '0') return false;
  if (forceColor !== undefined && forceColor !== '') return true;
  if (term?.toLowerCase() === 'dumb') return false;
  return isTTY === true;
}
