# Product decisions log

This log records the product decisions behind the case catalog (`docs/qa/scenarios/`). Cases state *what* the product does. This log records *why* a boundary sits where it does, so a later change of mind can be made deliberately, starting with a new entry and a case change.

Each entry names the decision, the reason, and the cases that encode it. When a decision changes, add a new dated entry that supersedes the old one; don't rewrite history.

## 2026-10-01

### Project membership comes from the platform registry

`kb project add <path>` registers the canonical path regardless of whether the repository contains `.kb/`. The platform does not create files in the user's repository during registration, and a repository's `.kb/` directory is not the authority for whether it is a project. Project configuration and state ownership are tracked as separate follow-up work.
Cases: S-043, S-124, S-187.

### Adapters and LLM settings are platform-only

Adapter bindings, adapter options, LLM credentials, and model selection are
configured once in platform config and apply to every registered project.
Projects may attach plugins at project scope, but cannot configure or override
adapters. A project config containing `platform.adapters` or
`platform.adapterOptions` fails with typed code `KB_ADAPTER_PROJECT_SCOPE` and
an actionable hint to move those fields to the platform config. This removes
project-level adapter configuration from the config schema/loader, CLI, REST,
Studio, health, doctor, and user documentation.
Cases: S-017, S-018, S-048; S-200 is historical and superseded by S-048.

### Plugin settings are platform-owned and project-scoped

Plugin defaults are layered with platform defaults and per-project overrides.
Project config is stored by `projectId` in platform state; the platform never
reads project plugin settings from `.kb/kb.config.*` in the consumer
repository. `kb config` resolves project scope from cwd, Studio edits the
selected project, invalid values are checked against the plugin's declared
settings schema, and removing a project archives its config for restoration
when the same deterministic project ID is registered again. The SDK currently
has no plugin settings-schema declaration; use the existing manifest
`SchemaRef` for the planned declaration rather than adapter `configSchema`.
Cases: S-043, S-124, S-196, S-204.

### Cases are the product specification

The case catalog is the product truth for code, tests and docs. Work is case-first TDD: case → e2e claim (red) → integration and unit tests → implementation. See [ADR-0050](../adr/0050-product-cases-are-the-specification.md).

### Release scope: platform core plus the Commit plugin

Plugins don't ship with the platform. Users install them from the marketplace by choice. Release cases cover the platform core and the Commit plugin installed from the marketplace. Other plugins (Review, Mind, Agents, Release, QA, Inbox, ClickUp, Steward, DevLink, Site Tools, Quality) remain marketplace plugins outside the release contour; their roster will be decided separately.
Cases: S-010, S-011, S-012, S-047, S-123, S-160.

### Supported operating systems: Linux and macOS

Windows is not supported, so no Windows installer is shipped. A macOS journey lane is required once CI is available again.
Cases: S-053.

### No self-registration

The first administrator bootstraps the platform and has an offline recovery path (`kb-create doctor --recover`). Users join only through admin invites. Members see no user administration and are denied direct access to it. Machine registration for host agents is not human self-registration: it stays admin-gated and outside the release.
Cases: S-100, S-104.6, S-105, S-106, S-107, S-108.

### Roles, invites, reset links and API tokens

- Two roles:
  - `admin`: everything, including inviting admins or members;
  - `member`: projects, plugins and workflows, with no user management.
- Admins can change roles. The last active admin can't be disabled or demoted.
- Invites are valid for 7 days, reset links for 24 hours, and both are single-use. Admins generate a reset link and share it themselves; email delivery comes later through the notifier adapter.
- API tokens inherit the user's role, can have an optional expiry, and are revocable.
Cases: S-101 to S-108.

### Built-in local adapter defaults

A built-in default adapter pack ships with the platform. Every capability that something local can serve without external dependencies gets a default: storage (filesystem), cache, logs, state and queues. LLM and vector store have no default, because a local one can't be guaranteed. Features that need them fail with a typed "adapter required" error and a `kb adapter bind` hint.
Cases: S-047, S-048, S-160.2, S-123.

