import { createErrorEnvelope } from "@kb-labs/core-platform";
import { createUxFailureResult } from "@kb-labs/core-platform/ux-result";
import { platform } from "@kb-labs/core-runtime";
import { resolvePluginBasePath } from "@kb-labs/plugin-contracts";
import type { IEntityRegistry, RegistrySnapshotManifestEntry } from "@kb-labs/core-registry";
import type { FastifyInstance } from "fastify";
import type { RestApiConfig } from "@kb-labs/rest-api-core";

function pluginBasePath(entry: RegistrySnapshotManifestEntry, basePath: string): string | undefined {
  if (!entry.manifest.rest?.basePath && !entry.manifest.rest?.routes?.length) return undefined;
  return resolvePluginBasePath(entry.manifest, "rest", basePath);
}

/** Map unmatched REST plugin namespaces to the known project-install condition. */
export function registerPluginRouteNotFoundHandler(
  app: FastifyInstance,
  config: RestApiConfig,
  registry: IEntityRegistry,
): void {
  const basePath = config.basePath.replace(/\/$/, "");
  const pluginPrefix = `${basePath}/plugins/`;

  app.setNotFoundHandler(async (request, reply) => {
    const pathname = request.url.split("?", 1)[0] ?? request.url;
    if (!pathname.startsWith(pluginPrefix)) return reply.code(404).send({ error: "Not Found" });

    const slug = pathname.slice(pluginPrefix.length).split("/", 1)[0];
    if (!slug) return reply.code(404).send({ error: "Not Found" });

    const snapshot = registry.snapshot();
    const requestedPrefix = `${pluginPrefix}${slug}`;
    const installedPrefix = snapshot.manifests.some((entry) => {
      const prefix = pluginBasePath(entry, basePath);
      return prefix === requestedPrefix || prefix?.startsWith(`${requestedPrefix}/`);
    });
    if (installedPrefix) return reply.code(404).send({ error: "Not Found" });

    let packageId: string | undefined;
    try {
      packageId = await registry.findStoredPluginPackageForRestPrefix(requestedPrefix, basePath);
    } catch (error) {
      platform.logger.warn("Failed to resolve stored plugin package for absent REST prefix", {
        correlationId: String(request.id),
        requestedPrefix,
        error: error instanceof Error ? error.message : String(error),
      });
    }
    const error = createErrorEnvelope("KB_PLUGIN_NOT_INSTALLED", {
      ...(packageId ? { details: { package: packageId } } : {}),
      correlationId: String(request.id),
      hint: packageId
        ? `Run \`kb marketplace install ${packageId}\` in this project.`
        : "Run `kb marketplace plugins list` to discover available plugins.",
    });
    return reply.code(404).send(createUxFailureResult(error));
  });
}
