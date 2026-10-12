/**
 * @module @kb-labs/gateway-auth/bootstrap-admin
 *
 * First-run admin provisioning (ADR-0020, Phase 1.14).
 *
 * On a fresh deployment the operator can't log in unless someone has
 * already been provisioned — and we deliberately removed the public
 * `/auth/register` for humans. `ensureBootstrapAdmin` reads bootstrap
 * config from env and creates a single tenant-admin User on first
 * start. Idempotent: subsequent runs see the existing User and do
 * nothing.
 *
 * Conflict handling is conservative: if a User with the configured
 * email already exists in the configured tenant but in a different
 * status (e.g. someone manually `disabled` them and forgot the env
 * was still set), we log a warning and **do not touch** the existing
 * record. Silently re-activating or re-passwording is the kind of
 * surprise we want to avoid on a production system.
 */

import { randomUUID } from 'node:crypto';
import bcrypt from 'bcryptjs';
import type { UsersStore } from './users-store.js';
import type { CredentialsStore } from './credentials-store.js';
import type { MembershipsStore } from './memberships-store.js';
import type { ProviderInfo } from './provider-registry.js';
import { isUsableAdmin } from './usable-admin.js';
import { isUniqueIndexConflict } from './store-errors.js';

export interface BootstrapConfig {
  adminEmail: string;
  adminPassword: string;
  tenantId: string;
}

export interface BootstrapAdminLogger {
  warn(...args: unknown[]): void;
  info(...args: unknown[]): void;
  error(...args: unknown[]): void;
}

export interface EnsureBootstrapAdminOptions {
  bootstrap: BootstrapConfig | undefined;
  users: UsersStore;
  credentials: CredentialsStore;
  memberships: MembershipsStore;
  providers: ProviderInfo[];
  bcryptCost: number;
  logger: BootstrapAdminLogger;
}

/**
 * What `ensureBootstrapAdmin` did, so the caller can report it (auth
 * readiness, startup diagnostics) without re-deriving it from the stores.
 */
export type BootstrapOutcome =
  | 'not-configured'
  | 'provisioned'
  | 'exists-active'
  | 'exists-non-active';

export class BootstrapCredentialConflictError extends Error {
  readonly code = 'bootstrap_credential_conflict';
  constructor() {
    super('bootstrap-admin: another bootstrap run committed a different password for this administrator');
    this.name = 'BootstrapCredentialConflictError';
  }
}

const isNonEmptyString = (v: unknown): v is string => typeof v === 'string' && v.length > 0;

