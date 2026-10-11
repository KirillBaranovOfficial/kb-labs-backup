import type { ProjectServiceApiDeclaration, ServiceManifest } from "./manifest.js";

export class ProjectServiceManifestError extends Error {
  readonly code = "KB_CONFIG_INVALID";
  readonly details = { path: "serviceManifest.projectApi" };
  readonly hint = "Correct projectApi.serviceId and projectApi.rewritePrefix in the service manifest, then restart the host.";

  constructor(message: string) {
    super(message);
    this.name = "ProjectServiceManifestError";
  }
}

/** Enforce the public service namespace contract at a declaration boundary. */
export function validateProjectServiceDeclarations(
  declarations: readonly ProjectServiceApiDeclaration[],
): ProjectServiceApiDeclaration[] {
  const serviceIds = new Set<string>();
  for (const declaration of declarations) {
    if (!/^[a-z][a-z0-9-]*$/.test(declaration.serviceId)) {
      throw new ProjectServiceManifestError(
        `Invalid project service id "${declaration.serviceId}". Use a lowercase URL segment containing letters, digits, and hyphens.`,
      );
    }
    if (serviceIds.has(declaration.serviceId)) {
      throw new ProjectServiceManifestError(
        `Duplicate project service id "${declaration.serviceId}" in project service declarations.`,
      );
    }
    if (
      !declaration.rewritePrefix.startsWith("/") ||
      (declaration.rewritePrefix.endsWith("/") && declaration.rewritePrefix !== "/")
    ) {
      throw new ProjectServiceManifestError(
        `Invalid project service rewritePrefix "${declaration.rewritePrefix}". It must be an absolute path without a trailing slash.`,
      );
    }
    serviceIds.add(declaration.serviceId);
  }
  return declarations.map((declaration) => ({ ...declaration }));
}

/** Validate and collect project URL declarations from service manifests. */
export function collectProjectServiceDeclarations(
  manifests: readonly ServiceManifest[],
): ProjectServiceApiDeclaration[] {
  return validateProjectServiceDeclarations(
    manifests.flatMap((manifest) => manifest.projectApi ? [manifest.projectApi] : []),
  );
}
