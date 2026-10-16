import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const workspaceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');

export function nativeInstallCommands({ workspace = workspaceRoot, tempRoot, platformRoot }) {
  const createPath = path.join(tempRoot, 'kb-create');
  const createModule = path.join(workspace, 'tools/kb-create');
  const indexPath = path.join(workspace, '.journey/release/journey-candidate/release-index.json');
  const installedCLI = path.join(platformRoot, 'node_modules/.bin/kb');
  return [
    { command: 'make', args: ['build', `BINARY=${createPath}`], cwd: createModule },
    {
      command: createPath,
      args: [
        'install', '--json', '--index', indexPath, '--platform-root', platformRoot,
        '--project-root', workspace, '--scenario', 'explore', '--scenario-answers', '{"access.mode":"local"}',
        '--registry', 'http://127.0.0.1:4873',
      ],
      cwd: workspace,
    },
    { command: installedCLI, args: ['--version'], cwd: workspace },
    { command: installedCLI, args: ['doctor', '--json'], cwd: workspace },
    { command: installedCLI, args: ['project', 'add', workspace, '--json'], cwd: workspace },
    { command: installedCLI, args: ['qa', 'cases', 'list', '--json'], cwd: workspace },
  ];
}

function run() {
  const tempRoot = process.env.KB_JOURNEY_NATIVE_ROOT;
  if (!tempRoot) throw new Error('KB_JOURNEY_NATIVE_ROOT is required; run e2e/journey/native-macos-smoke.sh.');
  const platformRoot = path.join(tempRoot, 'platform');
  for (const step of nativeInstallCommands({ tempRoot, platformRoot })) {
    const result = spawnSync(step.command, step.args, { cwd: step.cwd, stdio: 'inherit' });
    if (result.error) throw result.error;
    if (result.status !== 0) process.exit(result.status ?? 1);
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) run();
