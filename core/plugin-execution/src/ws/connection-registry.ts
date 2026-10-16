/**
 * WebSocket Connection Registry
 *
 * Tracks all active WebSocket connections for broadcast and targeted messaging.
 * Singleton pattern ensures all channels share the same connection state.
 */

import type { WebSocket } from 'ws';
import type { WSMessage } from '@kb-labs/plugin-contracts';
import { createErrorEnvelope, ErrorEnvelopeError } from '@kb-labs/core-platform';
import type { IWebSocketHostOperations } from '@kb-labs/core-platform/adapters';

/**
 * Connection metadata
 */
export interface ConnectionInfo {
  /** Unique connection ID */
  connectionId: string;
  /** Channel path (e.g., "/live", "/chat") */
  channelPath: string;
  /** WebSocket instance */
  socket: WebSocket;
  /** Tenant ID for multi-tenancy (optional) */
  tenantId?: string;
  /** Custom metadata */
  metadata: Record<string, unknown>;
  telemetry?: {
    logger: {
      info(fields: Record<string, unknown>, message?: string): void;
      warn(fields: Record<string, unknown>, message?: string): void;
    };
    stats: { outboundMessages: number; outboundBytes: number; droppedMessages: number };
  };
}

export interface DeliveryResult {
  sent: number;
  dropped: number;
}

/**
 * Connection registry for tracking and broadcasting
 */
export class ConnectionRegistry {
  private connections = new Map<string, ConnectionInfo>();
  private channelConnections = new Map<string, Set<string>>();

  private requireOpen(connectionId: string): ConnectionInfo {
    const connection = this.connections.get(connectionId);
    if (!connection) {
      throw new ErrorEnvelopeError(createErrorEnvelope('KB_RUNTIME_WS_CONNECTION_UNAVAILABLE', {
        details: { connectionId },
      }));
    }
    if (connection.socket.readyState !== 1) {
      if (connection.telemetry) {
        connection.telemetry.stats.droppedMessages += 1;
        connection.telemetry.logger.warn({
          event: 'websocket.message.dropped',
          'messaging.direction': 'outbound',
          reason: 'socket_not_open',
        }, '[ws] message dropped');
      }
      throw new ErrorEnvelopeError(createErrorEnvelope('KB_RUNTIME_WS_CONNECTION_UNAVAILABLE', {
        details: { connectionId },
      }));
    }
    return connection;
  }

  /** Host-call adapter exposed to the execution runtime through adapter:call. */
  readonly hostOperations: IWebSocketHostOperations = {
    send: async (connectionId, message) => {
      const connection = this.requireOpen(connectionId);
      const wsMessage = message as WSMessage;
      const payload = JSON.stringify(wsMessage);
      connection.socket.send(payload);
      if (connection.telemetry) {
        const payloadBytes = Buffer.byteLength(payload);
        connection.telemetry.stats.outboundMessages += 1;
        connection.telemetry.stats.outboundBytes += payloadBytes;
        connection.telemetry.logger.info({
          event: 'websocket.message.sent',
          'messaging.direction': 'outbound',
          'messaging.message_type': wsMessage.type,
          'messaging.message_id': wsMessage.messageId,
          payloadBytes,
        }, '[ws] message sent');
      }
    },
    broadcast: async (connectionId, message, excludeSelf = true) => {
      const connection = this.requireOpen(connectionId);
      const result = this.broadcast(connection.channelPath, message as WSMessage, excludeSelf ? connectionId : undefined);
      connection.telemetry?.logger.info({
        event: 'websocket.broadcast.sent',
        'messaging.direction': 'outbound',
        'messaging.message_type': (message as WSMessage).type,
        recipients: result.sent,
        droppedRecipients: result.dropped,
      }, '[ws] broadcast sent');
    },
    sendTo: async (connectionId, connectionIds, message) => {
      const connection = this.requireOpen(connectionId);
      const result = this.sendTo(connectionIds, message as WSMessage);
      connection.telemetry?.logger.info({
        event: 'websocket.message.sent_to',
        'messaging.direction': 'outbound',
        'messaging.message_type': (message as WSMessage).type,
        recipients: result.sent,
        droppedRecipients: result.dropped,
      }, '[ws] targeted messages sent');
    },
    close: async (connectionId, code, reason) => {
      const connection = this.requireOpen(connectionId);
      this.unregister(connectionId);
      connection.socket.close(code || 1000, reason);
    },
    getConnectionId: async (connectionId) => {
      this.requireOpen(connectionId);
      return connectionId;
    },
  };

  /**
   * Register a new connection
   */
  register(info: ConnectionInfo): void {
    this.connections.set(info.connectionId, info);

    if (!this.channelConnections.has(info.channelPath)) {
      this.channelConnections.set(info.channelPath, new Set());
    }
    this.channelConnections.get(info.channelPath)!.add(info.connectionId);
  }

  /**
   * Unregister a connection
   */
  unregister(connectionId: string): void {
    const info = this.connections.get(connectionId);
    if (info) {
      this.connections.delete(connectionId);
      this.channelConnections.get(info.channelPath)?.delete(connectionId);
    }
  }

  /**
   * Get connection by ID
   */
  getConnection(connectionId: string): ConnectionInfo | undefined {
    return this.connections.get(connectionId);
  }

  /**
   * Get all connections for a channel
   */
  getChannelConnections(channelPath: string): ConnectionInfo[] {
    const connectionIds = this.channelConnections.get(channelPath) || new Set();
    return Array.from(connectionIds)
      .map(id => this.connections.get(id))
      .filter((info): info is ConnectionInfo => info !== undefined);
  }

  /**
   * Broadcast message to all connections in a channel
   *
   * @param channelPath - Channel path
   * @param message - Message to broadcast
   * @param excludeConnectionId - Optional connection ID to exclude from broadcast
   */
  broadcast(channelPath: string, message: WSMessage, excludeConnectionId?: string): DeliveryResult {
    const connections = this.getChannelConnections(channelPath);
    const payload = JSON.stringify(message);

    let sent = 0;
    let dropped = 0;
    for (const conn of connections) {
      if (excludeConnectionId && conn.connectionId === excludeConnectionId) {
        continue;
      }

      // Only send if socket is open (readyState === 1)
      if (conn.socket.readyState === 1) {
        conn.socket.send(payload);
        sent += 1;
      } else {
        dropped += 1;
      }
    }
    return { sent, dropped };
  }

  /**
   * Send message to specific connections
   *
   * @param connectionIds - Array of connection IDs
   * @param message - Message to send
   */
  sendTo(connectionIds: string[], message: WSMessage): DeliveryResult {
    const payload = JSON.stringify(message);

    let sent = 0;
    let dropped = 0;
    for (const connectionId of connectionIds) {
      const conn = this.connections.get(connectionId);
      if (conn && conn.socket.readyState === 1) {
        conn.socket.send(payload);
        sent += 1;
      } else {
        dropped += 1;
      }
    }
    return { sent, dropped };
  }

  /**
   * Get registry statistics
   */
  getStats() {
    return {
      totalConnections: this.connections.size,
      channels: Array.from(this.channelConnections.entries()).map(([path, ids]) => ({
        path,
        connections: ids.size,
      })),
    };
  }
}

// Singleton instance
export const connectionRegistry = new ConnectionRegistry();
