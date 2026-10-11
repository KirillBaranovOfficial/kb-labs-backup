import type { IServiceTransport } from "@kb-labs/core-platform";
import type { ProjectRouting } from "@kb-labs/gateway-app";

type ProjectServiceTransport = Pick<IServiceTransport, "resolveProjectService">;

/** Adapts a local project runtime lease to the shared project service transport contract. */
export function createProjectServiceTransport(
  routing: ProjectRouting,
): ProjectServiceTransport {
  return {
    async resolveProjectService(projectId, serviceId) {
      const upstream = await routing.acquire(projectId);
      return {
        baseUrl: `http://${upstream.host}:${upstream.port}`,
        pathPrefix: `/${serviceId}`,
        headers: upstream.headers,
        release: upstream.release,
      };
    },
  };
}