export const ensureBootstrapAdmin = async (
  opts: EnsureBootstrapAdminOptions,
): Promise<BootstrapOutcome> => {
  const { bootstrap, users, credentials, memberships, providers, bcryptCost, logger } = opts;

  if (!bootstrap) {
    logger.info('bootstrap-admin: no bootstrap config provided, skipping');
    return 'not-configured';
  }

  if (
    !isNonEmptyString(bootstrap.adminEmail) ||
    !isNonEmptyString(bootstrap.adminPassword) ||
    !isNonEmptyString(bootstrap.tenantId)
  ) {
    throw new Error(
      'bootstrap-admin: incomplete bootstrap config (need adminEmail, adminPassword, tenantId)',
    );
  }

  let existing = await users.findByEmailTenant(bootstrap.adminEmail, bootstrap.tenantId);

  if (existing) {
    const existingCredential = await credentials.getCredential(existing.userId, 'email-password');
    const existingMemberships = await memberships.listByUser(existing.userId);
    if (isUsableAdmin(existing, existingCredential ? [existingCredential] : [], existingMemberships, {
      tenantId: bootstrap.tenantId,
      providers,
    })) {
      if (providers.some((provider) => provider.id === 'email-password' && provider.kind === 'password')
        && (!existingCredential || !await bcrypt.compare(bootstrap.adminPassword, existingCredential.hash))) {
        throw new BootstrapCredentialConflictError();
      }
      logger.info('bootstrap-admin: admin already exists and is active, skipping');
      return 'exists-active';
    }
    if (existing.status === 'active') {
      throw new Error('bootstrap-admin: existing active account is not usable; run kb-create doctor --recover');
    }
    if (!existing.bootstrapManaged || existing.status !== 'pending') {
      logger.warn(
        'bootstrap-admin: a user with the bootstrap email already exists outside an unfinished bootstrap; not touching it',
        { userId: existing.userId, status: existing.status, tenantId: existing.tenantId },
      );
      return 'exists-non-active';
    }
    return completeBootstrap(existing.userId, bootstrap, users, credentials, memberships, providers, bcryptCost);
  }

  const userId = randomUUID();
  existing = await users.createBootstrapPendingOrGet({
    userId,
    tenantId: bootstrap.tenantId,
    email: bootstrap.adminEmail,
    status: 'pending',
    bootstrapManaged: true,
  });
  if (!existing || !existing.bootstrapManaged || existing.status !== 'pending') {
    const credential = existing ? await credentials.getCredential(existing.userId, 'email-password') : null;
    const existingMemberships = existing ? await memberships.listByUser(existing.userId) : [];
    if (isUsableAdmin(existing, credential ? [credential] : [], existingMemberships, {
      tenantId: bootstrap.tenantId,
      providers,
    })) { return 'exists-active'; }
    return 'exists-non-active';
  }
  return completeBootstrap(existing.userId, bootstrap, users, credentials, memberships, providers, bcryptCost);
};

const completeBootstrap = async (
  userId: string,
  bootstrap: BootstrapConfig,
  users: UsersStore,
  credentials: CredentialsStore,
  memberships: MembershipsStore,
  providers: ProviderInfo[],
  bcryptCost: number,
): Promise<BootstrapOutcome> => {
  let revision = await users.beginBootstrapCredentialWrite(userId);
  if (revision === null) {
    return bootstrapWinnerOutcome(userId, bootstrap, users, credentials, memberships, providers, bcryptCost);
  }
  const hash = await bcrypt.hash(bootstrap.adminPassword, bcryptCost);
  await ensureAdminMembership(userId, bootstrap.tenantId, memberships);

  let user = await users.getById(userId);
  if (!user || user.status !== 'pending' || !user.bootstrapManaged) {
    throw new Error('bootstrap-admin: persisted administrator record is missing');
  }
  let credential = await credentials.setBootstrapCredential({
    userId,
    providerId: 'email-password',
    hash,
    revision,
  });
  if (!(await bcrypt.compare(bootstrap.adminPassword, credential.hash))) {
    throw new BootstrapCredentialConflictError();
  }

  user = await users.getById(userId);
  if (!user || user.status !== 'pending' || !user.bootstrapManaged) {
    return bootstrapWinnerOutcome(userId, bootstrap, users, credentials, memberships, providers, bcryptCost);
  }
  if (user.bootstrapRevision !== revision || credential.bootstrapRevision !== revision) {
    revision = user.bootstrapRevision ?? revision;
    credential = await credentials.setBootstrapCredential({
      userId,
      providerId: 'email-password',
      hash,
      revision,
    });
    if (!(await bcrypt.compare(bootstrap.adminPassword, credential.hash))) {
      throw new BootstrapCredentialConflictError();
    }
  }

  user = await users.getById(userId);
  if (!user || user.status !== 'pending' || !user.bootstrapManaged || user.bootstrapRevision !== revision
    || credential.bootstrapRevision !== revision) {
    return bootstrapWinnerOutcome(userId, bootstrap, users, credentials, memberships, providers, bcryptCost);
  }
  const allMemberships = await memberships.listByUser(userId);
  if (!isUsableAdmin(user, [credential], allMemberships, {
    tenantId: bootstrap.tenantId,
    providers: [{ id: 'email-password', kind: 'password' }],
    allowedStatuses: ['pending'],
    requireBootstrapOwnership: true,
  })) {
    throw new Error('bootstrap-admin: persisted administrator credentials or membership are incomplete');
  }
  // This compare-and-set is the one commit point. No retry writes a new hash.
  const activated = await users.activateBootstrapAdmin(userId, revision);
  if (activated) { return 'provisioned'; }

  return bootstrapWinnerOutcome(userId, bootstrap, users, credentials, memberships, providers, bcryptCost);
};

