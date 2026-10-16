# KB Labs E2E Checklist

> Auto-generated — do not edit manually.
> Source: `e2e/tests/specs/`. Run `pnpm test` to regenerate.

```
Last run: 2026-10-16T13:09:53.134Z
Passed: 0  Failed: 193  Planned: 14
```

## Services

| ID | Scenario | Spec | Status |
|----|----------|------|--------|
| SD-01 | PUT then GET returns stored value | `specs/services/state.spec.ts` | ❌ FAILED |
| S-01 | gateway is healthy | `specs/services/health.spec.ts` | ❌ FAILED |
| R-01 | rest-api /ready — registry loaded and plugins mounted | `specs/services/ready.spec.ts` | ❌ FAILED |
| PD-01 | gateway health reports consistent version | `specs/services/peer-deps.spec.ts` | ❌ FAILED |
| SD-01 | PUT then GET returns stored value | `specs/services/state.spec.ts` | ❌ FAILED |
| PD-01 | gateway health reports consistent version | `specs/services/peer-deps.spec.ts` | ❌ FAILED |
| R-01 | rest-api /ready — registry loaded and plugins mounted | `specs/services/ready.spec.ts` | ❌ FAILED |
| S-01 | gateway is healthy | `specs/services/health.spec.ts` | ❌ FAILED |
| SD-02 | DELETE removes the key (GET returns 404 after) | `specs/services/state.spec.ts` | ❌ FAILED |
| R-02 | workflow /ready — engine + catalog + scheduler ready | `specs/services/ready.spec.ts` | ❌ FAILED |
| PD-02 | services report consistent platform version | `specs/services/peer-deps.spec.ts` | ❌ FAILED |
| S-02 | rest-api is healthy | `specs/services/health.spec.ts` | ❌ FAILED |
| SD-02 | DELETE removes the key (GET returns 404 after) | `specs/services/state.spec.ts` | ❌ FAILED |
| S-02 | rest-api is healthy | `specs/services/health.spec.ts` | ❌ FAILED |
| PD-02 | services report consistent platform version | `specs/services/peer-deps.spec.ts` | ❌ FAILED |
| R-02 | workflow /ready — engine + catalog + scheduler ready | `specs/services/ready.spec.ts` | ❌ FAILED |
| SD-03 | GET on missing key returns 404 | `specs/services/state.spec.ts` | ❌ FAILED |
| S-03 | marketplace is healthy | `specs/services/health.spec.ts` | ❌ FAILED |
| R-03 | state-daemon /ready — broker ready | `specs/services/ready.spec.ts` | ❌ FAILED |
| SD-03 | GET on missing key returns 404 | `specs/services/state.spec.ts` | ❌ FAILED |
| S-03 | marketplace is healthy | `specs/services/health.spec.ts` | ❌ FAILED |
| R-03 | state-daemon /ready — broker ready | `specs/services/ready.spec.ts` | ❌ FAILED |
| SD-04 | stored value can be overwritten | `specs/services/state.spec.ts` | ❌ FAILED |
| S-04 | workflow is healthy | `specs/services/health.spec.ts` | ❌ FAILED |
| R-04 | rest-api /ready — all plugin routes registered | `specs/services/ready.spec.ts` | ❌ FAILED |
| SD-04 | stored value can be overwritten | `specs/services/state.spec.ts` | ❌ FAILED |
| S-04 | workflow is healthy | `specs/services/health.spec.ts` | ❌ FAILED |
| R-04 | rest-api /ready — all plugin routes registered | `specs/services/ready.spec.ts` | ❌ FAILED |
| SD-05 | TTL expires entry (1s TTL, checked after 1.5s) | `specs/services/state.spec.ts` | ❌ FAILED |
| S-05 | state-daemon is healthy | `specs/services/health.spec.ts` | ❌ FAILED |
| R-05 | marketplace /ready — registry loaded | `specs/services/ready.spec.ts` | ❌ FAILED |
| SD-05 | TTL expires entry (1s TTL, checked after 1.5s) | `specs/services/state.spec.ts` | ❌ FAILED |
| S-05 | state-daemon is healthy | `specs/services/health.spec.ts` | ❌ FAILED |
| R-05 | marketplace /ready — registry loaded | `specs/services/ready.spec.ts` | ❌ FAILED |
| SD-06 | stats reflect stored entries | `specs/services/state.spec.ts` | ❌ FAILED |
| SD-06 | stats reflect stored entries | `specs/services/state.spec.ts` | ❌ FAILED |
| SD-07 | clear with pattern removes matching keys | `specs/services/state.spec.ts` | ❌ FAILED |
| SD-07 | clear with pattern removes matching keys | `specs/services/state.spec.ts` | ❌ FAILED |