### Commit plugin without an LLM

Generating a commit plan needs an LLM, so without a bound LLM adapter the Commit plugin returns the typed "LLM adapter required" error instead of silently falling back to a heuristic plan. The release journey binds a deterministic test LLM adapter through normal adapter configuration; that adapter is test infrastructure, not a product shortcut.
Cases: S-160, S-123.

### Scheduling: workflow, job, cron

- A **workflow** is an automation scenario, a **job** is a background task, and a **cron** is a schedule.
- A cron is its own entity in `.kb/crons/<id>.yml`: schedule, timezone (default UTC), enabled, and a target (`workflow: <id>` with input, or a job).
- Missed runs are not caught up.
- Crons are managed through `kb workflow crons list|enable|disable`, the Studio Crons page and `/api/v1/crons`. Plugin manifest crons stay.
- The old `.kb/jobs` inline-workflow schedule is removed without compatibility.
- A project with enabled schedules keeps its runtime warm.
- Workflow run records persist through the storage adapter.
Cases: S-044 and the cron cases.

### Workflow operations in the release

Cancel and rerun are release behavior. Restart-from-step and approvals are active but don't gate a release. Inbound webhook triggers are not supported: there is no public ingress, authentication or retry contract, so the unreachable `on.webhook` trigger shape is removed.
Cases: S-008, S-143, S-144, S-145, S-032 (historical).

### Project MCP

- Commands of installed plugins automatically become project MCP tools.
- In secured mode, access uses the user's API token with that user's role.
- Tool failures are `CallToolResult` values with `isError: true`, the ADR-0049 envelope as `structuredContent`, and a `"<code>: <message> — <hint>"` text line; protocol errors stay JSON-RPC errors.
- Project MCP is release; agent sessions and host-agent registration are not.
Cases: S-045, S-140, S-141, S-142.

### Platform update and rollback

- Two compatibility policies:
  - `strict`: the exact release matrix;
  - `compatible`: semver ranges, the installer default.
- Update checks every installed plugin and adapter from the receipt and refuses, naming the one that would break.
- A downgrade through `update` is refused with a hint to use `rollback`.
- `status` and `doctor` show the channel and the exact version.
- Installing and updating from the canary channel is supported.
Cases: S-023, S-024, S-053.

### Recovery hints on installed platforms

Error hints on an installed platform point to `kb-create status` and `kb-create doctor --platform-root <path>`. They don't point to `kb-dev`, which is a development tool.
Cases: S-125, S-100.

### Installed artifact manifests come from the sealed plan

`kb-create` materializes each selected package's `kb-create.manifest.json` from that artifact's config requirements in the sealed release index after every pnpm operation that can replace package directories, including install, update, and snapshot restore. The release tarball stays unchanged, so its published bytes and sealed checksum remain the same; doctor reads the exact metadata the installer resolved and recorded in its receipt. pnpm links `node_modules/<package>` into its virtual store, so a later `pnpm install` can replace that directory and remove launcher-written files.
Cases: S-054.2, S-024.5.

### Studio first tier

The first-tier Studio pages are the current dashboard (described as it is), analytics, observability, user administration, projects, workflows, the marketplace, and the Commit plugin page as proof that marketplace plugins load.
Cases: S-020, S-046, S-106 to S-108, S-120 to S-125.

### History retention

There is no retention policy for run history yet.

### Developer tooling comes last

kb-dev and kb-devkit are functionally solid and need cases and tests only. kb-deploy and kb-monitor need stabilization. Deployment and container cases (S-004, S-034 to S-038) stay planned and outside the release until that phase.

### One UX outcome contract across every surface

