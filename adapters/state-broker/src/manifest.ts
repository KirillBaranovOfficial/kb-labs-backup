import type { AdapterManifest } from "@kb-labs/sdk/adapters";

export const manifest: AdapterManifest = {
  schema: 'kb.adapter/1',
  manifestVersion: "1.0.0",
  id: "state-broker",
  name: "StateBroker Cache",
  version: "1.0.0",
  description:
    "Built-in KB Labs StateBroker-backed cache; no Redis server required",
  author: "KB Labs Team",
  license: "KBPL-1.1",
  type: "core",
  implements: "ICache",
  defaultFor: ["cache"],
  capabilities: {
    custom: {
      ttl: true,
      patterns: true,
      atomic: true,
      sortedSets: true,
    },
  },
  configSchema: {
    namespace: {
      type: "string",
      default: "kb:",
      description: "Namespace prefix for cache keys",
    },
  },
};
