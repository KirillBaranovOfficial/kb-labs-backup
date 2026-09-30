---
id: S-021
title: S-021 — View workflow runs in Studio
persona: solo-developer
area: studio
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [studio]
---

## Goal

A Studio user selects a project and inspects workflow run history and one run’s details.

## Prerequisites

- [ ] Studio host and registered project with completed workflow runs.

## Steps

| # | Action | Expected |
|---|---|---|
| S-021.1 | Open the project’s workflow Runs view in Studio | Runs belonging to the selected project are listed. |
| S-021.2 | Select a completed and a failed run | Each detail view displays the correct terminal status. |
| S-021.3 | Open run logs | Step-level output and failure details are visible. |
