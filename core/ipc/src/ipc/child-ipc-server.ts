/**
 * @module @kb-labs/core-ipc/ipc/child-ipc-server
 *
 * IPC server for handling adapter calls from a specific child process.
 *
 * Unlike IPCServer (which listens on `process.on('message')`), ChildIPCServer
 * listens on `child.on('message')` — designed for worker-pool where the parent
 * is the main process managing multiple child workers.
 *
 * Includes Layer 2 permission enforcement: checks `call.context.permissions`
 * before dispatching to real adapters. Stateless — permissions travel with each call.
 */

import type { ChildProcess } from 'node:child_process';
import type { IPlatformAdapters } from '@kb-labs/core-platform';
import { PlatformRPCDispatcher } from './platform-rpc-dispatcher.js';

/**
 * ChildIPCServer — parent-side adapter call handler for a single child process.
 *
 * Responsibilities:
 * - Listen for `adapter:call` messages on child.on('message')
 * - Layer 2 permission check (adapter-level gate from call.context.permissions)
 * - Dispatch to real adapter on platform
 * - Send `adapter:response` back via child.send()
 * - Auto-stop on child exit
 *
 * Message coexistence: WorkerMessage types (execute, result, error, log, health, ready)
 * have different `type` fields than AdapterCall ('adapter:call') / AdapterResponse
 * ('adapter:response'). Both coexist on the same IPC channel without collision.
 */
export class ChildIPCServer {
  private messageHandler: (msg: unknown) => void;
  private exitHandler: () => void;
  private started = false;
  private readonly dispatcher: PlatformRPCDispatcher;

  constructor(
    private readonly platform: IPlatformAdapters,
    private readonly child: ChildProcess,
  ) {
    this.messageHandler = this.handleMessage.bind(this);
    this.exitHandler = () => this.stop();
    this.dispatcher = new PlatformRPCDispatcher(platform, (message) => {
      if (this.child.connected) { this.child.send(message); }
    });
  }

  /**
   * Start listening for adapter calls from child process.
   */
  start(): void {
    if (this.started) { return; }

    this.child.on('message', this.messageHandler);
    this.child.on('exit', this.exitHandler);
    this.started = true;
  }

  /**
   * Stop listening. Removes all listeners and cancels all EventBus subscriptions.
   */
  stop(): void {
    if (!this.started) {return;}

    this.child.off('message', this.messageHandler);
    this.child.off('exit', this.exitHandler);
    this.started = false;

    this.dispatcher.close();
  }

  /**
   * Handle incoming message from child.
   * Routes adapter:call, eventbus:subscribe, eventbus:unsubscribe.
   * WorkerMessages (execute, result, etc.) pass through untouched.
   */
  private handleMessage(msg: unknown): void {
    void this.dispatcher.dispatch(msg);
  }

  isStarted(): boolean {
    return this.started;
  }
}
