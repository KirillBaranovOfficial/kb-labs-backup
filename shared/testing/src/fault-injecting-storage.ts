import type { IStorage } from '@kb-labs/core-platform'

export type StorageOperation =
  | { name: 'read'; path: string }
  | { name: 'write'; path: string; data: Buffer }
  | { name: 'delete'; path: string }
  | { name: 'list'; prefix: string }
  | { name: 'exists'; path: string }

/** IStorage adapter decorator for deterministic adapter-failure tests. */
export class FaultInjectingStorage implements IStorage {
  constructor(
    private readonly delegate: IStorage,
    private readonly failureFor: (operation: StorageOperation) => Error | undefined | Promise<Error | undefined>,
  ) {}

  private async fail(operation: StorageOperation): Promise<void> {
    const error = await this.failureFor(operation)
    if (error) { throw error }
  }

  async read(path: string): Promise<Buffer | null> {
    await this.fail({ name: 'read', path })
    return this.delegate.read(path)
  }

  async write(path: string, data: Buffer): Promise<void> {
    await this.fail({ name: 'write', path, data })
    return this.delegate.write(path, data)
  }

  async delete(path: string): Promise<void> {
    await this.fail({ name: 'delete', path })
    return this.delegate.delete(path)
  }

  async list(prefix: string): Promise<string[]> {
    await this.fail({ name: 'list', prefix })
    return this.delegate.list(prefix)
  }

  async exists(path: string): Promise<boolean> {
    await this.fail({ name: 'exists', path })
    return this.delegate.exists(path)
  }
}
