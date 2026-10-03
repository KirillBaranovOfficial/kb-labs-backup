/**
 * The host with project routing: real gateway, real `launchPlatform`, real
 * project registry (temp KB_HOME) and real child processes, with the fake
 * runtime script standing in for `kb-project-runtime`.
 */

import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { IProjectRegistry } from "@kb-labs/core-contracts";
import { createProjectRegistry } from "@kb-labs/core-project-registry";
import { startTestHost, type HostFixtureConfig, type RunningHost } from "./harness.js";
import {
  FAKE_RUNTIME_ENTRY,
  isAlive,
  makeProject,
  waitFor,
  readUxSuccessResult,
  type FakeRuntimeReply,
  type TestProject,
} from "./testkit.js";
import type { HostOptions } from "../host.js";
import { createChildProcessLauncher } from "../project-runtime/launcher.js";
import type { RuntimeLauncher, RuntimeProject } from "../project-runtime/manager.js";

interface ErrorBody {
  error: { code: string; message: string; hint: string; cause?: string; details?: Record<string, string> };
}

interface HealthBody {
  projectRuntimes: {
    limit: number;
    active: number;
    runtimes: Array<{ projectId: string; state: string; inflight: number; keptRunningSchedules?: number; pid?: number }>;
  };
}

interface ReadyBody {
  ready: boolean;
  components: Record<string, { ready: boolean; status?: string }>;
}

const PROJECT_RUNTIME_ENTRY = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../../../project-runtime/app/dist/bin.js",
);

