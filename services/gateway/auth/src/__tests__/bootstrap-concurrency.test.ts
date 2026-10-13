import { afterEach, describe, expect, it } from 'vitest';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import bcrypt from 'bcryptjs';
import { createSqliteDocumentDatabase } from '../../../../../adapters/sqlite/src/index.js';
import { createEmailPasswordProvider } from '../providers/email-password.js';
import { evaluateAuthReadiness } from '../auth-readiness.js';
import { BootstrapCredentialConflictError, ensureBootstrapAdmin } from '../bootstrap-admin.js';
import { CredentialsStore } from '../credentials-store.js';
import { MembershipsStore } from '../memberships-store.js';
import { UsersStore } from '../users-store.js';
import type { IDocumentDatabase } from '@kb-labs/core-platform/adapters';

class PairBarrier {
  private arrivals = 0;
  private release!: () => void;
  private readonly released = new Promise<void>((resolve) => { this.release = resolve; });

  async pass(): Promise<void> {
    this.arrivals += 1;
    if (this.arrivals === 2) { this.release(); }
    await this.released;
  }
}

class FirstCallBarrier {
  private used = false;
  constructor(private readonly barrier: PairBarrier) {}
  async pass(): Promise<void> {
    if (this.used) { return; }
    this.used = true;
    await this.barrier.pass();
  }
}

class Deferred {
  private release!: () => void;
  readonly promise = new Promise<void>((resolve) => { this.release = resolve; });
  resolve(): void { this.release(); }
}

class InterleavedUsersStore extends UsersStore {
  private readonly initialLookupBarrier: FirstCallBarrier;
  private readonly activationBarrier?: FirstCallBarrier;

  constructor(docs: IDocumentDatabase, initialLookup: PairBarrier, activation?: PairBarrier) {
    super(docs);
    this.initialLookupBarrier = new FirstCallBarrier(initialLookup);
    this.activationBarrier = activation ? new FirstCallBarrier(activation) : undefined;
  }

  override async findByEmailTenant(email: string, tenantId: string) {
    const user = await super.findByEmailTenant(email, tenantId);
    await this.initialLookupBarrier.pass();
    return user;
  }

  override async activateBootstrapAdmin(userId: string, revision: number) {
    await this.activationBarrier?.pass();
    return super.activateBootstrapAdmin(userId, revision);
  }
}

class InterleavedMembershipsStore extends MembershipsStore {
  private readonly emptyReadBarrier: FirstCallBarrier;

  constructor(docs: IDocumentDatabase, emptyRead: PairBarrier) {
    super(docs);
    this.emptyReadBarrier = new FirstCallBarrier(emptyRead);
  }

  override async listByUser(userId: string) {
    const memberships = await super.listByUser(userId);
    if (memberships.length === 0) { await this.emptyReadBarrier.pass(); }
    return memberships;
  }
}

class InterleavedCredentialsStore extends CredentialsStore {
  credentialChangedAfterActivation = false;
  constructor(
    docs: IDocumentDatabase,
    private readonly beforeWrite: () => Promise<void>,
    private readonly users: UsersStore,
  ) { super(docs); }

  override async setBootstrapCredential(input: Parameters<CredentialsStore['setBootstrapCredential']>[0]) {
    await this.beforeWrite();
    const userBefore = await this.users.getById(input.userId);
    const credentialBefore = await this.getCredential(input.userId, input.providerId);
    const credential = await super.setBootstrapCredential(input);
    if (userBefore?.status === 'active' && credentialBefore?.hash !== credential.hash) {
      this.credentialChangedAfterActivation = true;
    }
    return credential;
  }
}

const logger = { info: () => undefined, warn: () => undefined, error: () => undefined };
const tenantId = 'bootstrap-concurrency';
const adminEmail = 'admin@bootstrap.test';
const providers = [{ id: 'email-password', kind: 'password' as const, operational: { state: 'operational' as const, checkedAt: 0 } }];
const roots: string[] = [];
const databases: IDocumentDatabase[] = [];

