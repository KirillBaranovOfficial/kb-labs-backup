import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import YAML from "yaml";
import type { IProjectRegistry } from "@kb-labs/core-contracts";
import { listProjects } from "@kb-labs/core-project-registry";
import type { RuntimeProject } from "./manager.js";

/** Return registered projects with active cron triggers in `.kb/workflows`. */
export async function findScheduledProjects(
  registry: IProjectRegistry,
): Promise<RuntimeProject[]> {
  const projects = await listProjects(registry);
  const scheduled: RuntimeProject[] = [];
  for (const project of projects) {
    if (project.status !== "active" || !project.pathExists) { continue; }
    const directory = join(project.path, ".kb", "workflows");
    let files: string[];
    try {
      files = await readdir(directory);
    } catch {
      continue;
    }
    let count = 0;
    for (const file of files.filter((name) => name.endsWith(".yml") || name.endsWith(".yaml"))) {
      try {
        const definition: unknown = YAML.parse(await readFile(join(directory, file), "utf8"));
        if (!definition || typeof definition !== "object") { continue; }
        const stored = definition as {
          spec?: { on?: { schedule?: { cron?: unknown } }; enabled?: unknown; status?: unknown };
          on?: { schedule?: { cron?: unknown } };
          enabled?: unknown;
          status?: unknown;
        };
        const spec = stored.spec ?? stored;
        const status = stored.status ?? spec.status;
        if (
          typeof spec.on?.schedule?.cron === "string" &&
          spec.enabled !== false &&
          status !== "disabled" &&
          status !== "paused"
        ) {
          count += 1;
        }
      } catch {
        // The workflow daemon reports invalid definitions; they cannot schedule.
      }
    }
    if (count > 0) {
      scheduled.push({ id: project.id, root: project.path, keptRunningSchedules: count });
    }
  }
  return scheduled;
}
