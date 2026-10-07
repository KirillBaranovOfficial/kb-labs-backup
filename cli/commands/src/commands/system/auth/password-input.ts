import type { Readable } from 'node:stream';

export async function readPasswordFromStdin(input: Readable = process.stdin): Promise<string> {
  let value = '';
  for await (const chunk of input) {
    value += typeof chunk === 'string' ? chunk : Buffer.from(chunk).toString('utf8');
  }
  return value.replace(/\r?\n$/, '');
}

export async function promptForPassword(): Promise<string> {
  const input = process.stdin;
  if (!input.isTTY || !input.setRawMode || !process.stderr.isTTY) {
    throw new Error('A password is required when stdin is not a terminal.');
  }
  process.stderr.write('Password: ');
  input.setRawMode(true);
  input.setEncoding('utf8');
  input.resume();
  return new Promise((resolve, reject) => {
    let password = '';
    const cleanup = () => {
      input.off('data', onData);
      input.setRawMode(false);
      input.pause();
      process.stderr.write('\n');
    };
    const onData = (chunk: string) => {
      for (const character of chunk) {
        if (character === '\u0003') {
          cleanup();
          reject(new Error('Password entry was cancelled.'));
          return;
        }
        if (character === '\r' || character === '\n') {
          cleanup();
          resolve(password);
          return;
        }
        if (character === '\u007f' || character === '\b') {
          password = password.slice(0, -1);
          continue;
        }
        password += character;
      }
    };
    input.on('data', onData);
  });
}