## Platform

| ID | Scenario | Spec | Status |
|----|----------|------|--------|
| CFG-01 | platform config endpoint responds | `specs/platform/config.spec.ts` | ❌ FAILED |
| CFG-01 | platform config endpoint responds | `specs/platform/config.spec.ts` | ❌ FAILED |
| CFG-02 | platform config has adapters section (no empty config) | `specs/platform/config.spec.ts` | ❌ FAILED |
| CFG-02 | platform config has adapters section (no empty config) | `specs/platform/config.spec.ts` | ❌ FAILED |
| CFG-03 | LLM adapter is configured (not placeholder) | `specs/platform/config.spec.ts` | 📋 planned |
| CFG-04 | storage adapter is configured | `specs/platform/config.spec.ts` | 📋 planned |
| DOC-01 | gateway /health/adapters returns adapter table | `specs/platform/doctor-degradation.spec.ts` | ❌ FAILED |
| DOC-01 | gateway /health/adapters returns adapter table | `specs/platform/doctor-degradation.spec.ts` | ❌ FAILED |
| ROOT-01 | platform config endpoint is reachable and schema is valid | `specs/platform/roots.spec.ts` | ❌ FAILED |
| PL-01 | plugin registry is populated after startup | `specs/platform/plugins.spec.ts` | ❌ FAILED |
| DOC-02 | slots with inmemory fallback report implementation name containing "InMemory" | `specs/platform/doctor-degradation.spec.ts` | ❌ FAILED |
| ROOT-01 | platform config endpoint is reachable and schema is valid | `specs/platform/roots.spec.ts` | ❌ FAILED |
| PL-01 | plugin registry is populated after startup | `specs/platform/plugins.spec.ts` | ❌ FAILED |
| DOC-02 | slots with inmemory fallback report implementation name containing "InMemory" | `specs/platform/doctor-degradation.spec.ts` | ❌ FAILED |
| PL-02 | plugin registry health — no validation errors | `specs/platform/plugins.spec.ts` | ❌ FAILED |
| ROOT-03 | plugin registry uses projectRoot for local plugin discovery | `specs/platform/roots.spec.ts` | ❌ FAILED |
| DOC-03 | slots with noop fallback report implementation name containing "NoOp" | `specs/platform/doctor-degradation.spec.ts` | ❌ FAILED |
| ROOT-03 | plugin registry uses projectRoot for local plugin discovery | `specs/platform/roots.spec.ts` | ❌ FAILED |
| PL-02 | plugin registry health — no validation errors | `specs/platform/plugins.spec.ts` | ❌ FAILED |
| DOC-03 | slots with noop fallback report implementation name containing "NoOp" | `specs/platform/doctor-degradation.spec.ts` | ❌ FAILED |
| ROOT-04 | start with custom projectRoot env var → config reflects that root | `specs/platform/roots.spec.ts` | 📋 planned |
| ROOT-05 | workflow daemon discovers .kb/workflows from projectRoot not platformRoot | `specs/platform/roots.spec.ts` | 📋 planned |
| ROOT-06 | marketplace lock file is read from projectRoot/.kb/marketplace.lock | `specs/platform/roots.spec.ts` | 📋 planned |
| PL-03 | studio plugin registry loaded (MF pages) | `specs/platform/plugins.spec.ts` | ❌ FAILED |
| DOC-04 | real adapters have no reason field | `specs/platform/doctor-degradation.spec.ts` | ❌ FAILED |
| PL-03 | studio plugin registry loaded (MF pages) | `specs/platform/plugins.spec.ts` | ❌ FAILED |
| DOC-04 | real adapters have no reason field | `specs/platform/doctor-degradation.spec.ts` | ❌ FAILED |
| PL-04 | commit plugin is registered and has expected commands | `specs/platform/plugins.spec.ts` | 📋 planned |
| PL-05 | scaffold plugin is registered and has expected templates | `specs/platform/plugins.spec.ts` | 📋 planned |
| PL-06 | workflow plugin is registered with daemon URL | `specs/platform/plugins.spec.ts` | 📋 planned |
| DOC-05 | recordedAt timestamps are valid ISO 8601 strings | `specs/platform/doctor-degradation.spec.ts` | ❌ FAILED |
| DOC-05 | recordedAt timestamps are valid ISO 8601 strings | `specs/platform/doctor-degradation.spec.ts` | ❌ FAILED |
| DOC-06 | no duplicate slot entries in the registry | `specs/platform/doctor-degradation.spec.ts` | ❌ FAILED |
| DOC-06 | no duplicate slot entries in the registry | `specs/platform/doctor-degradation.spec.ts` | ❌ FAILED |

