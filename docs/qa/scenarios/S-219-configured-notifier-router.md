---
id: S-219
title: Configured notifier adapter publishes notifications
persona: platform-maintainer
area: platform-core
priority: P1
status: active
release: false
modes: [local]
surfaces: [api]
---

## Goal

Bind the workspace notifier router through the platform adapter configuration
and deliver a notification to the platform event bus.

## Steps

| # | Action | Expected |
|---|---|---|
| S-219.1 | Configure `adapters.notifier` as `@kb-labs/notifier-router`, configure an `ops` channel with `type: telegram` and test credentials, route `info` notifications to `ops`, start the platform, and have a caller invoke `platform.notifier.notify` with an `info` notification | The generic adapter loader constructs the configured notifier and its declared channel service; `platform.notifier.notify` publishes `notification.emitted`, and the configured Telegram channel receives the notification through its HTTP client. |
