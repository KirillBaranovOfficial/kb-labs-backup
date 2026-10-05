import { realpath, readFile, stat } from 'node:fs/promises';
import path from 'node:path';
import yaml from 'js-yaml';
import { architectureIndexSchema, type ArchitectureIndex, type ViewerFileEntry } from '@kb-labs/viewer-contracts';

const INDEX_NAME = 'architecture.yaml';
const MAX_TEXT_BYTES = 2 * 1024 * 1024;

export async function resolveArchitectureRoot(projectRoot: string): Promise<string> {
  const root = await realpath(path.join(projectRoot, 'docs', 'architecture'));
  const project = await realpath(projectRoot);
  if (!root.startsWith(`${project}${path.sep}`)) throw new Error('Architecture root must be inside the project');
  return root;
}

export async function resolveSourceRoot(projectRoot: string, source = 'workspace'): Promise<string> {
  if (source === 'workspace') return resolveArchitectureRoot(projectRoot);
  if (!/^pkg-[a-z0-9-]{8,80}$/.test(source)) throw new Error('Unknown document source');
  const project = await realpath(projectRoot);
  const root = await realpath(path.join(project, '.kb', 'viewer', 'sources', source));
  if (!root.startsWith(`${project}${path.sep}`)) throw new Error('Document source must be inside the project');
  return root;
}

export async function loadArchitecture(root: string): Promise<ArchitectureIndex> {
  const indexPath = await resolveInside(root, INDEX_NAME);
  const source = await readFile(indexPath, 'utf8');
  const index = architectureIndexSchema.parse(yaml.load(source));
  const ids = new Set(index.nodes.map((node) => node.id));
  if (ids.size !== index.nodes.length || !ids.has(index.root)) throw new Error('Architecture index has duplicate nodes or an unknown root');
  for (const node of index.nodes) {
    if (node.parent && !ids.has(node.parent)) throw new Error(`Unknown parent '${node.parent}' for '${node.id}'`);
    for (const child of node.children) if (!ids.has(child)) throw new Error(`Unknown child '${child}' for '${node.id}'`);
    if (node.diagram) await resolveInside(root, node.diagram);
    for (const file of node.documents) await resolveInside(root, file);
  }
  return index;
}

export async function listFiles(root: string, relative = ''): Promise<ViewerFileEntry[]> {
  const absolute = await resolveInside(root, relative || '.');
  const { readdir } = await import('node:fs/promises');
  const entries = await readdir(absolute, { withFileTypes: true });
  const result: ViewerFileEntry[] = [];
  for (const entry of entries.sort((a, b) => a.name.localeCompare(b.name))) {
    if (entry.name.startsWith('.') || entry.name === 'node_modules') continue;
    const filePath = path.posix.join(relative, entry.name);
    if (entry.isSymbolicLink()) continue;
    const kind = entry.isDirectory() ? 'directory' : classify(entry.name);
    const size = entry.isFile() ? (await stat(path.join(absolute, entry.name))).size : undefined;
    result.push({ path: filePath, name: entry.name, kind, ...(size === undefined ? {} : { size }) });
  }
  return result;
}

export async function readTextFile(root: string, relative: string): Promise<{ content: string; kind: ViewerFileEntry['kind'] }> {
  const absolute = await resolveInside(root, relative);
  const info = await stat(absolute);
  if (!info.isFile() || info.size > MAX_TEXT_BYTES) throw new Error('File is not a supported text file or exceeds the 2 MB limit');
  const kind = classify(relative);
  if (!['markdown', 'diagram', 'other'].includes(kind)) throw new Error('This file must be opened with its document preview');
  return { content: await readFile(absolute, 'utf8'), kind };
}

export async function resolveInside(root: string, relative: string): Promise<string> {
  if (path.isAbsolute(relative) || relative.split(/[\\/]/).includes('..')) throw new Error('Invalid relative path');
  const candidate = await realpath(path.resolve(root, relative));
  if (candidate !== root && !candidate.startsWith(`${root}${path.sep}`)) throw new Error('Path escapes architecture root');
  return candidate;
}

export function isAllowedPackagePath(relative: string): boolean {
  const parts = relative.split(/[\\/]/);
  if (parts.some((part) => !part || part.startsWith('.'))) return false;
  return !parts.some((part) => /^(\.env|secrets?|credentials?|id_rsa|id_ed25519)$/i.test(part)
    || /\.(pem|key|p12|pfx)$/i.test(part));
}

function classify(name: string): ViewerFileEntry['kind'] {
  const extension = path.extname(name).toLowerCase();
  if (extension === '.md' || extension === '.mdx') return 'markdown';
  if (['.mmd', '.mermaid', '.puml', '.plantuml'].includes(extension)) return 'diagram';
  if (extension === '.pdf') return 'pdf';
  if (['.doc', '.docx'].includes(extension)) return 'office';
  return 'other';
}
