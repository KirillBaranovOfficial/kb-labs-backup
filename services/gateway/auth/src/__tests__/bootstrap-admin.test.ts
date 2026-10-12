/**
 * Tests for bootstrap-admin (ADR-0020, Phase 1.14).
 *
 * On the very first start of a fresh deployment, gateway provisions a
 * tenant-admin user from env / config so the operator can actually log
 * in and invite their team.
 *
 * Invariants:
 * - Idempotent. Running `ensureBootstrapAdmin` three times against the
 *   same DB must leave one User, one Credential, one Membership.
 * - No-op when bootstrap config is absent.
 * - No-op (with a logged warning) when a User already exists with that
 *   `(email, tenantId)` but a different status. We never silently
 *   re-activate or re-password an existing account.
 */

import { describe, it, expect, beforeEach } from 'vitest';
import bcrypt from 'bcryptjs';
import { createInMemoryDocumentDatabase } from '@kb-labs/sdk/testing';
import { UsersStore } from '../users-store.js';
import { CredentialsStore } from '../credentials-store.js';
import { MembershipsStore } from '../memberships-store.js';
import { BootstrapCredentialConflictError, ensureBootstrapAdmin } from '../bootstrap-admin.js';
import { evaluateAuthReadiness } from '../auth-readiness.js';
import { createEmailPasswordProvider } from '../providers/email-password.js';

let docs: ConstructorParameters<typeof UsersStore>[0];
let users: UsersStore;
let credentials: CredentialsStore;
let memberships: MembershipsStore;
let warnings: unknown[];
let logRecords: unknown[];

beforeEach(() => {
  docs = createInMemoryDocumentDatabase();
  users = new UsersStore(docs);
  credentials = new CredentialsStore(docs);
  memberships = new MembershipsStore(docs);
  warnings = [];
  logRecords = [];
});

class InterruptUsersStore extends UsersStore {
  constructor(docs: ConstructorParameters<typeof UsersStore>[0], private readonly interruptAfter: 'user' | 'activation' | null) {
    super(docs);
  }
  override async create(input: Parameters<UsersStore['create']>[0]) {
    const user = await super.create(input);
    if (this.interruptAfter === 'user') { throw new Error('injected user write interruption'); }
    return user;
  }
  override async activateBootstrapAdmin(userId: string, revision: number) {
    const user = await super.activateBootstrapAdmin(userId, revision);
    if (this.interruptAfter === 'activation') { throw new Error('injected activation write interruption'); }
    return user;
  }
}

class InterruptCredentialsStore extends CredentialsStore {
  constructor(docs: ConstructorParameters<typeof UsersStore>[0], private readonly interruptAfterCredential: boolean) {
    super(docs);
  }
  override async setBootstrapCredential(input: Parameters<CredentialsStore['setBootstrapCredential']>[0]) {
    const credential = await super.setBootstrapCredential(input);
    if (this.interruptAfterCredential) { throw new Error('injected credential write interruption'); }
    return credential;
  }
}

class InterruptMembershipsStore extends MembershipsStore {
  constructor(docs: ConstructorParameters<typeof UsersStore>[0], private readonly interruptAfterMembership: boolean) {
    super(docs);
  }
  override async addMembership(input: Parameters<MembershipsStore['addMembership']>[0]) {
    await super.addMembership(input);
    if (this.interruptAfterMembership) { throw new Error('injected membership write interruption'); }
  }
}

const logger = {
  warn: (...args: unknown[]) => { warnings.push(args); logRecords.push(args); },
  info: (...args: unknown[]) => { logRecords.push(args); },
  error: (...args: unknown[]) => { logRecords.push(args); },
};

const cfg = {
  adminEmail: 'admin@kblabs.ru',
  adminPassword: 'super-long-bootstrap-pw',
  tenantId: 'kblabs-cloud',
};

describe('happy path', () => {
  it('creates user(active) + credential + membership(tenant-admin) on first call', async () => {
    await ensureBootstrapAdmin({
      bootstrap: cfg,
      users,
      credentials,
      memberships,
      providers: [{ id: 'email-password', kind: 'password' }],
      bcryptCost: 4,
      logger,
    });
    const u = await users.findByEmailTenant(cfg.adminEmail, cfg.tenantId);
    expect(u).not.toBeNull();
    expect(u!.status).toBe('active');

    const cred = await credentials.getCredential(u!.userId, 'email-password');
    expect(cred).not.toBeNull();
    // Stored hash verifies the input password (so we know the cost was
    // applied correctly and we are not storing plaintext).
    const ok = await bcrypt.compare(cfg.adminPassword, cred!.hash);
    expect(ok).toBe(true);

    const mem = await memberships.listByUser(u!.userId);
    expect(mem).toHaveLength(1);
    expect(mem[0]).toMatchObject({ tenantId: cfg.tenantId, groupId: 'tenant-admin' });
  });
});

