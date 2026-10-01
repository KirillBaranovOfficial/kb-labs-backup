import { ulid } from 'ulid';
import type { INotifier, NotificationEvent, NotificationFilter } from '../../adapters/notifier.js';

type Subscriber = {
  filter: NotificationFilter;
  handler: (event: NotificationEvent) => Promise<void>;
};

/** Local in-process notification bus for development and single-process installs. */
export class InMemoryNotifier implements INotifier {
  private readonly subscribers = new Set<Subscriber>();

  async notify(event: Omit<NotificationEvent, 'id' | 'emittedAt'> & { source?: string }): Promise<void> {
    const complete: NotificationEvent = {
      ...event,
      id: ulid(),
      emittedAt: Date.now(),
    };
    await Promise.all(
      [...this.subscribers]
        .filter(({ filter }) =>
          (!filter.severity || filter.severity.includes(complete.severity ?? 'info')) &&
          (!filter.source || filter.source === complete.source) &&
          (!filter.predicate || filter.predicate(complete)),
        )
        .map(({ handler }) => handler(complete)),
    );
  }

  subscribe(filter: NotificationFilter, handler: (event: NotificationEvent) => Promise<void>): () => void {
    const subscriber = { filter, handler };
    this.subscribers.add(subscriber);
    return () => { this.subscribers.delete(subscriber); };
  }
}
