import type { Credential } from './credentials-store.js';
import type { Membership } from './memberships-store.js';
import type { User } from './users-store.js';

export const isUsableActiveAdmin = (
  user: User | null,
  credential: Credential | null,
  memberships: Membership[],
  tenantId: string,
): boolean => Boolean(
  user !== null
  && user.tenantId === tenantId
  && user.status === 'active'
  && credential !== null
  && credential.userId === user.userId
  && credential.providerId === 'email-password'
  && memberships.some((membership) =>
    membership.userId === user.userId
    && membership.tenantId === tenantId
    && membership.groupId === 'tenant-admin'),
);