## Gateway

| ID | Scenario | Spec | Status |
|----|----------|------|--------|
| GW-01 | invalid token on gateway route → 401 | `specs/gateway/auth.spec.ts` | ❌ FAILED |
| XS-01 | authenticated request passes through gateway to REST upstream | `specs/gateway/cross-service.spec.ts` | ❌ FAILED |
| GW-01 | invalid token on gateway route → 401 | `specs/gateway/auth.spec.ts` | ❌ FAILED |
| XS-01 | authenticated request passes through gateway to REST upstream | `specs/gateway/cross-service.spec.ts` | ❌ FAILED |
| GW-02 | missing token on gateway protected route → 401 | `specs/gateway/auth.spec.ts` | ❌ FAILED |
| XS-02 | gateway rejects proxied request without token | `specs/gateway/cross-service.spec.ts` | ❌ FAILED |
| GW-02 | missing token on gateway protected route → 401 | `specs/gateway/auth.spec.ts` | ❌ FAILED |
| XS-02 | gateway rejects proxied request without token | `specs/gateway/cross-service.spec.ts` | ❌ FAILED |
| GW-03 | auth/token endpoint exists and rejects bad credentials | `specs/gateway/auth.spec.ts` | ❌ FAILED |
| GW-03 | auth/token endpoint exists and rejects bad credentials | `specs/gateway/auth.spec.ts` | ❌ FAILED |
| XS-03 | gateway routes requests to correct upstream (workflow) | `specs/gateway/cross-service.spec.ts` | ❌ FAILED |
| GW-04 | /health is public (no auth required) | `specs/gateway/auth.spec.ts` | ❌ FAILED |
| XS-03 | gateway routes requests to correct upstream (workflow) | `specs/gateway/cross-service.spec.ts` | ❌ FAILED |
| XS-04 | expired/invalid token rejected on gateway-owned routes | `specs/gateway/cross-service.spec.ts` | ❌ FAILED |
| GW-04 | /health is public (no auth required) | `specs/gateway/auth.spec.ts` | ❌ FAILED |
| XS-04 | expired/invalid token rejected on gateway-owned routes | `specs/gateway/cross-service.spec.ts` | ❌ FAILED |
| GW-05 | valid token → protected route returns 200 | `specs/gateway/auth.spec.ts` | ❌ FAILED |
| GW-05 | valid token → protected route returns 200 | `specs/gateway/auth.spec.ts` | ❌ FAILED |
| GW-06 | refresh token endpoint works and returns valid access token | `specs/gateway/auth.spec.ts` | ❌ FAILED |
| GW-06 | refresh token endpoint works and returns valid access token | `specs/gateway/auth.spec.ts` | ❌ FAILED |
| GW-07 | register → token → authenticated request succeeds end-to-end | `specs/gateway/auth.spec.ts` | ❌ FAILED |
| GW-07 | register → token → authenticated request succeeds end-to-end | `specs/gateway/auth.spec.ts` | ❌ FAILED |

