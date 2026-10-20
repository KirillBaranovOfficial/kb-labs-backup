import { describe, expect, it, vi } from "vitest";
import { createProjectServiceTransport } from "./service-transport.js";
import type { ProjectRouting } from "@kb-labs/gateway-app";

describe("createProjectServiceTransport", () => {
  it("resolves a project service through the runtime lease and keeps guard paths internal", async () => {
    const routing: ProjectRouting = {
      acquire: vi.fn(async (projectId) => ({
        host: "127.0.0.1",
        port: 42000,
        headers: { "x-kb-runtime-token": "secret", "x-kb-project-id": projectId },
        release: vi.fn(),
      })),
      status: () => ({ limit: 1, active: 1, runtimes: [] }),
    };
    const transport = createProjectServiceTransport(routing);

    const connection = await transport.resolveProjectService("p1", "rest");

    expect(routing.acquire).toHaveBeenCalledWith("p1");
    expect(connection).toMatchObject({
      baseUrl: "http://127.0.0.1:42000",
      pathPrefix: "/rest",
      headers: { "x-kb-runtime-token": "secret", "x-kb-project-id": "p1" },
    });
    expect(connection?.release).toBeTypeOf("function");
  });
});