describe('idempotency', () => {
  it('three consecutive runs yield exactly one admin', async () => {
    for (let i = 0; i < 3; i++) {
      await ensureBootstrapAdmin({ bootstrap: cfg, users, credentials, memberships, providers: [{ id: 'email-password', kind: 'password' }], bcryptCost: 4, logger });
    }
    const u = await users.findByEmailTenant(cfg.adminEmail, cfg.tenantId);
    expect(u).not.toBeNull();
    const originalCredential = await credentials.getCredential(u!.userId, 'email-password');
    await expect(ensureBootstrapAdmin({
      bootstrap: { ...cfg, adminPassword: 'different-bootstrap-password' },
      users,
      credentials,
      memberships,
      providers: [{ id: 'email-password', kind: 'password' }],
      bcryptCost: 4,
      logger,
    })).rejects.toBeInstanceOf(BootstrapCredentialConflictError);
    const allMem = await memberships.listByUser(u!.userId);
    expect(allMem).toHaveLength(1);
    expect(warnings.length).toBe(0); // no warnings on idempotent reruns
    expect(await credentials.getCredential(u!.userId, 'email-password')).toEqual(originalCredential);
    const auth = createEmailPasswordProvider({ users, credentials, tenantId: cfg.tenantId, bcryptCost: 4 });
    await expect(auth.authenticate({ email: cfg.adminEmail, password: cfg.adminPassword }))
      .resolves.toMatchObject({ ok: true, email: cfg.adminEmail });
  });
});

describe('interrupted bootstrap recovery', () => {
  // case: S-222.5
  it('restart converges after failures following each persisted bootstrap write', async () => {
    for (const failedWrite of ['user', 'credential', 'membership', 'activation'] as const) {
      docs = createInMemoryDocumentDatabase();
      const interruptedUsers = new InterruptUsersStore(
        docs,
        failedWrite === 'user' || failedWrite === 'activation' ? failedWrite : null,
      );
      const interruptedCredentials = new InterruptCredentialsStore(docs, failedWrite === 'credential');
      const interruptedMemberships = new InterruptMembershipsStore(docs, failedWrite === 'membership');

      await expect(ensureBootstrapAdmin({
        bootstrap: cfg,
        users: interruptedUsers,
        credentials: interruptedCredentials,
        memberships: interruptedMemberships,
        providers: [{ id: 'email-password', kind: 'password' }],
        bcryptCost: 4,
        logger,
      })).rejects.toThrow('injected');

      const restartedUsers = new UsersStore(docs);
      const restartedCredentials = new CredentialsStore(docs);
      const restartedMemberships = new MembershipsStore(docs);
      await ensureBootstrapAdmin({
        bootstrap: cfg,
        users: restartedUsers,
        credentials: restartedCredentials,
        memberships: restartedMemberships,
        providers: [{ id: 'email-password', kind: 'password' }],
        bcryptCost: 4,
        logger,
      });

      const user = await restartedUsers.findByEmailTenant(cfg.adminEmail, cfg.tenantId);
      expect(user).toMatchObject({ status: 'active' });
      expect(await restartedUsers.listByTenant(cfg.tenantId)).toHaveLength(1);
      const adminMemberships = (await restartedMemberships.listByUser(user!.userId))
        .filter((membership) => membership.tenantId === cfg.tenantId && membership.groupId === 'tenant-admin');
      expect(adminMemberships).toHaveLength(1);
      const auth = createEmailPasswordProvider({
        users: restartedUsers,
        credentials: restartedCredentials,
        tenantId: cfg.tenantId,
        bcryptCost: 4,
      });
      await expect(auth.authenticate({ email: cfg.adminEmail, password: cfg.adminPassword }))
        .resolves.toMatchObject({ ok: true, email: cfg.adminEmail });
      await expect(evaluateAuthReadiness({
        authEnabled: true,
        loopbackOnly: false,
        tenantId: cfg.tenantId,
        jwtSecretIsDefault: false,
        bootstrap: { status: 'exists-active', email: cfg.adminEmail },
        users: restartedUsers,
        credentials: restartedCredentials,
        memberships: restartedMemberships,
        providers: [{ id: 'email-password', kind: 'password' }],
      })).resolves.toMatchObject({ ok: true, activeAdmins: 1 });
      expect(JSON.stringify(logRecords)).not.toContain(cfg.adminPassword);
    }
  });

  // case: S-222.8
  it('a sequential pending restart adopts the new password before activation', async () => {
    const interruptedCredentials = new InterruptCredentialsStore(docs, true);
    await expect(ensureBootstrapAdmin({
      bootstrap: cfg,
      users,
      credentials: interruptedCredentials,
      memberships,
      providers: [{ id: 'email-password', kind: 'password' }],
      bcryptCost: 4,
      logger,
    })).rejects.toThrow('injected credential write interruption');

    const changed = { ...cfg, adminPassword: 'replacement-bootstrap-password' };
    await ensureBootstrapAdmin({
      bootstrap: changed,
      users,
      credentials,
      memberships,
      providers: [{ id: 'email-password', kind: 'password' }],
      bcryptCost: 4,
      logger,
    });
    const provider = createEmailPasswordProvider({
      users,
      credentials,
      tenantId: cfg.tenantId,
      bcryptCost: 4,
    });
    await expect(provider.authenticate({ email: cfg.adminEmail, password: changed.adminPassword }))
      .resolves.toMatchObject({ ok: true, email: cfg.adminEmail });
    await expect(provider.authenticate({ email: cfg.adminEmail, password: cfg.adminPassword }))
      .resolves.toMatchObject({ ok: false });
  });
});

