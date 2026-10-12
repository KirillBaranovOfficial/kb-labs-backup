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
import { isUsableActiveAdmin } from './usable-admin.js';

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

const isNonEmptyString = (v: unknown): v is string => typeof v === 'string' && v.length > 0;

export const ensureBootstrapAdmin = async (
  opts: EnsureBootstrapAdminOptions,
): Promise<BootstrapOutcome> => {
  const { bootstrap, users, credentials, memberships, bcryptCost, logger } = opts;

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

  const existing = await users.findByEmailTenant(bootstrap.adminEmail, bootstrap.tenantId);

  if (existing) {
    const existingCredential = await credentials.getCredential(existing.userId, 'email-password');
    const existingMemberships = await memberships.listByUser(existing.userId);
    if (isUsableActiveAdmin(existing, existingCredential, existingMemberships, bootstrap.tenantId)) {
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
    await completeBootstrap(existing.userId, bootstrap, users, credentials, memberships, bcryptCost);
    logger.info('bootstrap-admin: completed interrupted tenant-admin bootstrap', {
      userId: existing.userId,
      tenantId: existing.tenantId,
    });
    return 'provisioned';
  }

  const userId = randomUUID();
  await users.create({
    userId,
    tenantId: bootstrap.tenantId,
    email: bootstrap.adminEmail,
    status: 'pending',
    bootstrapManaged: true,
  });
  await completeBootstrap(userId, bootstrap, users, credentials, memberships, bcryptCost);

  logger.info('bootstrap-admin: provisioned tenant-admin', {
    userId,
    tenantId: bootstrap.tenantId,
  });
  return 'provisioned';
};

const completeBootstrap = async (
  userId: string,
  bootstrap: BootstrapConfig,
  users: UsersStore,
  credentials: CredentialsStore,
  memberships: MembershipsStore,
  bcryptCost: number,
): Promise<void> => {
  const hash = await bcrypt.hash(bootstrap.adminPassword, bcryptCost);
  await credentials.setCredential({ userId, providerId: 'email-password', hash });

  const existingMembership = (await memberships.listByUser(userId))
    .find((membership) => membership.tenantId === bootstrap.tenantId);
  if (existingMembership && existingMembership.groupId !== 'tenant-admin') {
    await memberships.setGroup(userId, bootstrap.tenantId, 'tenant-admin');
  } else if (!existingMembership) {
    await memberships.addMembership({ userId, tenantId: bootstrap.tenantId, groupId: 'tenant-admin' });
  }

  const user = await users.getById(userId);
  if (!user) {
    throw new Error('bootstrap-admin: persisted administrator record is missing');
  }
  const credential = await credentials.getCredential(userId, 'email-password');
  const allMemberships = await memberships.listByUser(userId);
  if (!isUsableActiveAdmin({ ...user, status: 'active' }, credential, allMemberships, bootstrap.tenantId)) {
    throw new Error('bootstrap-admin: persisted administrator credentials or membership are incomplete');
  }
  // Activation is the commit point: readiness and login ignore pending users.
  await users.setStatus(userId, 'active');
};
