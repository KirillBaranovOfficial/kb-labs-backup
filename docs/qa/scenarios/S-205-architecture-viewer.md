---
id: S-205
title: Studio — browse and share an architecture package
persona: solo-developer
area: viewer-plugin
priority: P1
status: planned
release: false
modes: [local, secured]
surfaces: [studio, api]
---

## Goal

As a developer, I can browse a project's architecture map and documents in Studio, import or export a portable architecture package, and receive consistent, actionable results from the Viewer REST API.

## Prerequisites

- [ ] A registered project contains `docs/architecture/architecture.yaml` and its referenced diagrams and documents.
- [ ] Studio is available for the selected project; the Viewer plugin is installed and enabled.
- [ ] For import, a ZIP archive contains `architecture.yaml` at its root and valid relative document paths.

## Steps

| # | Action | Expected |
|---|---|---|
| S-205.1 | Open **Документы и архитектура** from the Studio plugin menu | The page shows the selected project's architecture package, a C1–C4 node tree, package files, and a document panel. While data is loading a loading state is visible; an empty or unavailable package has a distinct empty state. |
| S-205.2 | Select a node and open one of its Markdown or Mermaid documents | Studio shows the node's summary, level, parent breadcrumbs, case references, and linked documents. Markdown is rendered and Mermaid is shown as a diagram; selecting a node in the diagram selects the matching architecture node. |
| S-205.3 | Open a PDF, then open a DOC or DOCX document and choose **Преобразовать и открыть PDF** | The PDF is viewable with page and zoom controls and can be downloaded. Office conversion opens a PDF preview while preserving the original document. If conversion is unavailable or fails, Studio shows a safe actionable failure without a stack trace. |
| S-205.4 | In Studio, import a valid architecture ZIP, select the imported source, then export it | The imported source appears in the source selector and its architecture can be browsed. Export downloads a ZIP containing allowed package files; hidden or sensitive files are excluded and the UI reports their count. |
| S-205.5 | Request `GET /api/v1/plugins/viewer/sources` and `GET /api/v1/plugins/viewer/index?source=workspace` | Each successful JSON response is `{ "ok": true, "data": ... }`; source and index payloads are inside `data`. |
| S-205.6 | Request `GET /api/v1/plugins/viewer/file?source=workspace&path=README.md` for a valid file, then request a path outside the selected source | The valid file returns its supported content in the success envelope. The traversal request returns a non-2xx failure envelope with a catalogued `KB_*` code and an actionable hint; no file outside the project source is read. |
| S-205.7 | Submit an invalid import body, an unsafe ZIP path, or a ZIP without a root `architecture.yaml` | The request returns a non-2xx `{ "ok": false, "error": ... }` envelope with a catalogued `KB_*` code, plain-language message, and next step. It does not leave a partial source in the project. |
| S-205.8 | Open the Viewer page or call a Viewer REST route when the project has no readable architecture package, or when a route fails unexpectedly | Studio shows a distinct failed state with a safe `KB_*` code, message, actionable hint, and correlation ID where applicable; it never shows a raw error or stack. REST returns the shared failure envelope and preserves the appropriate HTTP status. |

## Contract notes

The Viewer currently exposes Studio and REST surfaces. The architecture plan's `kb viewer` CLI commands are proposed only and are not part of this case until they are implemented and registered.
