import { createServer, request as httpRequest, type Server } from "node:http";
import Fastify, { type FastifyInstance } from "fastify";
import { afterEach, describe, expect, it } from "vitest";
import { registerProjectRoutes } from "../project-routing.js";

const PROJECT_SERVICES = [{ serviceId: "rest", rewritePrefix: "/api/v1" }];

describe("registerProjectRoutes", () => {
  let upstream: Server | undefined;
  let app: FastifyInstance | undefined;

  afterEach(async () => {
    await app?.close();
    await new Promise<void>((resolve) => {
      if (!upstream) {
        resolve();
        return;
      }
      upstream.closeAllConnections();
      upstream.close(() => resolve());
    });
    upstream = undefined;
    app = undefined;
  });

  it("rejects duplicate or invalid service namespaces at Gateway assembly", async () => {
    app = Fastify();
    const transport = { resolveProjectService: async () => undefined };
    await expect(registerProjectRoutes(app, [
      { serviceId: "rest", rewritePrefix: "/api/v1" },
      { serviceId: "rest", rewritePrefix: "/api/v2" },
    ], transport)).rejects.toThrow(/duplicate project service id/i);
    await expect(registerProjectRoutes(app, [
      { serviceId: "rest/api", rewritePrefix: "/api/v1" },
    ], transport)).rejects.toThrow(/invalid project service id/i);
  });

  async function setup(transport: (port: number) => { resolveProjectService: (projectId: string, serviceId: string) => Promise<{ baseUrl: string; pathPrefix?: string; headers: Record<string, string>; release: () => void } | undefined> }, upstreamReply?: { statusCode: number; body: string; stall?: boolean; stream?: boolean }, services = PROJECT_SERVICES, timeoutMs = 30_000) {
    const seen: Array<{ url?: string; headers: Record<string, unknown>; body: string }> = [];
    upstream = createServer((req, res) => {
      const chunks: Buffer[] = [];
      req.on("data", (c: Buffer) => chunks.push(c));
      req.on("end", () => {
        seen.push({ url: req.url, headers: req.headers, body: Buffer.concat(chunks).toString() });
        if (upstreamReply?.stall) return;
        if (upstreamReply?.stream) {
          res.writeHead(upstreamReply.statusCode, { "content-type": "text/event-stream" });
          res.write("data: open\\n\\n");
          return;
        }
        res.writeHead(upstreamReply?.statusCode ?? 202, { "x-up": "1", "content-type": "application/json" });
        res.end(upstreamReply?.body ?? '"ok"');
      });
    });
    await new Promise<void>((resolve) => {
      upstream!.listen(0, "127.0.0.1", resolve);
    });
    const port = (upstream.address() as { port: number }).port;
    app = Fastify();
    await registerProjectRoutes(app, services, transport(port), timeoutMs);
    await app.ready();
    return seen;
  }

  it("rewrites the service namespace, replaces control headers and releases once", async () => {
    let released = 0;
    const seen = await setup((port) => ({
      resolveProjectService: async (id, serviceId) => {
        expect(id).toBe("prj_a");
        expect(serviceId).toBe("rest");
        return {
          baseUrl: `http://127.0.0.1:${port}`,
          pathPrefix: "/rest",
          headers: { "x-kb-runtime-token": "real" },
          release: () => {
            released += 1;
          },
        };
      },
    }));

    const res = await app!.inject({
      method: "POST",
      url: "/api/v1/projects/prj_a/rest/analytics/events?x=1",
      headers: { "x-kb-runtime-token": "forged", "content-type": "application/json" },
      payload: '{"a":1}',
    });

    expect(res.statusCode).toBe(202);
    expect(res.headers["x-up"]).toBe("1");
    expect(seen[0]?.url).toBe("/rest/api/v1/analytics/events?x=1");
    expect(seen[0]?.headers["x-kb-runtime-token"]).toBe("real");
    expect(seen[0]?.body).toBe('{"a":1}');
    expect(released).toBe(1);
  });

  it("routes the MCP service root without requiring a trailing slash", async () => {
    const seen = await setup((port) => ({
      resolveProjectService: async () => ({
        baseUrl: `http://127.0.0.1:${port}`,
        pathPrefix: "/mcp",
        headers: {},
        release: () => undefined,
      }),
    }), undefined, [{ serviceId: "mcp", rewritePrefix: "/" }]);

    const response = await app!.inject({ method: "POST", url: "/api/v1/projects/prj_a/mcp", payload: "{}" });

    expect(response.statusCode).toBe(202);
    expect(seen[0]?.url).toBe("/mcp/");
  });

  // case: S-192.5
  it("returns the catalogued not-found error for an undeclared service without acquiring a runtime", async () => {
    let acquired = false;
    await setup(() => ({
      resolveProjectService: async () => {
        acquired = true;
        throw new Error("unknown service should not acquire a runtime");
      },
    }));

    const res = await app!.inject({
      method: "GET",
      url: "/api/v1/projects/prj_a/unknown-service/health",
    });

    expect(res.statusCode).toBe(404);
    expect(res.json()).toMatchObject({
      ok: false,
      error: { code: "KB_RUNTIME_RESOURCE_NOT_FOUND" },
    });
    expect(acquired).toBe(false);
  });

  it("answers 502 and releases when the runtime is unreachable", async () => {
    let released = 0;
    await setup(() => ({
      resolveProjectService: async () => ({ baseUrl: "http://127.0.0.1:1", pathPrefix: "/rest", headers: {}, release: () => { released += 1; } }),
    }));

    const res = await app!.inject({ method: "GET", url: "/api/v1/projects/p/rest/x" });

    expect(res.statusCode).toBe(502);
    expect(released).toBe(1);
  });

  // case: S-220.2
  it("times out a stalled upstream and releases its lease once", async () => {
    let released = 0;
    await setup(() => ({
      resolveProjectService: async () => ({ baseUrl: `http://127.0.0.1:${(upstream!.address() as { port: number }).port}`, pathPrefix: "/rest", headers: {}, release: () => { released += 1; } }),
    }), { statusCode: 200, body: "", stall: true }, PROJECT_SERVICES, 20);

    const response = await app!.inject({ method: "GET", url: "/api/v1/projects/p/rest/slow" });

    expect(response.statusCode).toBe(504);
    expect(response.json()).toMatchObject({ ok: false, error: { code: "KB_RUNTIME_TIMEOUT" } });
    expect(released).toBe(1);
  });

  it("aborts a streaming upstream when the downstream client closes and releases once", async () => {
    let released = 0;
    let upstreamClosed = false;
    let resolveRelease!: () => void;
    const releasedLease = new Promise<void>((resolve) => { resolveRelease = resolve; });
    let resolveUpstreamClose!: () => void;
    const closedUpstream = new Promise<void>((resolve) => { resolveUpstreamClose = resolve; });
    upstream = createServer((req, res) => {
      res.on("close", () => { upstreamClosed = true; resolveUpstreamClose(); });
      req.on("end", () => {
        res.writeHead(200, { "content-type": "text/event-stream" });
        res.write("data: open\\n\\n");
      });
      req.resume();
    });
    await new Promise<void>((resolve) => upstream!.listen(0, "127.0.0.1", resolve));
    const port = (upstream.address() as { port: number }).port;
    app = Fastify();
    await registerProjectRoutes(app, PROJECT_SERVICES, { resolveProjectService: async () => ({
      baseUrl: `http://127.0.0.1:${port}`, pathPrefix: "/rest", headers: {}, release: () => { released += 1; resolveRelease(); },
    }) });
    await app.listen({ host: "127.0.0.1", port: 0 });
    const address = app.server.address();
    if (!address || typeof address === "string") throw new Error("Expected TCP listener");
    await new Promise<void>((resolve, reject) => {
      const downstream = httpRequest({ host: "127.0.0.1", port: address.port, path: "/api/v1/projects/p/rest/events" }, (response) => {
        response.once("data", () => {
          downstream.destroy();
          resolve();
        });
      });
      downstream.once("error", (error) => {
        if (error.message === "socket hang up") resolve();
        else reject(error);
      });
      downstream.end();
    });
    await Promise.all([releasedLease, closedUpstream]);
    expect(released).toBe(1);
    expect(upstreamClosed).toBe(true);
  });

  // case: S-203.2
  it("wraps a non-envelope upstream 404 as a typed resource-not-found failure", async () => {
    await setup(() => ({
      resolveProjectService: async () => ({ baseUrl: `http://127.0.0.1:${(upstream!.address() as { port: number }).port}`, pathPrefix: "/rest", headers: {}, release: () => undefined }),
    }), { statusCode: 404, body: '{"error":"Not Found"}' });

    const res = await app!.inject({ method: "GET", url: "/api/v1/projects/p/rest/api/v1/plugins/agents/sessions" });

    expect(res.statusCode).toBe(404);
    expect(res.json()).toMatchObject({
      ok: false,
      error: {
        code: "KB_RUNTIME_RESOURCE_NOT_FOUND",
        message: "Project runtime returned HTTP 404 without an error envelope.",
        hint: "Run `kb marketplace plugins doctor --scope project` to inspect project plugin routes, then retry the request.",
        details: { upstreamStatus: "404" },
        correlationId: expect.any(String),
      },
    });
  });
});