All TypeScript and Go CLIs and HTTP/MCP machine results use `{ok:true,data,meta?}` for success and `{ok:false,error}` for failure. Failures use the existing platform `ErrorEnvelope` and `KB_*` catalog; unknown failures use `KB_RUNTIME_UNEXPECTED` with a correlation ID and a stack only in structured logs. CLI exit codes are shared: 0 for a normally completed command call, 2 for invalid usage/input, and 1 for other operation failures. A normal return, including void, verifies completion of the command call only; any product postcondition is owned by its case. Human errors use one renderer for code, message, known cause, and next step. Registry-driven coverage is required across commands, HTTP routes, MCP tools, and Studio error boundaries.
Cases: S-203.

### Workspace CLI resolves configured adapters from the workspace

In a monorepo development checkout, configured adapter packages resolve directly from pnpm workspace package roots only when the platform-owned config contains `"platform": { "mode": "development" }`. This explicit marker is absent from kb-create's rendered installed-platform config; a retained `pnpm-workspace.yaml` does not enable workspace resolution. The root package does not mirror every adapter binding as a dependency, because that duplicates `.kb/kb.config.jsonc` and can drift. Adapter classification follows the KB manifest, and only configured platform adapters resolve from the platform workspace. A bound invalid package returns typed `KB_HOST_ADAPTER_UNAVAILABLE`; if the package is absent from both workspace and lock, the error directs the operator to `kb marketplace install <package> --scope platform`. An unbuilt workspace adapter returns the build command `pnpm build`.
Cases: S-179.5, S-179.6, S-179.7, S-179.8, S-179.9, S-179.10, S-179.11, S-179.12.

## 2026-10-02

### System CLI commands declare startup scope

Every registered system CLI command declares whether startup requires a
registered project. Plugin commands are always project-scoped because plugins
attach to projects (ADR-0052). Global help without a command and version are
platform-scoped global flags. `project show` is platform-scoped and resolves an
explicit reference itself, or looks up cwd when no reference is given; an
unknown cwd returns `KB_PROJECT_UNKNOWN`. Scope and auth exemption remain
independent declarations.
Cases: S-124, S-054.17, PC-002.6.

### Platform CLI authentication gate

The platform owns one deny-by-default authentication gate at each entry point. CLI wraps every system and plugin command before dispatch; only its platform-owned auth/help/version/bootstrap/diagnostic allowlist bypasses the gate. Plugins and services declare no auth metadata. The Gateway authenticates globally before routing to services, project runtimes, or plugin REST handlers; only explicit public routes pass anonymously. Manifest-declared webhook routes are authenticated by their declared webhook verifier (secret, HMAC, or custom validator), not by a user session; undeclared paths cannot bypass the gateway gate by resembling webhook routes. MCP authenticates at its transport boundary before session or tool dispatch and retains HTTP 401 for unauthenticated clients. In all entry points, authentication precedes permissions and product preconditions. Local mode is a no-op. Authenticated authorization failures use `KB_AUTH_FORBIDDEN` with the permission the Gateway checked, or a catalog message that does not name a role when no permission is available.

A CORS preflight (`OPTIONS` with `Access-Control-Request-Method`) receives only a CORS response and never dispatches to a session, tool, service, or plugin handler; ordinary OPTIONS requests remain authenticated.

`/health/auth` is a self-guarded loopback operator route: the platform gate lets only this exact route reach its own checks for loopback peer, loopback Host, and absent forwarding headers; all other callers receive a plain 404 before readiness is computed. `/health`, `/health/adapters`, and `/ready` remain public because health probes need coarse readiness before login. `/auth/token` remains public because its client-credential body is the proof used to obtain a machine session. Public marketplace-registry package-list, package-metadata, and package-stats GET routes remain available without a user session; package visibility still goes through `canAccess`, using the optional identity forwarded by the Gateway when present.

Owner intent (2026-10-02): “главное сделать платформенно (чтобы все подключенные плагины сразу получали защиту от доступа) без необходимости добавлять что-то в потребитель. по сути для разных точек разная защита — CLI - общая обертка над всеми командами (до вызова команды идет проверка авторизация и только после доступ), gateway - тоже все запросы не публичные перехватывает и отдает ошибку до того как запрос дойдет до сервисов. ну и MCP аналогично”

Cases: S-007.4, S-041.7 to S-041.13, S-045.5, S-104, S-140.6.

