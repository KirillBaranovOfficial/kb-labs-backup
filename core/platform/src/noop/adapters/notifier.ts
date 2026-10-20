/** Throwing fallback used when a notifier is explicitly required but unavailable. */

import type { INotifier, NotificationEvent, NotificationFilter } from '../../adapters/notifier.js';
import { AdapterUnavailableError } from '../../errors.js';

export class UnavailableNotifier implements INotifier {
  async notify(_event: Omit<NotificationEvent, 'id' | 'emittedAt'> & { source?: string }): Promise<void> {
    throw new AdapterUnavailableError('notifier');
  }

  subscribe(_filter: NotificationFilter, _handler: (event: NotificationEvent) => Promise<void>): () => void {
    throw new AdapterUnavailableError('notifier');
  }
}
