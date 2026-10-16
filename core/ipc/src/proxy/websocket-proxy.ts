import type { IWebSocketHostOperations } from '@kb-labs/core-platform/adapters';
import type { ITransport } from '../transport/transport.js';
import { RemoteAdapter } from './remote-adapter.js';

export class WebSocketProxy extends RemoteAdapter<IWebSocketHostOperations> implements IWebSocketHostOperations {
  constructor(transport: ITransport) {
    super('websocket', transport);
  }

  async send(connectionId: string, message: unknown): Promise<void> {
    await this.callRemote('send', [connectionId, message]);
  }

  async broadcast(connectionId: string, message: unknown, excludeSelf?: boolean): Promise<void> {
    await this.callRemote('broadcast', [connectionId, message, excludeSelf]);
  }

  async sendTo(connectionId: string, connectionIds: string[], message: unknown): Promise<void> {
    await this.callRemote('sendTo', [connectionId, connectionIds, message]);
  }

  async close(connectionId: string, code?: number, reason?: string): Promise<void> {
    await this.callRemote('close', [connectionId, code, reason]);
  }

  async getConnectionId(connectionId: string): Promise<string> {
    return (await this.callRemote('getConnectionId', [connectionId])) as string;
  }
}
