---
id: S-200
title: Projects — adapter settings are platform-owned
persona: solo-developer
area: config
priority: P1
status: historical
release: false
modes: [local, secured]
surfaces: [cli, studio]
superseded_by: S-048
---

## Goal

This case recorded a proposed per-project adapter and model configuration
boundary. The owner decision is now platform-only: adapter bindings, adapter
options, LLM credentials, and model selection belong to platform config. A
project can attach plugins but cannot override these settings.

The active behavior is covered by [S-048](S-048-project-adapter-binding.md).
