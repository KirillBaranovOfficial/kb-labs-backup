import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import ts from 'typescript';
import { describe, expect, it } from 'vitest';

const workspaceRoot = path.resolve(import.meta.dirname, '../../../..');
const pluginsRoot = path.join(workspaceRoot, 'plugins');

function sourceFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const fullPath = path.join(directory, entry.name);
    if (entry.isDirectory()) return sourceFiles(fullPath);
    return fullPath.endsWith('.ts') && !fullPath.endsWith('.d.ts') ? [fullPath] : [];
  });
}

function property(node: ts.ObjectLiteralExpression, name: string): ts.Expression | undefined {
  const match = node.properties.find((item): item is ts.PropertyAssignment =>
    ts.isPropertyAssignment(item) &&
    ((ts.isIdentifier(item.name) && item.name.text === name) ||
      (ts.isStringLiteral(item.name) && item.name.text === name)),
  );
  return match?.initializer;
}

function registeredCliHandlers(): string[] {
  const handlers = new Set<string>();
  for (const manifestPath of sourceFiles(pluginsRoot).filter((file) => /\/manifest(?:\.v3)?\.ts$/.test(file))) {
    const source = ts.createSourceFile(manifestPath, readFileSync(manifestPath, 'utf8'), ts.ScriptTarget.Latest, true);
    const visit = (node: ts.Node): void => {
      if (ts.isObjectLiteralExpression(node)) {
        const cliValue = property(node, 'cli');
        if (cliValue && ts.isObjectLiteralExpression(cliValue)) {
          const commandsValue = property(cliValue, 'commands');
          if (commandsValue && ts.isArrayLiteralExpression(commandsValue)) {
            for (const command of commandsValue.elements) {
              if (!ts.isObjectLiteralExpression(command)) continue;
              const handler = property(command, 'handler');
              if (handler && ts.isStringLiteral(handler)) {
                const relative = handler.text.split('#')[0]!;
                handlers.add(path.resolve(path.dirname(manifestPath), relative.replace(/\.js$/, '.ts')));
              }
            }
          }
        }
      }
      if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 'cmd') {
        const handler = node.arguments[1];
        if (handler && ts.isStringLiteral(handler) && handler.text.startsWith('./') && handler.text.endsWith('.js#default')) {
          handlers.add(path.resolve(path.dirname(manifestPath), handler.text.replace(/\.js#default$/, '.ts')));
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(source);
  }
  return [...handlers].sort();
}

describe('registered plugin CLI handler result contract', () => {
  // case: S-203.1, S-203.2
  it('discovers handlers from every plugin manifest and prevents direct JSON or string failures', () => {
    const handlers = registeredCliHandlers();
    expect(handlers.length).toBeGreaterThan(0);

    const violations = handlers.flatMap((handler) => {
      if (!handler.startsWith(pluginsRoot) || !statSync(handler, { throwIfNoEntry: false })?.isFile()) {
        return [`${handler}: registered CLI handler source is missing`];
      }
      const source = readFileSync(handler, 'utf8');
      const findings: string[] = [];
      if (/\b(?:ctx|context|ui)\s*(?:\?\.)?\s*(?:\.\s*ui\s*)?(?:\?\.|\.)\s*json\s*(?:\?\.)?\s*\(/.test(source)) findings.push(`${handler}: calls UI json`);
      if (/\b(?:ctx|context|ui)\s*(?:\?\.)?\s*(?:\.\s*ui\s*)?(?:\?\.|\.)\s*error\s*(?:\?\.)?\s*\(/.test(source)) findings.push(`${handler}: calls UI error`);
      if (/\bok\s*:\s*false\s*,\s*error\s*:\s*(['"`])/.test(source)) findings.push(`${handler}: returns a string error`);
      const handlerSource = ts.createSourceFile(handler, source, ts.ScriptTarget.Latest, true);
      const inspectReturnedFailures = (node: ts.Node): void => {
        if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) &&
          ['handleError', 'validationError'].includes(node.expression.text) &&
          ts.isExpressionStatement(node.parent)) {
          findings.push(`${handler}: discards typed ${node.expression.text} result`);
        }
        ts.forEachChild(node, inspectReturnedFailures);
      };
      inspectReturnedFailures(handlerSource);
      return findings;
    });

    expect(violations).toEqual([]);
  });
});