afterEach(async () => {
  await Promise.all(databases.splice(0).map((database) => database.close()));
  await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

const openSharedStores = async (interleaveActivation: boolean) => {
  const root = await mkdtemp(join(tmpdir(), 'gateway-bootstrap-race-'));
  roots.push(root);
  const filename = join(root, 'gateway.sqlite');
  const first = createSqliteDocumentDatabase({ filename, wal: false, ttlSweepIntervalMs: 0 });
  const second = createSqliteDocumentDatabase({ filename, wal: false, ttlSweepIntervalMs: 0 });
  databases.push(first, second);

  const initialLookup = new PairBarrier();
  const membershipRead = new PairBarrier();
  const credentialWrite = new PairBarrier();
  const secondCredentialGate = new Deferred();
  const activation = interleaveActivation ? new PairBarrier() : undefined;
  const stores = [first, second].map((docs, index) => {
    const users = new InterleavedUsersStore(docs, initialLookup, activation);
    return {
    users,
    credentials: new InterleavedCredentialsStore(docs, interleaveActivation
      ? () => credentialWrite.pass()
      : index === 0 ? async () => undefined : () => secondCredentialGate.promise, users),
    memberships: new InterleavedMembershipsStore(docs, membershipRead),
  }; });
  return { stores, first, second, releaseSecondCredential: () => secondCredentialGate.resolve() };
};

describe('first-admin bootstrap concurrency against shared SQLite stores', () => {
  // case: S-222.8
  it('converges for identical config and preserves one credential for conflicting passwords', async () => {
    for (const passwords of [
      ['same-bootstrap-password', 'same-bootstrap-password'],
      ['first-bootstrap-password', 'second-bootstrap-password'],
    ]) {
      const samePassword = passwords[0] === passwords[1];
      const { stores, first, releaseSecondCredential } = await openSharedStores(samePassword);
      const start = (index: number) => ensureBootstrapAdmin({
          bootstrap: { adminEmail, adminPassword: passwords[index]!, tenantId },
          ...stores[index]!,
          providers,
          bcryptCost: 4,
          logger,
        });
      let outcomes: PromiseSettledResult<Awaited<ReturnType<typeof ensureBootstrapAdmin>>>[];
      let committedCredentialHash: string | undefined;
      if (samePassword) {
        outcomes = await Promise.allSettled([start(0), start(1)]);
      } else {
        const firstRun = start(0);
        const secondRun = start(1);
        const firstOutcome = await Promise.allSettled([firstRun]);
        const completedUser = await new UsersStore(first).findByEmailTenant(adminEmail, tenantId);
        committedCredentialHash = completedUser
          ? (await new CredentialsStore(first).getCredential(completedUser.userId, 'email-password'))?.hash
          : undefined;
        releaseSecondCredential();
        const secondOutcome = await Promise.allSettled([secondRun]);
        outcomes = [firstOutcome[0]!, secondOutcome[0]!];
      }

      const users = new UsersStore(first);
      const credentials = new CredentialsStore(first);
      const memberships = new MembershipsStore(first);
      const user = await users.findByEmailTenant(adminEmail, tenantId);
      expect(user?.status).toBe('active');
      expect(await users.listByTenant(tenantId)).toHaveLength(1);
      expect(await memberships.listByUser(user!.userId)).toMatchObject([
        { tenantId, groupId: 'tenant-admin' },
      ]);
      expect(await memberships.listByUser(user!.userId)).toHaveLength(1);
      expect(stores.every((store) => !store.credentials.credentialChangedAfterActivation)).toBe(true);

      const credential = await credentials.getCredential(user!.userId, 'email-password');
      expect(credential).not.toBeNull();
      const login = createEmailPasswordProvider({ users, credentials, tenantId, bcryptCost: 4 });
      const loginResults = await Promise.all(passwords.map((password) => login.authenticate({ email: adminEmail, password })));
      expect(loginResults.filter((result) => result.ok)).toHaveLength(passwords[0] === passwords[1] ? 2 : 1);

      const readiness = await evaluateAuthReadiness({
        authEnabled: true,
        loopbackOnly: true,
        tenantId,
        jwtSecretIsDefault: false,
        bootstrap: { status: 'provisioned', email: adminEmail },
        users,
        memberships,
        credentials,
        providers,
      });
      expect(readiness).toMatchObject({ ok: true, activeAdmins: 1 });

      if (passwords[0] === passwords[1]) {
        expect(outcomes.every((outcome) => outcome.status === 'fulfilled')).toBe(true);
        expect(outcomes.map((outcome) => outcome.status === 'fulfilled' && outcome.value).sort())
          .toEqual(['exists-active', 'provisioned']);
      } else {
        const rejected = outcomes.filter((outcome) => outcome.status === 'rejected');
        expect(rejected).toHaveLength(1);
        expect(rejected[0]).toMatchObject({
          status: 'rejected',
          reason: expect.any(BootstrapCredentialConflictError),
        });
        expect(String(rejected[0]!.reason)).not.toContain(passwords[0]!);
        expect(String(rejected[0]!.reason)).not.toContain(passwords[1]!);
        const finalHash = credential!.hash;
        expect(committedCredentialHash).toBe(finalHash);
        expect(await bcrypt.compare(passwords[0]!, finalHash)
          || await bcrypt.compare(passwords[1]!, finalHash)).toBe(true);
        expect((await credentials.getCredential(user!.userId, 'email-password'))?.hash).toBe(finalHash);
      }
    }
  });
});
