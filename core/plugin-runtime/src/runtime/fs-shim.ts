/**
 * Sandboxed filesystem implementation
 *
 * Security model:
 * - Plugins declare what they WANT in manifest (allow-list)
 * - Platform enforces hardcoded security patterns (deny-list below)
 * - Users can further restrict via kb.config.json (future)
 */

import * as fs from 'node:fs/promises';
import * as os from 'node:os';
import * as path from 'node:path';
import type { FSShim, FileStat, DirEntry, MkdirOptions, RmOptions, WriteFileOptions, GlobOptions, PermissionSpec } from '@kb-labs/plugin-contracts';
import { PermissionError } from '@kb-labs/plugin-contracts';

/**
 * Patterns that are ALWAYS denied (platform security, not configurable)
 *
 * These are enforced regardless of what plugin requests in manifest.
 * This protects against malicious or buggy plugins.
 */
const HARDCODED_DENIED_PATTERNS = [
  /node_modules/,
  /\.git\//,
  /\.env$/,
  /\.env\./,
  /\.ssh/,
  /\/etc\//,
  /\/usr\//,
  /credentials/i,
  /password/i,
  /\.pem$/,
  /\.key$/,
  /\.secret$/,
];

const VAR_PATH_PATTERN = /\/var(?:\/|$)/;

function isWithinPath(candidate: string, root: string): boolean {
  const relative = path.relative(root, candidate);
  return relative === '' || (
    relative !== '..' &&
    !relative.startsWith(`..${path.sep}`) &&
    !path.isAbsolute(relative)
  );
}

function isMissingPathError(error: unknown): boolean {
  return error instanceof Error && 'code' in error &&
    (error.code === 'ENOENT' || error.code === 'ENOTDIR');
}

/** Resolve existing symlinks in a path while retaining any missing suffix. */
async function realPathForSecurity(filePath: string): Promise<string> {
  let existingPath = filePath;
  const missingSegments: string[] = [];

  while (true) {
    try {
      return path.resolve(await fs.realpath(existingPath), ...missingSegments.reverse());
    } catch (error) {
      if (!isMissingPathError(error)) {
        throw error;
      }

      const parent = path.dirname(existingPath);
      if (parent === existingPath) {
        return filePath;
      }
      missingSegments.push(path.basename(existingPath));
      existingPath = parent;
    }
  }
}

/**
 * Convert glob pattern to regex
 */
function globToRegex(pattern: string): RegExp {
  const escaped = pattern
    .replace(/[.+^${}()|[\]\\]/g, '\\$&') // Escape special chars
    .replace(/\*\*/g, '{{GLOBSTAR}}')     // Temp placeholder for **
    .replace(/\*/g, '[^/]*')              // * matches anything except /
    .replace(/\?/g, '.')                   // ? matches single char
    .replace(/{{GLOBSTAR}}/g, '.*');      // ** matches anything including /
  return new RegExp(escaped);
}

export interface CreateFSShimOptions {
  permissions: PermissionSpec;
  cwd: string;
  outdir?: string;
}

/**
 * Check if a path matches a glob pattern (resolved to absolute path)
 */
function matchesGlobPattern(filePath: string, pattern: string, cwd: string): boolean {
  // SPECIAL CASE: ** wildcard means "match everything"
  if (pattern === '**') {
    return true;
  }

  // If pattern doesn't contain glob chars, treat as prefix match
  if (!pattern.includes('*') && !pattern.includes('?')) {
    const resolvedPattern = path.resolve(cwd, pattern);
    return filePath.startsWith(resolvedPattern);
  }

  // Convert glob to regex and match
  const resolvedPattern = path.resolve(cwd, pattern);
  const regex = globToRegex(resolvedPattern);
  return regex.test(filePath);
}

/**
 * Create a sandboxed filesystem shim
 */