## Marketplace

| ID | Scenario | Spec | Status |
|----|----------|------|--------|
| ML-01 | install package → appears in GET /packages | `specs/marketplace/lifecycle.spec.ts` | ❌ FAILED |
| ML-01 | install package → appears in GET /packages | `specs/marketplace/lifecycle.spec.ts` | ❌ FAILED |
| ML-02 | disable package → disabled flag set in listing | `specs/marketplace/lifecycle.spec.ts` | ❌ FAILED |
| ML-02 | disable package → disabled flag set in listing | `specs/marketplace/lifecycle.spec.ts` | ❌ FAILED |
| MKT-01 | marketplace lists installed packages | `specs/marketplace/packages.spec.ts` | ❌ FAILED |
| ML-03 | enable package → enabled flag restored in listing | `specs/marketplace/lifecycle.spec.ts` | ❌ FAILED |
| MKT-01 | marketplace lists installed packages | `specs/marketplace/packages.spec.ts` | ❌ FAILED |
| ML-03 | enable package → enabled flag restored in listing | `specs/marketplace/lifecycle.spec.ts` | ❌ FAILED |
| MKT-02 | marketplace diagnostics — lock file OK, no errors | `specs/marketplace/packages.spec.ts` | ❌ FAILED |
| MS-01 | POST /workspace/sync — returns structured response | `specs/marketplace/sync.spec.ts` | ❌ FAILED |
| ML-04 | POST /packages/update — responds without error | `specs/marketplace/lifecycle.spec.ts` | ❌ FAILED |
| MKT-02 | marketplace diagnostics — lock file OK, no errors | `specs/marketplace/packages.spec.ts` | ❌ FAILED |
| MS-01 | POST /workspace/sync — returns structured response | `specs/marketplace/sync.spec.ts` | ❌ FAILED |
| ML-04 | POST /packages/update — responds without error | `specs/marketplace/lifecycle.spec.ts` | ❌ FAILED |
| MKT-03 | install a test package and verify it appears in listing | `specs/marketplace/packages.spec.ts` | ❌ FAILED |
| ML-05 | uninstall package → gone from GET /packages | `specs/marketplace/lifecycle.spec.ts` | ❌ FAILED |
| MS-02 | POST /workspace/sync — idempotent (second call ≤ first) | `specs/marketplace/sync.spec.ts` | ❌ FAILED |
| MKT-03 | install a test package and verify it appears in listing | `specs/marketplace/packages.spec.ts` | ❌ FAILED |
| MKT-04 | install entity from remote registry | `specs/marketplace/packages.spec.ts` | 📋 planned |
| MKT-05 | disable package → commands disappear from CLI | `specs/marketplace/packages.spec.ts` | 📋 planned |
| MS-02 | POST /workspace/sync — idempotent (second call ≤ first) | `specs/marketplace/sync.spec.ts` | ❌ FAILED |
| ML-05 | uninstall package → gone from GET /packages | `specs/marketplace/lifecycle.spec.ts` | ❌ FAILED |
| MS-03 | sync with autoEnable=true — packages appear in listing | `specs/marketplace/sync.spec.ts` | ❌ FAILED |
| MS-03 | sync with autoEnable=true — packages appear in listing | `specs/marketplace/sync.spec.ts` | ❌ FAILED |

## Workflows

