---
id: S-123
title: Studio — Use the installed Commit plugin page
persona: solo-developer
area: studio
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [studio]
---

## Goal

After choosing to install the Commit plugin into a project, use its Studio contribution to inspect changed files and generate a commit plan; uninstalling the plugin removes its page.

## Prerequisites

- [ ] Initialized Git project registered with the platform, containing at least one tracked-file change and no staged commit plan.
- [ ] Commit plugin's exact marketplace version identified and an LLM adapter available for plan generation.

## Steps

| # | Action | Expected |
|---|--------|----------|
| S-123.1 | From the project root run `kb marketplace install @kb-labs/commit-entry@<version>` | The selected project's install record contains the requested exact Commit plugin version; marketplace installation itself is covered by S-010/S-047. |
| S-123.2 | Refresh Studio for that project and open the Commit navigation item | The Commit page is present at `/p/commit` and displays the `Commit Plan` and `Files` tabs. |
| S-123.3 | Open the Files tab | The changed tracked file and its Git status are listed for the selected project/repository scope. |
| S-123.4 | Return to Commit Plan and choose `Generate plan` | A generated plan lists conventional commit message(s) and the changed files assigned to each; the page does not create a Git commit until the user chooses Apply. |
| S-123.5 | From the project root run `kb marketplace uninstall @kb-labs/commit-entry`, then reload Studio | The Commit navigation item and `/p/commit` contribution are absent for that project. |
