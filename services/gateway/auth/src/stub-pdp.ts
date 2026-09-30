/**
 * @module @kb-labs/gateway-auth/stub-pdp
 *
 * Role policy over the memberships store. Administrators receive the
 * canonical platform permissions; members receive project use, plugin,
 * workflow, and own-account permissions only.
 *
 * Closed-world: actions outside the canonical PERMISSIONS enum return
 * `allow: false` for every identity.
 */

import type {
  Identity,
  IPolicyDecisionPoint,
  PolicyDecision,
  Resource,
  PolicyContext,
} from '@kb-labs/core-contracts';
import { PERMISSIONS } from '@kb-labs/core-contracts';
import type { MembershipsStore, GroupId } from './memberships-store.js';

const ALL_PERMISSIONS: ReadonlyArray<string> = Object.freeze(Object.values(PERMISSIONS));
const MACHINE_PERMISSIONS: ReadonlyArray<string> = Object.freeze([PERMISSIONS.MACHINE_REGISTER]);
const MEMBER_PERMISSIONS: ReadonlyArray<string> = Object.freeze([
  PERMISSIONS.PROJECTS_READ,
  PERMISSIONS.PROJECTS_WRITE,
  PERMISSIONS.PLUGINS_READ,
  PERMISSIONS.PLUGINS_WRITE,
  PERMISSIONS.WORKFLOWS_READ,
  PERMISSIONS.WORKFLOWS_WRITE,
  PERMISSIONS.ACCOUNT_READ,
  PERMISSIONS.ACCOUNT_WRITE,
]);

const GROUP_PERMISSIONS: Readonly<Record<GroupId, ReadonlyArray<string>>> = Object.freeze({
  'tenant-admin': ALL_PERMISSIONS,
  'tenant-member': MEMBER_PERMISSIONS,
});

export interface StubPDPOptions {
  memberships: MembershipsStore;
}

export const createStubPDP = (opts: StubPDPOptions): IPolicyDecisionPoint => {
  const permissionsForUser = async (identity: Identity): Promise<ReadonlyArray<string>> => {
    const list = await opts.memberships.listByUser(identity.userId);
    const inTenant = list.find(m => m.tenantId === identity.tenantId);
    if (!inTenant) {return [];}
    return GROUP_PERMISSIONS[inTenant.groupId];
  };

  return {
    async check(
      identity: Identity,
      action: string,
      _resource?: Resource,
      _ctx?: PolicyContext,
    ): Promise<PolicyDecision> {
      if (identity.type === 'machine') {
        return MACHINE_PERMISSIONS.includes(action)
          ? { allow: true }
          : { allow: false, reason: 'permission_denied' };
      }

      const perms = await permissionsForUser(identity);
      if (perms.length === 0) {
        return { allow: false, reason: 'no_membership' };
      }
      if (!perms.includes(action)) {
        return { allow: false, reason: 'permission_denied' };
      }
      return { allow: true };
    },

    async enumeratePermissions(identity: Identity): Promise<string[]> {
      if (identity.type === 'machine') {
        return [...MACHINE_PERMISSIONS];
      }
      const perms = await permissionsForUser(identity);
      return [...perms];
    },
  };
};