| ID | Scenario | Spec | Status |
|----|----------|------|--------|
| W-01 | create workflow run returns run ID | `specs/workflows/basic.spec.ts` | ❌ FAILED |
| WFD-01 | workflow catalog endpoint is accessible | `specs/workflows/discovery.spec.ts` | ❌ FAILED |
| W-01 | create workflow run returns run ID | `specs/workflows/basic.spec.ts` | ❌ FAILED |
| WF-01 | workflow stats endpoint responds with counts | `specs/workflows/engine.spec.ts` | ❌ FAILED |
| WFD-01 | workflow catalog endpoint is accessible | `specs/workflows/discovery.spec.ts` | ❌ FAILED |
| W-02 | GET /runs/:id returns a valid initial status | `specs/workflows/basic.spec.ts` | ❌ FAILED |
| WF-01 | workflow stats endpoint responds with counts | `specs/workflows/engine.spec.ts` | ❌ FAILED |
| J-01 | run → reaches success within 30s | `specs/workflows/journey.spec.ts` | ❌ FAILED |
| WFD-02 | cron catalog is populated from .kb/workflows | `specs/workflows/discovery.spec.ts` | ❌ FAILED |
| W-02 | GET /runs/:id returns a valid initial status | `specs/workflows/basic.spec.ts` | ❌ FAILED |
| WFD-02 | cron catalog is populated from .kb/workflows | `specs/workflows/discovery.spec.ts` | ❌ FAILED |
| WF-02 | workflow catalog is populated (e2e-hello is present) | `specs/workflows/engine.spec.ts` | ❌ FAILED |
| J-01 | run → reaches success within 30s | `specs/workflows/journey.spec.ts` | ❌ FAILED |
| W-03 | e2e-hello workflow reaches completed within 30s | `specs/workflows/basic.spec.ts` | ❌ FAILED |
| WF-02 | workflow catalog is populated (e2e-hello is present) | `specs/workflows/engine.spec.ts` | ❌ FAILED |
| WFD-03 | workflow refresh rescans .kb/workflows without restart | `specs/workflows/discovery.spec.ts` | ❌ FAILED |
| J-02 | run → cancel → status is cancelled or failed | `specs/workflows/journey.spec.ts` | ❌ FAILED |
| W-03 | e2e-hello workflow reaches completed within 30s | `specs/workflows/basic.spec.ts` | ❌ FAILED |
| WFD-03 | workflow refresh rescans .kb/workflows without restart | `specs/workflows/discovery.spec.ts` | ❌ FAILED |
| J-02 | run → cancel → status is cancelled or failed | `specs/workflows/journey.spec.ts` | ❌ FAILED |
| WF-03 | e2e-hello run reaches terminal state within 30s | `specs/workflows/engine.spec.ts` | ❌ FAILED |
| W-04 | e2e-fail workflow reaches failed status within 30s | `specs/workflows/basic.spec.ts` | ❌ FAILED |
| WFD-04 | workflow found by name from .kb/workflows is runnable | `specs/workflows/discovery.spec.ts` | ❌ FAILED |
| WF-03 | e2e-hello run reaches terminal state within 30s | `specs/workflows/engine.spec.ts` | ❌ FAILED |
| J-03 | run detail has required fields and jobs endpoint responds 200 | `specs/workflows/journey.spec.ts` | ❌ FAILED |
| W-04 | e2e-fail workflow reaches failed status within 30s | `specs/workflows/basic.spec.ts` | ❌ FAILED |
| WF-04 | runs list is accessible and includes recent run | `specs/workflows/engine.spec.ts` | ❌ FAILED |
| WFD-04 | workflow found by name from .kb/workflows is runnable | `specs/workflows/discovery.spec.ts` | ❌ FAILED |
| J-03 | run detail has required fields and jobs endpoint responds 200 | `specs/workflows/journey.spec.ts` | ❌ FAILED |
| — | WFD-P01: plugin workflow templates appear in catalog | `specs/workflows/plugin-templates.spec.ts` | ❌ FAILED |
| WFD-05 | workflow defined in projectRoot/.kb/workflows overrides platformRoot definition | `specs/workflows/discovery.spec.ts` | 📋 planned |
| WFD-06 | invalid workflow YAML in .kb/workflows is reported in /ready diagnostics | `specs/workflows/discovery.spec.ts` | 📋 planned |
| WF-04 | runs list is accessible and includes recent run | `specs/workflows/engine.spec.ts` | ❌ FAILED |
| J-04 | list runs → newly started run appears in the list | `specs/workflows/journey.spec.ts` | ❌ FAILED |
| WR-01 | GET /runs — returns a runs array | `specs/workflows/runs.spec.ts` | ❌ FAILED |
| — | WFD-P01: plugin workflow templates appear in catalog | `specs/workflows/plugin-templates.spec.ts` | ❌ FAILED |
| WF-05 | stats running count increases during active run | `specs/workflows/engine.spec.ts` | ❌ FAILED |
| J-04 | list runs → newly started run appears in the list | `specs/workflows/journey.spec.ts` | ❌ FAILED |
| WR-01 | GET /runs — returns a runs array | `specs/workflows/runs.spec.ts` | ❌ FAILED |
| — | WFD-P02: plugin workflow template has source=plugin | `specs/workflows/plugin-templates.spec.ts` | ❌ FAILED |
| WF-05 | stats running count increases during active run | `specs/workflows/engine.spec.ts` | ❌ FAILED |
| WR-02 | GET /runs?status=success — returns only success runs | `specs/workflows/runs.spec.ts` | ❌ FAILED |
| — | WFD-P02: plugin workflow template has source=plugin | `specs/workflows/plugin-templates.spec.ts` | ❌ FAILED |
| WF-06 | e2e-fail workflow ends with failed status | `specs/workflows/engine.spec.ts` | ❌ FAILED |
| WR-02 | GET /runs?status=success — returns only success runs | `specs/workflows/runs.spec.ts` | ❌ FAILED |
| — | WFD-P03: plugin workflow template has description | `specs/workflows/plugin-templates.spec.ts` | ❌ FAILED |
| WF-06 | e2e-fail workflow ends with failed status | `specs/workflows/engine.spec.ts` | ❌ FAILED |
| WR-03 | POST /workflows/:id/runs — returns a valid runId | `specs/workflows/runs.spec.ts` | ❌ FAILED |
| — | WFD-P03: plugin workflow template has description | `specs/workflows/plugin-templates.spec.ts` | ❌ FAILED |
| WF-07 | cron catalog endpoint is accessible | `specs/workflows/engine.spec.ts` | ❌ FAILED |
| WR-03 | POST /workflows/:id/runs — returns a valid runId | `specs/workflows/runs.spec.ts` | ❌ FAILED |
| — | WFD-P04: plugin template is resolvable by ID | `specs/workflows/plugin-templates.spec.ts` | ❌ FAILED |
| WF-07 | cron catalog endpoint is accessible | `specs/workflows/engine.spec.ts` | ❌ FAILED |
| WR-04 | GET /runs/:runId — returns run detail | `specs/workflows/runs.spec.ts` | ❌ FAILED |
| — | WFD-P04: plugin template is resolvable by ID | `specs/workflows/plugin-templates.spec.ts` | ❌ FAILED |
| WR-04 | GET /runs/:runId — returns run detail | `specs/workflows/runs.spec.ts` | ❌ FAILED |
| WR-05 | run reaches terminal state within 30s | `specs/workflows/runs.spec.ts` | ❌ FAILED |
| WR-05 | run reaches terminal state within 30s | `specs/workflows/runs.spec.ts` | ❌ FAILED |
| WR-06 | POST cancel — accepted for a running or pending run | `specs/workflows/runs.spec.ts` | ❌ FAILED |
| WR-06 | POST cancel — accepted for a running or pending run | `specs/workflows/runs.spec.ts` | ❌ FAILED |
| WR-07 | GET /jobs — returns a jobs array | `specs/workflows/runs.spec.ts` | ❌ FAILED |
| WR-07 | GET /jobs — returns a jobs array | `specs/workflows/runs.spec.ts` | ⚠️ skipped |