### Bind persistent built-in adapters on every fresh install

"The idea is that the user has no headache at the base level and doesn't need to configure everything from scratch. Instead of an InMemory fallback, the minimum is FS/SQLite or something cheap and easy to install. No local databases with lots of replicas are needed, but later the user can replace adapters with the ones they need."

Fresh local and secured installs resolve the same built-in adapter for every platform slot with a persistent local implementation, including when the user has not added a binding. Defaults are replaceable through `kb adapter bind <slot> <package>`; replacing a provider does not migrate or delete data held by the previous provider. An available persistent adapter must never be silently replaced by an InMemory fallback. OpenAI remains the implicit LLM provider; its adapter can construct without a key and reports `KB_ADAPTER_CREDENTIALS_MISSING` only when used. Missing credentials for optional capabilities are warning-severity doctor findings and do not make an otherwise healthy install fail; use reports the typed credentials error. Adapter manifests may declare required credential options as secret values with environment sources; these credentials may be missing at construction, while required structural options such as a service map exclude a provider from implicit defaults. Embeddings and vector store remain unconfigured and return typed adapter-required errors without configured providers. Durable account, workflow, KV, document, and analytics data survives a host restart. Doctor diagnosis is read-only; `kb-create doctor --fix` is the explicit repair operation for missing installed manifest projections.

The sealed base install includes `@kb-labs/adapters-openai` so the runtime can
select it through `defaultFor: ["llm"]` without writing an explicit
`platform.adapters.llm` binding. With `OPENAI_API_KEY` unset, adapter list and
doctor report the missing credential as a warning; an LLM request returns the
typed credentials error.
Cases: S-054.1, S-054.2, S-054.9, S-054.10, S-054.11.

### 2026-10-01: Explicit token kinds and lifecycle contract changes

Every JWT carries an explicit token kind. User access, user refresh, machine access, and machine refresh tokens use distinct `type` values, and each verifier accepts only its own kind. There is no compatibility for tokens issued before this contract. When an update crosses this contract change, saved user sessions become invalid and the CLI reports `KB_AUTH_REQUIRED` with the `kb auth login` hint so users can sign in again. Machine credentials are re-established automatically: hosts register and obtain fresh machine tokens on start, including after rollback to a release that does not recognize the new token contract. Cases: S-041.4, S-023, S-024.

### 2026-10-06: Saved user sessions survive same-contract platform lifecycle changes

A saved CLI user session survives platform update, rollback, and host restart while the token contract remains unchanged. If a host rejects a saved token, protected commands return `KB_AUTH_REQUIRED` with the login hint. The host machine connection still authenticates from stored machine registration credentials on every start. No legacy token compatibility path is retained. This supersedes the 2026-10-01 lifecycle expectation for updates that do not change the token contract. Cases: S-041.4, S-023, S-024.

### Platform members: core plugins only

The platform ships its core plugins as members: `workflow` (the CLI for the workflow service), `marketplace` (installing the rest) and `scaffold` (plugin authoring). Commit, Review, Quality, Release and Steward are marketplace plugins and are not platform members. This applies the 2026-10-01 release-scope decision to the release configuration (`release.flows.platform.platformMembers`) and to the journey candidate. Before this change, the journey candidate didn't install the workflow CLI at all, so `kb workflow …` was unknown on installed platforms.
Cases: S-006, S-008, S-010, S-160.

### 2026-10-01: Platform installs seal runtime peer dependencies

The platform release index includes every runtime peer dependency reachable
from its installed platform members and SDK, recursively. SDK peers are shared
platform copies for all plugins, so the platform install owns those artifacts;
plugins do not install their own copies. The release sealer derives this set
from the exact staged package manifests and published `dist` imports. Internal
imports must resolve through a declared dependency or a peer sealed in the
platform set; external packages resolve through the npm dependency graph.
Packages imported at runtime remain peers when the host provides the
shared copy; ordinary package dependencies remain dependencies. The SDK does
not bundle peer packages such as `shared-tool-kit` and `shared-command-kit` as
special cases.