const bootstrapWinnerOutcome = async (
  userId: string,
  bootstrap: BootstrapConfig,
  users: UsersStore,
  credentials: CredentialsStore,
  memberships: MembershipsStore,
  providers: ProviderInfo[],
  bcryptCost: number,
): Promise<BootstrapOutcome> => {
  const winner = await users.getById(userId);
  const winnerCredential = await credentials.getCredential(userId, 'email-password');
  const winnerMemberships = await memberships.listByUser(userId);
  if (isUsableAdmin(winner, winnerCredential ? [winnerCredential] : [], winnerMemberships, {
    tenantId: bootstrap.tenantId,
    providers,
  })) {
    if (winnerCredential && await bcrypt.compare(bootstrap.adminPassword, winnerCredential.hash)) {
      return 'exists-active';
    }
    throw new BootstrapCredentialConflictError();
  }
  if (winner?.status === 'pending' && winner.bootstrapManaged && winner.bootstrapRevision !== undefined) {
    const hash = await bcrypt.hash(bootstrap.adminPassword, bcryptCost);
    const currentCredential = await credentials.setBootstrapCredential({
      userId,
      providerId: 'email-password',
      hash,
      revision: winner.bootstrapRevision,
    });
    if (!(await bcrypt.compare(bootstrap.adminPassword, currentCredential.hash))) {
      throw new BootstrapCredentialConflictError();
    }
    const current = await users.getById(userId);
    const allMemberships = await memberships.listByUser(userId);
    const currentRevision = current?.bootstrapRevision;
    if (current?.status === 'pending' && current.bootstrapManaged
      && currentRevision !== undefined
      && currentRevision === currentCredential.bootstrapRevision
      && isUsableAdmin(current, [currentCredential], allMemberships, {
        tenantId: bootstrap.tenantId,
        providers: [{ id: 'email-password', kind: 'password' }],
        allowedStatuses: ['pending'],
        requireBootstrapOwnership: true,
      })) {
      const activated = await users.activateBootstrapAdmin(userId, currentRevision);
      if (activated) { return 'provisioned'; }
      const finalUser = await users.getById(userId);
      const finalCredential = await credentials.getCredential(userId, 'email-password');
      const finalMemberships = await memberships.listByUser(userId);
      if (isUsableAdmin(finalUser, finalCredential ? [finalCredential] : [], finalMemberships, {
        tenantId: bootstrap.tenantId,
        providers,
      })) {
        if (finalCredential && await bcrypt.compare(bootstrap.adminPassword, finalCredential.hash)) {
          return 'exists-active';
        }
        throw new BootstrapCredentialConflictError();
      }
    }
  }
  throw new Error('bootstrap-admin: activation compare-and-set lost before the administrator became usable');
};

const ensureAdminMembership = async (
  userId: string,
  tenantId: string,
  memberships: MembershipsStore,
): Promise<void> => {
  const existing = (await memberships.listByUser(userId))
    .find((membership) => membership.tenantId === tenantId);
  if (existing?.groupId === 'tenant-admin') { return; }
  if (existing) {
    await memberships.setGroup(userId, tenantId, 'tenant-admin');
    return;
  }
  try {
    await memberships.addMembership({ userId, tenantId, groupId: 'tenant-admin' });
  } catch (error) {
    if (!isUniqueIndexConflict(error)) { throw error; }
    const raced = (await memberships.listByUser(userId))
      .find((membership) => membership.tenantId === tenantId);
    if (raced?.groupId !== 'tenant-admin') { throw error; }
  }
};