## Studio

| ID | Scenario | Spec | Status |
|----|----------|------|--------|
| SA-01 | gateway / — returns HTML with <html> tag | `specs/studio/app.spec.ts` | ❌ FAILED |
| SA-01 | gateway / — returns HTML with <html> tag | `specs/studio/app.spec.ts` | ❌ FAILED |
| SA-02 | MF manifest — valid JSON with remotes map | `specs/studio/app.spec.ts` | ❌ FAILED |
| SA-02 | MF manifest — valid JSON with remotes map | `specs/studio/app.spec.ts` | ❌ FAILED |
| SA-03 | MF chunk for workflow plugin resolves | `specs/studio/app.spec.ts` | ❌ FAILED |
| ST-01 | gateway serves studio app | `specs/studio/mf-loading.spec.ts` | ❌ FAILED |
| SA-03 | MF chunk for workflow plugin resolves | `specs/studio/app.spec.ts` | ❌ FAILED |
| MF-01 | MF manifest is served with correct Content-Type | `specs/studio/mf-proxy.spec.ts` | ❌ FAILED |
| SA-04 | REST /api/v1/status responds for studio health bar | `specs/studio/app.spec.ts` | ❌ FAILED |
| ST-01 | gateway serves studio app | `specs/studio/mf-loading.spec.ts` | ❌ FAILED |
| MF-01 | MF manifest is served with correct Content-Type | `specs/studio/mf-proxy.spec.ts` | ❌ FAILED |
| ST-02 | gateway serves MF manifest JSON | `specs/studio/mf-loading.spec.ts` | ❌ FAILED |
| SA-04 | REST /api/v1/status responds for studio health bar | `specs/studio/app.spec.ts` | ❌ FAILED |
| MF-02 | remoteEntry.js served as JavaScript (not HTML/text) | `specs/studio/mf-proxy.spec.ts` | ❌ FAILED |
| ST-02 | gateway serves MF manifest JSON | `specs/studio/mf-loading.spec.ts` | ❌ FAILED |
| SA-05 | static assets have caching headers | `specs/studio/app.spec.ts` | ❌ FAILED |
| MF-02 | remoteEntry.js served as JavaScript (not HTML/text) | `specs/studio/mf-proxy.spec.ts` | ❌ FAILED |
| ST-03 | all MF chunks load without 404 | `specs/studio/mf-loading.spec.ts` | 📋 planned |
| ST-04 | studio app renders without JS console errors | `specs/studio/mf-loading.spec.ts` | 📋 planned |
| SA-05 | static assets have caching headers | `specs/studio/app.spec.ts` | ❌ FAILED |
| MW-01 | REST /api/v1/studio/registry — returns plugin list | `specs/studio/mf-widgets.spec.ts` | ❌ FAILED |
| MF-03 | MF assets have CORS headers (required for cross-origin MF loading) | `specs/studio/mf-proxy.spec.ts` | ❌ FAILED |
| RT-01 | WebSocket upgrade succeeds through gateway (/hosts/connect) | `specs/studio/realtime.spec.ts` | ❌ FAILED |
| MW-01 | REST /api/v1/studio/registry — returns plugin list | `specs/studio/mf-widgets.spec.ts` | ❌ FAILED |
| RT-01 | WebSocket upgrade succeeds through gateway (/hosts/connect) | `specs/studio/realtime.spec.ts` | ❌ FAILED |
| MF-03 | MF assets have CORS headers (required for cross-origin MF loading) | `specs/studio/mf-proxy.spec.ts` | ❌ FAILED |
| RT-02 | SSE endpoint streams events (not buffered by proxy) | `specs/studio/realtime.spec.ts` | ❌ FAILED |
| MW-02 | every remoteEntryUrl resolves to application/javascript (not HTML) | `specs/studio/mf-widgets.spec.ts` | ❌ FAILED |
| MF-04 | JS chunks return 200 not redirect (proxy rewrite must not double-prefix) | `specs/studio/mf-proxy.spec.ts` | ⚠️ skipped |
| RT-02 | SSE endpoint streams events (not buffered by proxy) | `specs/studio/realtime.spec.ts` | ⚠️ skipped |
