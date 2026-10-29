import { createRequire } from 'node:module';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

let input = '';
for await (const chunk of process.stdin) input += chunk;
const request = JSON.parse(input);
const unique = (files) => [...new Set(files.map((file) => path.resolve(file)))].sort();

if (request.tool === 'typescript') {
  const requireFromWorkspace = createRequire(path.join(request.workspaceRoot, 'package.json'));
  const ts = requireFromWorkspace('typescript');
  const result = {};
  for (const configFile of request.configFiles) {
    const parsed = ts.getParsedCommandLineOfConfigFile(configFile, {}, {
      ...ts.sys,
      onUnRecoverableConfigFileDiagnostic(diagnostic) {
        throw new Error(ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n'));
      },
    });
    if (!parsed) throw new Error(`TypeScript did not parse config ${configFile}`);
    if (parsed.errors.length > 0) {
      throw new Error(parsed.errors.map((diagnostic) => ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n')).join('\n'));
    }
    result[configFile] = unique([configFile, ...(parsed.options.configFile?.extendedSourceFiles ?? [])]);
  }
  process.stdout.write(JSON.stringify(result));
} else if (request.tool === 'vitest') {
  const requireFromPackage = createRequire(path.join(request.packageRoot, 'package.json'));
  const vitestEntry = requireFromPackage.resolve('vitest/node');
  const vitest = await import(pathToFileURL(vitestEntry).href);
  const resolved = await vitest.resolveConfig({ root: request.packageRoot, run: true }, {});
  process.stdout.write(JSON.stringify(unique(resolved.viteConfig.configFileDependencies)));
} else {
  throw new Error(`Unknown tool input reporter: ${request.tool}`);
}
