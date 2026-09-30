# @kb-labs/marketplace

Marketplace manages plugin and adapter packages for an installed KB Labs platform. The host owns its HTTP service; do not start a standalone marketplace daemon.

## Project plugins

Run from the project root. Schema v3 of `.kb/marketplace.lock` records each plugin's exact version, tarball integrity, source kind, and enabled state. It contains no machine-specific path or install timestamp; runtime resolution uses the machine's shared extension store. Install does not update project `package.json`, the package-manager lockfile, or `node_modules`. Run `kb marketplace sync` to regenerate a v2 lock.

```bash
kb marketplace install @kb-labs/commit-entry@1.2.3
kb marketplace plugins list
kb marketplace plugins disable @kb-labs/commit-entry
kb marketplace plugins enable @kb-labs/commit-entry
kb marketplace uninstall @kb-labs/commit-entry
```

For a local plugin, build it and pass an explicit relative link path:

```bash
pnpm build
kb marketplace link --link .kb/plugins/my-plugin/packages/my-plugin-entry
```

## Machine adapters

Adapters are installed in the platform package graph. Bind the installed package to a project slot, then set that project's options:

```bash
kb marketplace install @kb-labs/adapters-fs@1.2.3 --scope platform
kb adapter bind storage @kb-labs/adapters-fs
kb config set platform.adapterOptions.storage '{"basePath":".kb/cache"}'
```

The bind command verifies that the platform marketplace lock records the package as an adapter before writing `platform.adapters.<slot>` in the current project's config.