describe('absent bootstrap config', () => {
  it('is a no-op when bootstrap is undefined', async () => {
    await ensureBootstrapAdmin({
      bootstrap: undefined,
      users, credentials, memberships, providers: [{ id: 'email-password', kind: 'password' }], bcryptCost: 4, logger,
    });
    expect(await users.findByEmailTenant(cfg.adminEmail, cfg.tenantId)).toBeNull();
  });

  it('throws on partial bootstrap (missing email / password / tenant)', async () => {
    await expect(
      ensureBootstrapAdmin({
        bootstrap: { adminEmail: cfg.adminEmail, adminPassword: '', tenantId: cfg.tenantId },
        users, credentials, memberships, providers: [{ id: 'email-password', kind: 'password' }], bcryptCost: 4, logger,
      }),
    ).rejects.toThrow();
  });
});

describe('conflicting state', () => {
  it('logs a warning and does not touch a pre-existing user with another status', async () => {
    // Operator created an account manually with status: disabled and re-set the env.
    await users.create({
      userId: 'pre-existing',
      tenantId: cfg.tenantId,
      email: cfg.adminEmail,
      status: 'disabled',
    });
    await ensureBootstrapAdmin({ bootstrap: cfg, users, credentials, memberships, providers: [{ id: 'email-password', kind: 'password' }], bcryptCost: 4, logger });
    const u = await users.findByEmailTenant(cfg.adminEmail, cfg.tenantId);
    expect(u!.userId).toBe('pre-existing');
    expect(u!.status).toBe('disabled');
    expect(warnings.length).toBeGreaterThan(0);
    // No credential was set.
    expect(await credentials.getCredential('pre-existing', 'email-password')).toBeNull();
  });
});

describe('returned outcome', () => {
  const run = (bootstrap: typeof cfg | undefined) =>
    ensureBootstrapAdmin({ bootstrap, users, credentials, memberships, providers: [{ id: 'email-password', kind: 'password' }], bcryptCost: 4, logger });

  it("'not-configured' when there is no bootstrap config", async () => {
    expect(await run(undefined)).toBe('not-configured');
  });

  it("'provisioned' on first run, then 'exists-active'", async () => {
    expect(await run(cfg)).toBe('provisioned');
    expect(await run(cfg)).toBe('exists-active');
  });

  it("'exists-non-active' when the existing admin is disabled", async () => {
    await run(cfg);
    const user = await users.findByEmailTenant(cfg.adminEmail, cfg.tenantId);
    await users.setStatus(user!.userId, 'disabled');
    expect(await run(cfg)).toBe('exists-non-active');
  });
});