The release sealer analyzes SDK exports except subpaths declared in
`kbLabs.release.nonRuntimeEntrypoints`; test and build helper subpaths are
omitted there, while new exports remain runtime by default. Optional SDK peers
are consumer-provided and are not platform members.
If a declared runtime entrypoint statically imports an optional peer, sealing
fails so runtime code cannot rely on a package the platform does not provide.

Cases: S-003.1, S-054.2.

### Adapter installs preserve sealed platform artifacts

Platform adapters remain installed in the platform root because runtime adapter discovery reads their package paths from the platform marketplace lock. Before adding an adapter, marketplace records exact `file:.kb/v2/cache/packages/*.tgz` dependencies as pnpm overrides, so pnpm cannot replace sealed packages while resolving the new adapter. Plugin packages continue to use the shared extension store at both scopes.
Cases: S-010.1, S-054.11.

### The gateway-to-service secret is required and installer-managed

`GATEWAY_INTERNAL_SECRET` is required by the gateway, REST API, and marketplace-registry in every mode because each service boundary denies requests without the shared value. A fresh local or secured kb-create install generates it in the private secret store, passes it to every protected service, and retains the same value through update and rollback. Container and team deployments provide the required variable through their deployment secret configuration; startup fails with `KB_CONFIG_SECRET_MISSING` when it is absent. Users of kb-create never enter this secret manually.
Cases: S-038.1 to S-038.7.

### Superseding decision: kb-create owns sealed artifact overrides

The installed platform's package-manager configuration belongs to kb-create,
which seals the release artifacts. Install and update write an override for
every sealed package to its exact cached `file:` archive; snapshots include
`pnpm-workspace.yaml`, so rollback restores the previous pins. Marketplace
never edits package-manager configuration. A platform without a required pin
gets a typed error directing its operator to `kb-create update` rather than an
automatic repair. This keeps direct and transitive instances of a sealed
package on the same artifact and makes lifecycle recovery deterministic.
Cases: S-010.1, S-054.11, S-054.12.

### One storage default

`@kb-labs/adapters-fs` is the only adapter that declares itself the default for `storage`. `@kb-labs/data-store` stays installable and bindable, but two defaults made install resolution ambiguous.
Cases: S-047, S-048.

### Marketplace plugins never mutate the sealed platform dependency graph

Marketplace plugin entry packages use the shared extension store at both project
and platform scope. The selected scope still controls the marketplace lock and
visibility; it does not decide whether pnpm may rewrite the installed platform's
package graph. Platform adapters remain installed in the platform graph because
the host resolves them as platform runtime providers. This supersedes ADR-0048
D1's platform-scope plugin placement, which predates sealed installed receipts.
Cases: S-010.1, S-123.1.

### Proposed: sealed service lifecycle after installation — needs owner confirmation

Recommend selective service installation from the platform's sealed release
index, with `kb-create service add|enable|disable` operating on the installed
receipt. Adding a service should resolve only artifacts in a verified index,
preserve the existing service set on failure, and record a snapshot before
changing the installation. Enabling or disabling a service should update the
host's managed service state and make its associated CLI plugin available or
unavailable with it where a service has an associated CLI plugin. Updates
preserve the installed service set and enabled states; rollback restores both
from the snapshot. Platform-root operators
manage local installs; secured-mode service changes require a platform
administrator. This avoids paying the package, install, and operational cost
of every official service on every install while retaining a convenient path to
add services later. Marketplace installation remains the plugin mechanism and
does not own service artifacts.

Cases: S-194. This is a proposal only; confirm the command and secured-mode
authorization surface before implementation.

### 2026-10-02: CLI config absence is valid; present broken config is an error

Platform and project config layers are optional. An installed platform can use
its built-in adapter manifests when it has no user-written platform config, and
a source checkout can omit `platform.mode`; absence does not imply malformed
configuration or trigger a setup error. When a config file is present but
cannot be read, parsed, or validated, CLI startup returns a shared typed config
error before running a command. It must not silently replace that configuration
with NoOp adapters. Adapter initialization degradation remains governed by
S-054.2 and is not covered by this config-load decision.

