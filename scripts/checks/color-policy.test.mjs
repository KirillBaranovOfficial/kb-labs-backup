import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const cases = JSON.parse(readFileSync(path.join(root, 'tools/clikit/ui/color-cases.json'), 'utf8'));

function invokeInstaller(installer, testCase) {
  const env = { ...process.env };
  delete env.NO_COLOR;
  delete env.FORCE_COLOR;
  env.TERM = testCase.term ?? 'xterm-256color';
  if (testCase.noColor !== '') env.NO_COLOR = testCase.noColor;
  if (testCase.forceColor !== '') env.FORCE_COLOR = testCase.forceColor;

  const ptyDriver = `import os, pty, subprocess, sys
master, slave = pty.openpty()
process = subprocess.Popen(["sh", sys.argv[1], "--unknown"], stdin=slave, stdout=slave, stderr=slave)
os.close(slave)
chunks = []
while True:
    try:
        chunk = os.read(master, 4096)
    except OSError:
        break
    if not chunk:
        break
    chunks.append(chunk)
os.close(master)
sys.stdout.buffer.write(b"".join(chunks))
sys.exit(process.wait())`;
  const result = testCase.tty
    ? spawnSync('python3', ['-c', ptyDriver, installer], { cwd: root, env, encoding: 'utf8' })
    : spawnSync('sh', [installer, '--unknown'], { cwd: root, env, encoding: 'utf8' });
  assert.equal(result.status, 1, result.stderr || result.stdout);
  return `${result.stdout}${result.stderr}`.includes('\u001b[');
}

for (const installer of ['tools/kb-dev/install.sh', 'tools/kb-deploy/install.sh']) {
  for (const testCase of cases) {
    test(`${installer} color policy: ${testCase.name}`, () => {
      assert.equal(invokeInstaller(path.join(root, installer), testCase), testCase.want);
    });
  }
}
