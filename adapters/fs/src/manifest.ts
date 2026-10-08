/**
 * @module @kb-labs/adapters-fs/manifest
 * Adapter manifest for filesystem storage.
 */

import type { AdapterManifest } from "@kb-labs/sdk/adapters";

/**
 * Adapter manifest for filesystem storage.
 */
export const manifest: AdapterManifest = {
  schema: 'kb.adapter/1',
  manifestVersion: "1.0.0",
  id: "fs-storage",
  name: "Filesystem Storage",
  version: "1.0.0",
  description: "Local filesystem storage adapter with path security",
  author: "KB Labs Team",
  license: "KBPL-1.1",
  type: "core",
  implements: "IStorage",
  networkRequired: false,
  defaultFor: ["storage"],
  capabilities: {
    streaming: true,
    custom: {
      glob: true,
      metadata: true,
    },
  },
  configSchema: {
    baseDir: {
      type: "string",
      default: "<KB_HOME>/state/<projectId>/storage",
      description: "Base directory for all file operations; omitted paths use platform state for the current project",
    },
  },
};