System logs query platform and host records without project registration.
Project-specific workflow log views remain scoped to their selected project.
`mcp config` stays project-scoped because its URL and result contain the
registered project identity. `docs generate-cli-reference` stays
project-scoped because it writes documentation into the project tree. The
simple `hello` command is platform-scoped because it reads no project state and
writes no project files. `config get`, `config set`, and `config show` are
project-context commands: their default/effective view includes project state,
while explicit `--scope platform|project` selects the corresponding user config
file from any registered project cwd. Platform adapter changes require an
explicit host restart, which `config set --scope platform` reports as the exact
next command.

Cases: PC-002.7, S-003.7, S-017.5, S-018.3, S-054.2, S-054.18, S-199.6,
S-201.1–S-201.2, S-203.7–S-203.8.

## 2026-10-06

### Enabled plugin identity must match its manifest

An enabled marketplace lock entry whose canonical package/plugin ID differs
from `ManifestV3.id` is invalid and contributes no commands or other entities.
Discovery retains a typed diagnostic, CLI unknown-command output points to
`kb marketplace plugins doctor --scope <scope>`, and plugin doctor lists the
failed enabled entry. No silent continue is allowed. Cases: S-203.18.

### Gateway authentication exposes only safe rejection categories

Protected Gateway rejections may include `details.reason` only from the closed
set `expired`, `invalid`, `unknown-issuer`, `inactive`, and `revoked`. Missing
or invalid credentials use `KB_AUTH_REQUIRED`; expired, revoked, and inactive
credentials use their catalogued actionable auth code. The Gateway logs the
same category with the request correlation ID. It never returns raw verifier
messages, token claims, token IDs, or secret material. Cases: S-203.17.

### Plugin command declarations have one shared schema

The `@kb-labs/plugin-contracts` CLI declaration schema is the source of truth
for the SDK authoring types, CLI registration, and discovery. CLI flag names
use lowercase letters, digits, and hyphens; choices apply only to string
flags. Invalid first-party declarations fail the workspace manifest test and
enabled invalid plugins receive a retained discovery diagnostic. Discovery
and CLI execution use one handler-path resolver. Cases: S-203.18.

### Gateway credential failures use catalogued actionable codes

Missing and invalid credentials remain `KB_AUTH_REQUIRED`. Expired access
credentials use `KB_AUTH_TOKEN_EXPIRED`; revoked sessions and inactive users
use their own catalogued codes. Safe `details.reason` values remain available
for automation. The CLI renders the returned catalog envelope and fills its
Gateway URL template from the saved auth context. KB-signed JWTs use an
explicit issuer; tokens without that issuer require a fresh login. Cases:
S-203.17.

### macOS user temp directories are valid plugin workspaces

The hardcoded filesystem boundary denies operating-system paths such as `/etc`,
`/usr`, and system-owned `/var` content. macOS places the current user's private
temporary directory under `/var/folders`, often exposed through `/var` while
resolving to `/private/var`; that directory is valid project cwd and output
storage. Resolve paths through symlinks before checking the boundary and exempt
only paths inside the canonical macOS user temp directory. Other `/var` paths
remain denied.
Cases: S-210.

### Product analytics excludes raw adapter operation telemetry

`IAnalytics` stores product actions and usage that answers a product question.
Raw cache, storage, and vector-store calls are adapter telemetry and must not
be written as analytics events. LLM and embeddings events remain because their
usage and cost inform product usage. Adapter errors belong in contextual
operational logs; operation counts and latency belong in metrics where the
runtime exposes a metrics registry. The current core platform has no shared
adapter metrics registry, so it does not synthesize analytics or per-call log
events as a substitute. Local SQLite analytics retains 90 days by default;
`platform.adapterOptions.analytics.retentionDays` overrides the age in days.
Cases: S-002, S-121, S-191.
