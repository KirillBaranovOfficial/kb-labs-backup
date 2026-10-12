import type { Credential } from './credentials-store.js';
import type { Membership } from './memberships-store.js';
import type { ProviderInfo } from './provider-registry.js';
import type { User, UserStatus } from './users-store.js';

export interface UsableAdminOptions {
  tenantId: string;
  providers: ProviderInfo[];
  allowedStatuses?: UserStatus[];
  requireBootstrapOwnership?: boolean;
}

export interface UsableAdminResult {
  usable: boolean;
  unavailableProviders: ProviderInfo[];
}

/**
 * The shared login predicate used by readiness and bootstrap completion.
 * Redirect providers authenticate the email and then resolve an active local
 * user in the tenant. Password providers require that provider's credential.
 */
export const isUsableAdmin = (
  user: User | null,
  credentials: Credential[],
  memberships: Membership[],
  options: UsableAdminOptions,
): UsableAdminResult => {
  const accountIsAdmin = user !== null
    && user.tenantId === options.tenantId
    && (options.allowedStatuses ?? ['active']).includes(user.status)
    && (!options.requireBootstrapOwnership || user.bootstrapManaged === true)
    && memberships.some((membership) =>
      membership.userId === user.userId
      && membership.tenantId === options.tenantId
      && membership.groupId === 'tenant-admin');
  if (!accountIsAdmin || user === null) {
    return { usable: false, unavailableProviders: [] };
  }
  const operationalProviders = options.providers.filter((provider) => provider.operational.state === 'operational');
  const usable = operationalProviders.some((provider) =>
    provider.kind === 'redirect'
    || (provider.kind === 'password'
      && credentials.some((credential) =>
        credential.userId === user.userId && credential.providerId === provider.id)));
  return {
    usable,
    unavailableProviders: usable ? [] : options.providers.filter((provider) => provider.operational.state === 'unavailable'),
  };
};