describe("kb-host project routing", () => {
  let kbHome: string;
  let registry: IProjectRegistry;
  let running: RunningHost | undefined;
  const projects: TestProject[] = [];

  beforeEach(async () => {
    kbHome = await mkdtemp(join(tmpdir(), "kb-home-test-"));
    registry = createProjectRegistry({ root: kbHome });
  });

  afterEach(async () => {
    await running?.stop().catch(() => undefined);
    running = undefined;
    await Promise.all(projects.splice(0).map((p) => p.cleanup()));
    await rm(kbHome, { recursive: true, force: true });
  });

  async function registered(
    behavior: Parameters<typeof makeProject>[0] = {},
  ): Promise<TestProject> {
    const project = await makeProject(behavior);
    projects.push(project);
    await registry.add(project.root);
    return project;
  }

  async function host(
    hostSection: Record<string, unknown> = {},
    options: HostOptions["projectRuntime"] = {},
    fixture: Omit<HostFixtureConfig, "host"> = {},
  ): Promise<RunningHost> {
    running = await startTestHost(
      { host: hostSection, ...fixture },
      {
        projectRegistry: registry,
        projectRuntime: { entry: FAKE_RUNTIME_ENTRY, stopTimeoutMs: 1500, ...options },
      },
    );
    return running;
  }

  async function reply(
    subject: RunningHost,
    projectId: string,
    path: string,
    init?: RequestInit,
  ): Promise<FakeRuntimeReply> {
    const response = await subject.fetchGateway(
      `/api/v1/projects/${projectId}${path}`,
      init,
    );
    expect(response.status).toBe(200);
    return (await response.json()) as FakeRuntimeReply;
  }

  it("routes each project id to its own runtime, so the projects are isolated", async () => {
    const a = await registered();
    const b = await registered();
    const subject = await host();

    const fromA = await reply(subject, a.id, "/rest/whoami");
    const fromB = await reply(subject, b.id, "/rest/whoami");
    const fromAAgain = await reply(subject, a.id, "/rest/whoami");

    expect(fromA.projectRoot).toBe(a.root);
    expect(fromA.projectId).toBe(a.id);
    expect(fromB.projectRoot).toBe(b.root);
    expect(fromB.projectId).toBe(b.id);
    expect(fromA.pid).not.toBe(fromB.pid);
    expect(fromAAgain.pid).toBe(fromA.pid);
    // The service path after the project id reaches the runtime unchanged.
    expect(fromA.path).toBe("/rest/whoami");
  });

  it("starts the registered project's real runtime and serves its REST health route", async () => {
    const project = await registered();
    const previousKbHome = process.env.KB_HOME;
    process.env.KB_HOME = kbHome;
    try {
      const probe = createChildProcessLauncher({
        entry: PROJECT_RUNTIME_ENTRY,
        stopTimeoutMs: 1500,
      });
      const launcher: RuntimeLauncher = {
        launch: (runtimeProject) => createChildProcessLauncher({
          entry: PROJECT_RUNTIME_ENTRY,
          env: { KB_PLATFORM_ROOT: runtimeProject.root },
          stopTimeoutMs: 1500,
        }).launch(runtimeProject),
        probe: probe.probe,
      };
      running = await startTestHost(
        { host: {} },
        {
          projectRegistry: registry,
          projectRuntime: {
            launcher,
            startTimeoutMs: 10_000,
          },
        },
      );

      const response = await running.fetchGateway(
        `/api/v1/projects/${project.id}/rest/api/v1/health`,
      );
      const body = await response.json();
      if (response.status !== 200) {
        throw new Error(String((body as ErrorBody).error.message) + " | " +
          String((body as ErrorBody).error.hint) + " | " +
          String((body as ErrorBody).error.cause).slice(-900));
      }
      expect(body).toEqual(expect.any(Object));
      const health = await readUxSuccessResult<HealthBody>(await running.fetchGateway("/health"));
      expect(health.projectRuntimes.runtimes).toContainEqual(
        expect.objectContaining({ projectId: project.id, state: "ready" }),
      );
    } finally {
      await running?.stop().catch(() => undefined);
      running = undefined;
      if (previousKbHome === undefined) delete process.env.KB_HOME;
      else process.env.KB_HOME = previousKbHome;
    }
  });

  // case: S-124.12
  it("returns the runtime's typed project identity mismatch without exposing a stack", async () => {
    const project = await registered();
    const launcher: RuntimeLauncher = {
      async launch(_runtimeProject: RuntimeProject) {
        return {
          pid: undefined,
          address: { host: "127.0.0.1", port: 1 },
          token: "test-runtime-token",
          exited: Promise.resolve({ code: 1, signal: null }),
          stop: async () => undefined,
          outputTail: () => "KB_PROJECT_RUNTIME_IDENTITY_MISMATCH: registry path mismatch",
        };
      },
      probe: async () => false,
    };
    const subject = await host({}, { launcher });

    const response = await subject.fetchGateway(
      `/api/v1/projects/${project.id}/rest/api/v1/health`,
    );
    expect(response.status).toBe(502);
    const body = (await response.json()) as ErrorBody;
    expect(body.error.code).toBe("KB_PROJECT_RUNTIME_IDENTITY_MISMATCH");
    expect(body.error.hint).toContain("kb project list");
    expect(JSON.stringify(body)).not.toContain("Error:");
    expect(JSON.stringify(body)).not.toContain("at ");
  });

  it("forwards method, query and body, and sets the runtime headers itself", async () => {
    const a = await registered();
    const subject = await host();

    const echoed = await reply(subject, a.id, "/rest/items?limit=5&sort=asc", {
      method: "POST",
      headers: {
        "content-type": "application/json",
        authorization: "Bearer caller-token",
        // A client must not be able to speak to the runtime as the host.
        "x-kb-runtime-token": "forged",
        "x-kb-project-id": "prj_forged",
      },
      body: JSON.stringify({ hello: "world" }),
    });

    expect(echoed.method).toBe("POST");
    expect(echoed.search).toBe("?limit=5&sort=asc");
    expect(JSON.parse(echoed.body)).toEqual({ hello: "world" });
    expect(echoed.received.authorization).toBe("Bearer caller-token");
    expect(echoed.received.projectHeader).toBe(a.id);
    expect(echoed.received.tokenHeader).toMatch(/^[0-9a-f]{64}$/);
  });

  it("keeps runtime addresses and secrets out of the public health payload", async () => {
    const a = await registered();
    const subject = await host();
    await reply(subject, a.id, "/rest/whoami");
    const runtime = await readUxSuccessResult<HealthBody>(await subject.fetchGateway("/health"));
    expect(runtime.projectRuntimes.runtimes).toHaveLength(1);
    // Nothing in the public health payload reveals the runtime's address.
    expect(JSON.stringify(runtime)).not.toMatch(/127\.0\.0\.1:\d+/);
    expect(JSON.stringify(runtime)).not.toContain("port");
  });

  // case: S-010.7
  it("passes live host service addresses into the child runtime", async () => {
    const a = await registered();
    const subject = await host();
    expect(process.env.KB_STATE_DAEMON_URL).toBeUndefined();
    const { stateDaemonUrl: stateUrl, gatewayUrl, firstLeaseStatus } = await reply(subject, a.id, "/rest/__host-services");

    if (typeof stateUrl !== "string" || typeof gatewayUrl !== "string" || typeof firstLeaseStatus !== "number") {
      throw new Error("project runtime did not receive usable host service addresses");
    }
    expect(stateUrl).toMatch(/^http:\/\/127\.0\.0\.1:\d+$/);
    expect(stateUrl).not.toBe("http://127.0.0.1:7777");
    expect(gatewayUrl).toBe(`http://127.0.0.1:${subject.gatewayPort}`);
    expect(firstLeaseStatus).toBe(404);
  });

  // case: S-203.15
  it("uses the default gateway listener address when gateway.port is absent", async () => {
    const project = await registered();
    const subject = await host({}, {}, { defaultGatewayPort: true });
    const platformConfig = JSON.parse(
      await readFile(`${subject.projectRoot}/.kb/kb.config.json`, "utf8"),
    ) as { gateway?: Record<string, unknown> };
    expect(platformConfig.gateway).not.toHaveProperty("port");

    const runtime = await reply(subject, project.id, "/rest/__host-services");
    expect(runtime.gatewayUrl).toBe(`http://127.0.0.1:${subject.gatewayPort}`);
  });

  it("answers KB_PROJECT_UNKNOWN for an id that is not in the registry, without starting anything", async () => {
    const subject = await host();

    const response = await subject.fetchGateway(
      "/api/v1/projects/prj_0000000000000000/rest/whoami",
    );

    expect(response.status).toBe(404);
    const body = (await response.json()) as ErrorBody;
    expect(body.error.code).toBe("KB_PROJECT_UNKNOWN");
    expect(body.error.hint.length).toBeGreaterThan(0);
    const health = await readUxSuccessResult<HealthBody>(await subject.fetchGateway("/health"));
    expect(health.projectRuntimes.runtimes).toEqual([]);
  });

  it("addresses projects by id only, not by registered name or path", async () => {
    const a = await registered();
    const subject = await host();
    const view = await registry.get(a.id);

    for (const ref of [view.project.name, encodeURIComponent(a.root)]) {
      const response = await subject.fetchGateway(
        `/api/v1/projects/${ref}/rest/whoami`,
      );
      expect(response.status).toBe(404);
      expect(((await response.json()) as ErrorBody).error.code).toBe(
        "KB_PROJECT_UNKNOWN",
      );
    }
  });

  it("answers KB_PROJECT_RUNTIME_START_FAILED when the runtime does not start", async () => {
    const a = await registered({ exitBeforeReady: 4 });
    const subject = await host();

    const response = await subject.fetchGateway(
      `/api/v1/projects/${a.id}/rest/whoami`,
    );

    expect(response.status).toBe(502);
    const body = (await response.json()) as ErrorBody;
    expect(body.error.code).toBe("KB_PROJECT_RUNTIME_START_FAILED");
    expect(body.error.details).toEqual({ project: a.id });
    // Other projects are unaffected.
    const b = await registered();
    expect((await reply(subject, b.id, "/rest/whoami")).projectRoot).toBe(b.root);
  });

  it("answers KB_PROJECT_RUNTIME_LIMIT when every runtime slot is busy", async () => {
    const a = await registered();
    const b = await registered();
    const subject = await host({ maxActiveProjects: 1 });
    await reply(subject, a.id, "/rest/whoami");

    // A request that stays in flight keeps A's runtime busy.
    const slow = subject.fetchGateway(
      `/api/v1/projects/${a.id}/rest/slow?delay=1500`,
    );
    await waitFor(async () => {
      const health = await readUxSuccessResult<HealthBody>(await subject.fetchGateway("/health"));
      return health.projectRuntimes.runtimes[0]?.inflight === 1;
    }, "the slow request to be in flight");

    const response = await subject.fetchGateway(
      `/api/v1/projects/${b.id}/rest/whoami`,
    );
    expect(response.status).toBe(503);
    expect(((await response.json()) as ErrorBody).error.code).toBe(
      "KB_PROJECT_RUNTIME_LIMIT");

    expect((await slow).status).toBe(200);
    // Once A is idle, B gets the slot.
    await waitFor(async () => {
      const retry = await subject.fetchGateway(
        `/api/v1/projects/${b.id}/rest/whoami`,
      );
      return retry.status === 200;
    }, "B to start once A is idle");
  });

  it("applies the gateway's authentication before a project runtime is ever started", async () => {
    const a = await registered();
    const subject = await host({ auth: "on" });

    const response = await subject.fetchGateway(
      `/api/v1/projects/${a.id}/rest/whoami`,
    );

    expect(response.status).toBe(401);
    // Public /health still answers, and nothing was started for the anonymous call.
    const health = await readUxSuccessResult<HealthBody>(await subject.fetchGateway("/health"));
    expect(health.projectRuntimes.runtimes).toEqual([]);
  });

  it("is ready and healthy with no project runtime, and reflects runtimes once they start lazily", async () => {
    const a = await registered();
    const subject = await host({ maxActiveProjects: 3 });

    const before = await subject.fetchGateway("/ready");
    expect(before.status).toBe(200);
    const beforeBody = await readUxSuccessResult<ReadyBody>(before);
    expect(beforeBody.ready).toBe(true);
    expect(beforeBody.components.projectRuntimes).toEqual({
      ready: true,
      status: "0/3 active",
    });

    await reply(subject, a.id, "/rest/whoami");

    const health = await readUxSuccessResult<HealthBody>(await subject.fetchGateway("/health"));
    expect(health.projectRuntimes.limit).toBe(3);
    expect(health.projectRuntimes.active).toBe(1);
    expect(health.projectRuntimes.runtimes).toEqual([
      expect.objectContaining({ projectId: a.id, state: "ready", inflight: 0 }),
    ]);
    const after = await readUxSuccessResult<ReadyBody>(await subject.fetchGateway("/ready"));
    expect(after.components.projectRuntimes?.status).toBe("1/3 active");
  });

  it("stops an idle runtime through the host", async () => {
    const a = await registered();
    const subject = await host({}, { idleTimeoutMs: 300, sweepIntervalMs: 50 });
    const { pid } = await reply(subject, a.id, "/rest/whoami");
    expect(isAlive(pid)).toBe(true);

    await waitFor(() => !isAlive(pid), "the idle runtime to stop");

    const health = await readUxSuccessResult<HealthBody>(await subject.fetchGateway("/health"));
    expect(health.projectRuntimes.runtimes).toEqual([]);
    // The next request starts it again.
    const again = await reply(subject, a.id, "/rest/whoami");
    expect(again.pid).not.toBe(pid);
  });

  it("starts scheduled workflow projects at host boot and keeps them warm", async () => {
    const a = await registered();
    const crons = join(a.root, ".kb", "crons");
    await mkdir(crons, { recursive: true });
    await writeFile(join(crons, "every-minute.yml"), [
      'schedule: "* * * * *"',
      "workflow: scheduled",
      "",
    ].join("\n"));
    await writeFile(join(crons, "hourly.yml"), [
      'schedule: "0 * * * *"',
      "timezone: UTC",
      "enabled: true",
      "workflow: stored-active",
      "",
    ].join("\n"));
    await writeFile(join(crons, "paused.yml"), [
      'schedule: "30 * * * *"',
      "enabled: false",
      "workflow: stored-paused",
      "",
    ].join("\n"));
    const subject = await host({}, { idleTimeoutMs: 30, sweepIntervalMs: 10, startPollMs: 10 });

    await waitFor(async () => {
      const health = await readUxSuccessResult<HealthBody>(await subject.fetchGateway("/health"));
      return health.projectRuntimes.runtimes.some(
        (runtime) => runtime.projectId === a.id && runtime.state === "ready",
      );
    }, "the scheduled runtime to start during host startup");

    const before = await readUxSuccessResult<HealthBody>(await subject.fetchGateway("/health"));
    expect(before.projectRuntimes.runtimes[0]).toMatchObject({
      projectId: a.id,
      keptRunningSchedules: 2,
    });
    const pid = (await reply(subject, a.id, "/rest/whoami")).pid;
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 120);
    });
    expect(isAlive(pid)).toBe(true);
  });

  it("stops every project runtime when the host shuts down, leaving no orphans", async () => {
    const a = await registered();
    const b = await registered();
    const subject = await host();
    const pids = [
      (await reply(subject, a.id, "/rest/whoami")).pid,
      (await reply(subject, b.id, "/rest/whoami")).pid,
    ];
    const { childPid } = (await (
      await subject.fetchGateway(`/api/v1/projects/${a.id}/rest/__spawn-child`)
    ).json()) as { childPid: number };
    for (const pid of [...pids, childPid]) {
      expect(isAlive(pid)).toBe(true);
    }

    const exitCode = await subject.stop();

    expect(exitCode).toBe(0);
    for (const pid of [...pids, childPid]) {
      expect(isAlive(pid)).toBe(false);
    }
  });

  it("records the use of a project in the registry when its runtime starts", async () => {
    const a = await registered();
    const subject = await host();
    expect((await registry.get(a.id)).project.lastUsedAt).toBeNull();

    await reply(subject, a.id, "/rest/whoami");

    await waitFor(
      async () => (await registry.get(a.id)).project.lastUsedAt !== null,
      "lastUsedAt to be recorded",
    );
  });
});
