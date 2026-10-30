/**
 * Host CLI color policy shared with generated Go and POSIX shell consumers.
 * Keep the precedence and behavioral cases here; generated consumers are
 * checked by tools/clikit/ui/generate-color-policy.mjs --check.
 *
 * NO_COLOR: https://no-color.org/
 * FORCE_COLOR: https://github.com/chalk/supports-color
 */
export const colorPolicy = {
  rules: [
    { when: 'noColorNonEmpty', result: false },
    { when: 'forceColorDisabled', result: false },
    { when: 'forceColorEnabled', result: true },
    { when: 'termDumb', result: false },
    { when: 'tty', result: true },
  ],
  cases: [
    { name: 'piped default', tty: false, noColor: '', forceColor: '', term: 'xterm-256color', want: false },
    { name: 'terminal default', tty: true, noColor: '', forceColor: '', term: 'xterm-256color', want: true },
    { name: 'empty NO_COLOR terminal', tty: true, noColor: '', forceColor: '', term: 'xterm-256color', want: true },
    { name: 'forced color pipe', tty: false, noColor: '', forceColor: '1', term: 'dumb', want: true },
    { name: 'TERM dumb terminal', tty: true, noColor: '', forceColor: '', term: 'dumb', want: false },
    { name: 'NO_COLOR beats force', tty: true, noColor: '1', forceColor: '1', term: 'xterm-256color', want: false },
    { name: 'FORCE_COLOR zero', tty: true, noColor: '', forceColor: '0', term: 'xterm-256color', want: false },
  ],
};

/**
 * Resolve whether color is enabled using the ordered rules in colorPolicy.
 * @param {boolean | undefined} isTTY
 * @param {string | undefined} noColor
 * @param {string | undefined} forceColor
 * @param {string | undefined} term
 */
export function resolveColorSupport(isTTY, noColor, forceColor, term) {
  for (const rule of colorPolicy.rules) {
    const matches = rule.when === 'noColorNonEmpty' ? Boolean(noColor)
      : rule.when === 'forceColorDisabled' ? forceColor === '0'
        : rule.when === 'forceColorEnabled' ? Boolean(forceColor)
          : rule.when === 'termDumb' ? term?.toLowerCase() === 'dumb'
            : rule.when === 'tty' ? isTTY === true
              : false;
    if (matches) return rule.result;
  }
  return false;
}
