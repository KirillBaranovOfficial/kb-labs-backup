---
id: S-011
title: Marketplace — Enable / disable plugin
persona: solo-developer
area: marketplace
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal
Developer can temporarily disable a plugin without uninstalling it, and re-enable it later.

## Prerequisites
- [ ] At least one plugin installed (e.g. from S-010)
- [ ] Services running

---

## Steps

### Phase 1 — Disable

| # | Action | Expected | Actual | Status |
|---|--------|----------|--------|--------|
| S-011.1 | `kb marketplace plugins disable <name>` | Plugin marked disabled | | ⬜ |
| S-011.2 | `kb --help` after disable | Plugin commands no longer visible | | ⬜ |
| S-011.3 | `cat .kb/marketplace.lock` | Plugin entry has `"enabled": false` | | ⬜ |

### Phase 2 — Re-enable

| # | Action | Expected | Actual | Status |
|---|--------|----------|--------|--------|
| S-011.4 | `kb marketplace plugins enable <name>` | Plugin re-enabled | | ⬜ |
| S-011.5 | `kb --help` | Plugin commands back | | ⬜ |

### Phase 3 — Edge cases

| # | Action | Expected | Actual | Status |
|---|--------|----------|--------|--------|
| S-011.6 | Disable a plugin that has running commands | Graceful — completes current, then disables | | ⬜ |
| S-011.7 | Disable non-existent plugin | Clear error: "plugin not found" | | ⬜ |

---

## Result
## Bugs
## Notes