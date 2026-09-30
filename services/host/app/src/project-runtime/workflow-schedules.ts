import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import YAML from "yaml";
import type { IProjectRegistry } from "@kb-labs/core-contracts";
import type { RuntimeProject } from "./manager.js";

/** Return registered projects with enabled workflow or user-job cron schedules. */
export async function findScheduledProjects(
  registry: IProjectRegistry,
): Promise<RuntimeProject[]> {
  const projects = await registry.list();
  const scheduled: RuntimeProject[] = [];
  for (const view of projects) {
    const project = view.project;
    if (project.status !== "active" || !view.pathExists) { continue; }
    const workflowDir = join(project.path, ".kb", "workflows");
    const jobsDir = join(project.path, ".kb", "jobs");
    const count = await countEnabledSchedules(workflowDir, isScheduledWorkflow)
      + await countEnabledSchedules(jobsDir, isEnabledCronJob);
    if (count > 0) {
      scheduled.push({ id: project.id, root: project.path, keptRunningSchedules: count });
    }
  }
  return scheduled;
}

async function countEnabledSchedules(
  directory: string,
  accepts: (definition: Record<string, unknown>) => boolean,
): Promise<number> {
  let files: string[];
  try {
    files = await readdir(directory);
  } catch {
    return 0;
  }

  let count = 0;
  for (const file of files.filter((name) => name.endsWith(".yml") || name.endsWith(".yaml"))) {
    try {
      const definition: unknown = YAML.parse(await readFile(join(directory, file), "utf8"));
      if (definition && typeof definition === "object" && accepts(definition as Record<string, unknown>)) {
        count += 1;
      }
    } catch {
      // The workflow daemon reports invalid definitions; they cannot schedule.
    }
  }
  return count;
}

function isScheduledWorkflow(definition: Record<string, unknown>): boolean {
  const stored = definition as {
    spec?: { on?: { schedule?: { cron?: unknown } }; enabled?: unknown; status?: unknown };
    on?: { schedule?: { cron?: unknown } };
    enabled?: unknown;
    status?: unknown;
  };
  const spec = stored.spec ?? stored;
  const status = stored.status ?? spec.status;
  return typeof spec.on?.schedule?.cron === "string" && spec.enabled !== false
    && status !== "disabled" && status !== "paused";
}

function isEnabledCronJob(definition: Record<string, unknown>): boolean {
  return typeof definition.schedule === "string" && definition.enabled !== false
    && definition.autoStart !== false;
}
