---
id: S-022
title: S-022 — Trigger a workflow from Studio
persona: solo-developer
area: studio
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [studio]
---

## Goal

A project user starts a workflow from Studio, supplies its required inputs, and sees the resulting run.

## Prerequisites

- [ ] Project has an enabled workflow with a required input and Studio access.

## Steps

| # | Action | Expected |
|---|---|---|
| S-022.1 | Open the workflow in Studio and choose Run | Input form is displayed for declared inputs. |
| S-022.2 | Submit a valid input value | Studio accepts the request and creates a run. |
| S-022.3 | Open the run from the Runs view | New run appears for the selected project with its status and submitted inputs. |
