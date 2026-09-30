import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import type { IProjectRegistry, ProjectView } from "@kb-labs/core-contracts";
import { findScheduledProjects } from "../project-runtime/workflow-schedules.js";

describe("findScheduledProjects", () => {
  const roots: string[] = [];

  afterEach(async () => {
    await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
  });

  it("keeps projects warm for enabled workflow and user cron schedules", async () => {
    const root = await mkdtemp(join(tmpdir(), "kb-scheduled-project-"));
    roots.push(root);
    await mkdir(join(root, ".kb", "workflows"), { recursive: true });
    await mkdir(join(root, ".kb", "jobs"), { recursive: true });
    await writeFile(join(root, ".kb", "workflows", "release.yml"),
      "name: release\non:\n  schedule:\n    cron: '* * * * *'\n");
    await writeFile(join(root, ".kb", "jobs", "enabled.yml"),
      "name: cleanup\nschedule: '* * * * *'\nenabled: true\nautoStart: true\njobs: {}\n");
    await writeFile(join(root, ".kb", "jobs", "disabled.yml"),
      "name: paused\nschedule: '* * * * *'\nenabled: false\njobs: {}\n");

    const view = {
      project: { id: "prj_test", name: "scheduled", status: "active", addedAt: "", path: root },
      declaration: {},
      pathExists: true,
      stateDir: join(root, ".state"),
    } as ProjectView;
    const registry = { list: async () => [view] } as IProjectRegistry;

    await expect(findScheduledProjects(registry)).resolves.toMatchObject([
      { id: "prj_test", root, keptRunningSchedules: 2 },
    ]);
  });
});
