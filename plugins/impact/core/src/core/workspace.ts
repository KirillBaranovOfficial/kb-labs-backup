import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import type { SubRepo } from '@kb-labs/impact-contracts';
import { findRegisteredProjectContainingPathSync } from '@kb-labs/core-project-registry';

export function findWorkspaceRoot(cwd?: string): string {
  const current = resolve(cwd ?? process.cwd());
  const project = findRegisteredProjectContainingPathSync(current);
  if (project) {
    return project.path;
  }
  throw new Error(`No registered project contains "${current}". Register it with: kb project add "${current}"`);
}

/**
 * Parse .gitmodules to get submodule paths.
 * Returns entries like { path: "platform/kb-labs-core", name: "kb-labs-core", category: "platform" }.
 * Falls back to scanning top-level dirs for .git (flat layout).
 */
export function listSubRepos(workspaceRoot: string): SubRepo[] {
  const gitmodulesPath = join(workspaceRoot, '.gitmodules');

  if (existsSync(gitmodulesPath)) {
    return parseSubReposFromGitmodules(gitmodulesPath, workspaceRoot);
  }

  // Fallback: flat layout — top-level dirs with .git
  return scanFlatLayout(workspaceRoot);
}

function parseSubReposFromGitmodules(gitmodulesPath: string, workspaceRoot: string): SubRepo[] {
  const repos: SubRepo[] = [];
  try {
    const content = readFileSync(gitmodulesPath, 'utf-8');
    const pathMatches = content.matchAll(/^\s*path\s*=\s*(.+)$/gm);
    for (const match of pathMatches) {
      const relPath = (match[1] ?? '').trim();
      if (!relPath) {continue;}

      const fullPath = join(workspaceRoot, relPath);
      if (!existsSync(join(fullPath, '.git')) && !existsSync(join(fullPath, 'package.json'))) {
        continue;
      }

      const parts = relPath.split('/');
      const name = parts.at(-1) ?? relPath;
      const category = parts.length > 1 ? parts.slice(0, -1).join('/') : '';
      repos.push({ path: relPath, category, name });
    }
  } catch {
    // ignore parse errors
  }
  return repos;
}

function scanFlatLayout(workspaceRoot: string): SubRepo[] {
  const repos: SubRepo[] = [];
  try {
    for (const entry of readdirSync(workspaceRoot, { withFileTypes: true })) {
      if (!entry.isDirectory() || entry.name.startsWith('.') || entry.name === 'node_modules') {continue;}
      const fullPath = join(workspaceRoot, entry.name);
      if (existsSync(join(fullPath, '.git')) || existsSync(join(fullPath, 'package.json'))) {
        repos.push({ path: entry.name, category: '', name: entry.name });
      }
    }
  } catch {
    // ignore
  }
  return repos;
}