export function createFSShim(options: CreateFSShimOptions): FSShim {
  const { permissions, cwd, outdir } = options;

  // Store patterns for glob matching (not resolved to absolute paths yet)
  const readablePatterns: string[] = [
    '.', // cwd always allowed for reading
    ...(permissions.fs?.read ?? []),
  ];

  // Store patterns for writing
  const writablePatterns: string[] = [
    outdir ? path.relative(cwd, path.resolve(outdir)) : '.kb/output', // outdir always allowed
    ...(permissions.fs?.write ?? []),
  ];
  let realTempDirPromise: Promise<string> | undefined;

  function getRealTempDir(): Promise<string> {
    realTempDirPromise ??= fs.realpath(os.tmpdir());
    return realTempDirPromise;
  }

  function normalizePath(filePath: string): string {
    return path.normalize(path.resolve(cwd, filePath));
  }

  async function checkDeniedPatterns(normalizedPath: string): Promise<string> {
    const realPath = await realPathForSecurity(normalizedPath);
    const realTempDir = await getRealTempDir();
    const macUserTempDir = process.platform === 'darwin' &&
      realTempDir.includes(`${path.sep}var${path.sep}folders${path.sep}`);
    const isInsideMacUserTemp = macUserTempDir && isWithinPath(realPath, realTempDir);

    // Check hardcoded security patterns (platform-level, not configurable)
    for (const pattern of HARDCODED_DENIED_PATTERNS) {
      if (pattern.test(normalizedPath) || pattern.test(realPath)) {
        throw new PermissionError(`Access denied: path matches security pattern`, {
          path: normalizedPath,
          pattern: pattern.toString(),
        });
      }
    }

    if (!isInsideMacUserTemp && (VAR_PATH_PATTERN.test(normalizedPath) || VAR_PATH_PATTERN.test(realPath))) {
      throw new PermissionError(`Access denied: path matches security pattern`, {
        path: normalizedPath,
        pattern: VAR_PATH_PATTERN.toString(),
      });
    }

    return realPath;
  }

  async function checkReadPermission(filePath: string): Promise<string> {
    const normalized = normalizePath(filePath);
    const realPath = await checkDeniedPatterns(normalized);
    const realCwd = await realPathForSecurity(cwd);

    const isAllowed = readablePatterns.some(pattern =>
      matchesGlobPattern(realPath, pattern, realCwd)
    );

    if (!isAllowed) {
      throw new PermissionError(`Read access denied`, { path: filePath });
    }

    return normalized;
  }

  async function checkWritePermission(filePath: string): Promise<string> {
    const normalized = normalizePath(filePath);
    const realPath = await checkDeniedPatterns(normalized);
    const realCwd = await realPathForSecurity(cwd);

    const isAllowed = writablePatterns.some(pattern =>
      matchesGlobPattern(realPath, pattern, realCwd)
    );

    if (!isAllowed) {
      throw new PermissionError(`Write access denied`, { path: filePath });
    }

    return normalized;
  }

  return {
    async readFile(filePath: string, encoding: BufferEncoding = 'utf-8'): Promise<string> {
      const resolved = await checkReadPermission(filePath);
      return fs.readFile(resolved, encoding);
    },

    async readFileBuffer(filePath: string): Promise<Uint8Array> {
      const resolved = await checkReadPermission(filePath);
      const buffer = await fs.readFile(resolved);
      return new Uint8Array(buffer);
    },

    async writeFile(
      filePath: string,
      content: string | Uint8Array,
      options?: WriteFileOptions
    ): Promise<void> {
      const resolved = await checkWritePermission(filePath);

      // Ensure parent directory exists
      await fs.mkdir(path.dirname(resolved), { recursive: true });

      const writeOptions: { encoding?: BufferEncoding; flag?: string } = {
        encoding: options?.encoding ?? 'utf-8',
      };

      if (options?.append) {
        writeOptions.flag = 'a';
      }

      await fs.writeFile(resolved, content, writeOptions);
    },

    async readdir(dirPath: string): Promise<string[]> {
      const resolved = await checkReadPermission(dirPath);
      return fs.readdir(resolved);
    },

    async readdirWithStats(dirPath: string): Promise<DirEntry[]> {
      const resolved = await checkReadPermission(dirPath);
      const entries = await fs.readdir(resolved, { withFileTypes: true });

      return entries.map(entry => ({
        name: entry.name,
        isFile: entry.isFile(),
        isDirectory: entry.isDirectory(),
      }));
    },

    async stat(filePath: string): Promise<FileStat> {
      const resolved = await checkReadPermission(filePath);
      const stats = await fs.stat(resolved);

      return {
        isFile: () => stats.isFile(),
        isDirectory: () => stats.isDirectory(),
        size: stats.size,
        mtime: stats.mtimeMs,
        ctime: stats.ctimeMs,
      };
    },

    async exists(filePath: string): Promise<boolean> {
      try {
        const resolved = await checkReadPermission(filePath);
        await fs.access(resolved);
        return true;
      } catch {
        return false;
      }
    },

    async mkdir(dirPath: string, options?: MkdirOptions): Promise<void> {
      const resolved = await checkWritePermission(dirPath);
      await fs.mkdir(resolved, { recursive: options?.recursive ?? false });
    },

    async rm(filePath: string, options?: RmOptions): Promise<void> {
      const resolved = await checkWritePermission(filePath);
      await fs.rm(resolved, {
        recursive: options?.recursive ?? false,
        force: options?.force ?? false,
      });
    },

    async copy(src: string, dest: string): Promise<void> {
      const resolvedSrc = await checkReadPermission(src);
      const resolvedDest = await checkWritePermission(dest);

      // Ensure parent directory exists
      await fs.mkdir(path.dirname(resolvedDest), { recursive: true });
      await fs.cp(resolvedSrc, resolvedDest, { recursive: true });
    },

    async move(src: string, dest: string): Promise<void> {
      const resolvedSrc = await checkWritePermission(src); // Need write to delete source
      const resolvedDest = await checkWritePermission(dest);

      // Ensure parent directory exists
      await fs.mkdir(path.dirname(resolvedDest), { recursive: true });
      await fs.rename(resolvedSrc, resolvedDest);
    },

    async glob(pattern: string, options?: GlobOptions): Promise<string[]> {
      // Use node:fs to implement simple glob
      const searchCwd = options?.cwd ? path.resolve(cwd, options.cwd) : cwd;
      const ignorePatterns = options?.ignore ?? [];

      // Simple recursive file walker
      const matches: string[] = [];

      async function walk(dir: string): Promise<void> {
        // Check read permission for directory
        try {
          await checkReadPermission(dir);
        } catch {
          return; // Skip directories we can't read
        }

        const entries = await fs.readdir(dir, { withFileTypes: true });

        for (const entry of entries) {
          const fullPath = path.join(dir, entry.name);
          const relativePath = path.relative(searchCwd, fullPath);

          // Skip ignored patterns
          if (ignorePatterns.some((ignore: string) => matchesGlobPattern(fullPath, ignore, searchCwd))) {
            continue;
          }

          if (entry.isDirectory()) {
            await walk(fullPath);
          } else if (entry.isFile() && matchesGlobPattern(fullPath, pattern, searchCwd)) {
            // Check if file matches pattern
            matches.push(options?.absolute ? fullPath : relativePath);
          }
        }
      }

      await walk(searchCwd);
      return matches;
    },

    resolve(filePath: string): string {
      return path.resolve(cwd, filePath);
    },

    relative(filePath: string): string {
      return path.relative(cwd, filePath);
    },

    join(...segments: string[]): string {
      return path.join(...segments);
    },

    dirname(filePath: string): string {
      return path.dirname(filePath);
    },

    basename(filePath: string, ext?: string): string {
      return path.basename(filePath, ext);
    },

    extname(filePath: string): string {
      return path.extname(filePath);
    },
  };
}
