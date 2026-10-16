/** Host-owned WebSocket operations used by plugin runtime senders. */
export interface IWebSocketHostOperations {
  send(connectionId: string, message: unknown): Promise<void> | void;
  broadcast(connectionId: string, message: unknown, excludeSelf?: boolean): Promise<void> | void;
  sendTo(connectionId: string, connectionIds: string[], message: unknown): Promise<void> | void;
  close(connectionId: string, code?: number, reason?: string): Promise<void> | void;
  getConnectionId(connectionId: string): Promise<string> | string;
}
