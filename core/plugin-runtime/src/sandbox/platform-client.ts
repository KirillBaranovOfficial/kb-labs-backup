/** Platform proxies carried on the shared framed stdio channel. */

import type { PlatformServices } from '@kb-labs/plugin-contracts';
import { StdioTransport, type StdioChannel, createProxyPlatform } from '@kb-labs/core-ipc';

let transport: StdioTransport | null = null;

export async function connectToPlatform(channel: StdioChannel): Promise<PlatformServices> {
  transport = new StdioTransport(channel);
  return createProxyPlatform({ transport });
}

export async function disconnectFromPlatform(): Promise<void> {
  if (!transport) { return; }
  await transport.close();
  transport = null;
}
