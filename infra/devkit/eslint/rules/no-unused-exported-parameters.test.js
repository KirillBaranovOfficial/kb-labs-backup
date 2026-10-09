import { Linter } from 'eslint';
import { describe, expect, it } from 'vitest';
import noUnusedExportedParameters from './no-unused-exported-parameters.js';

const linter = new Linter();

function lint(source) {
  return linter.verify(source, [{
    languageOptions: { ecmaVersion: 'latest', sourceType: 'module' },
    plugins: { 'kb-labs': { rules: { 'no-unused-exported-parameters': noUnusedExportedParameters } } },
    rules: { 'kb-labs/no-unused-exported-parameters': 'error' },
  }]);
}

describe('no-unused-exported-parameters', () => {
  it('rejects ignored leading and trailing inputs on exported functions', () => {
    const messages = lint('export function fail(_ctx, value, _isJson) { return value; }');
    expect(messages.map(({ message }) => message)).toEqual([
      "Exported parameter '_ctx' is unused; remove it from the public signature.",
      "Exported parameter '_isJson' is unused; remove it from the public signature.",
    ]);
  });

  it('checks exported function expressions, local export lists, and public class methods', () => {
    const messages = lint([
      'const local = (_ctx, value) => value;',
      'export { local };',
      'export const direct = (_json) => 1;',
      'export const expression = function (_functionConstInput) { return 1; };',
      'export class PublicApi { method(_ctx) { return 1; } }',
    ].join('\n'));
    expect(messages).toHaveLength(4);
    expect(messages.map(({ message }) => message)).toEqual([
      "Exported parameter '_ctx' is unused; remove it from the public signature.",
      "Exported parameter '_json' is unused; remove it from the public signature.",
      "Exported parameter '_functionConstInput' is unused; remove it from the public signature.",
      "Exported parameter '_ctx' is unused; remove it from the public signature.",
    ]);
  });

  it('checks public callable class fields and object properties', () => {
    const messages = lint([
      'export class Api { run = (_fieldInput) => 1; method = function (_functionInput) { return 1; }; #private = (_privateInput) => 1; }',
      'export const ApiObject = { run(_methodInput) { return 1; }, execute: (_propertyInput) => 1, nested: { call(_nestedInput) { return 1; } } };',
    ].join('\n'));
    expect(messages.map(({ message }) => message)).toEqual([
      "Exported parameter '_fieldInput' is unused; remove it from the public signature.",
      "Exported parameter '_functionInput' is unused; remove it from the public signature.",
      "Exported parameter '_methodInput' is unused; remove it from the public signature.",
      "Exported parameter '_propertyInput' is unused; remove it from the public signature.",
      "Exported parameter '_nestedInput' is unused; remove it from the public signature.",
    ]);
  });

  it('follows function aliases used by public object and class fields', () => {
    const messages = lint([
      'const handler = (_objectInput) => 1;',
      'const classHandler = (_classInput) => 1;',
      'const ListedApi = { listed(_listedInput) { return 1; } };',
      'const spreadSource = { spread(_spreadInput) { return 1; } };',
      'export const Api = { run: handler };',
      'export const SpreadApi = { ...spreadSource };',
      'export class PublicApi { run = classHandler; }',
      'export { ListedApi };',
    ].join('\n'));
    expect(messages).toHaveLength(4);
    expect(messages.map(({ message }) => message)).toEqual([
      "Exported parameter '_objectInput' is unused; remove it from the public signature.",
      "Exported parameter '_classInput' is unused; remove it from the public signature.",
      "Exported parameter '_listedInput' is unused; remove it from the public signature.",
      "Exported parameter '_spreadInput' is unused; remove it from the public signature.",
    ]);
  });

  it('allows used exported parameters and ignores unexported functions', () => {
    expect(lint([
      'export function used(ctx, value) { return ctx + value; }',
      'function internal(_ctx, value) { return value; }',
    ].join('\n'))).toEqual([]);
  });
});
