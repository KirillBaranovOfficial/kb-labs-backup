# Product decisions log

This log records the product decisions behind the case catalog (`docs/qa/scenarios/`). Cases state *what* the product does. This log records *why* a boundary sits where it does, so a later change of mind can be made deliberately, starting with a new entry and a case change.

Each entry names the decision, the reason, and the cases that encode it. When a decision changes, add a new dated entry that supersedes the old one; don't rewrite history.

## 2026-10-01

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

### Studio first tier

The first-tier Studio pages are the current dashboard (described as it is), analytics, observability, user administration, projects, workflows, the marketplace, and the Commit plugin page as proof that marketplace plugins load.
Cases: S-020, S-046, S-106 to S-108, S-120 to S-125.

### History retention

There is no retention policy for run history yet.

### Developer tooling comes last

kb-dev and kb-devkit are functionally solid and need cases and tests only. kb-deploy and kb-monitor need stabilization. Deployment and container cases (S-004, S-034 to S-038) stay planned and outside the release until that phase.

## 2026-10-02

### Platform members: core plugins only

The platform ships its core plugins as members: `workflow` (the CLI for the workflow service), `marketplace` (installing the rest) and `scaffold` (plugin authoring). Commit, Review, Quality, Release and Steward are marketplace plugins and are not platform members. This applies the 2026-10-01 release-scope decision to the release configuration (`release.flows.platform.platformMembers`) and to the journey candidate. Before this change, the journey candidate didn't install the workflow CLI at all, so `kb workflow …` was unknown on installed platforms.
Cases: S-006, S-008, S-010, S-160.

### One storage default

`@kb-labs/adapters-fs` is the only adapter that declares itself the default for `storage`. `@kb-labs/data-store` stays installable and bindable, but two defaults made install resolution ambiguous.
Cases: S-047, S-048.

