---
id: S-121
title: Studio — Inspect analytics and filter measured usage
persona: solo-developer
area: studio
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [studio]
---

## Goal

Review tracked platform events and adapter usage in Studio, then narrow a metric to the desired period and model.

## Prerequisites

- [ ] Analytics adapter configured and seeded with events from two sources/types and LLM usage for at least two models on distinct dates.
- [ ] At least one adapter of each shown type is configured for its detailed usage view.

## Steps

| # | Action | Expected |
|---|--------|----------|
| S-121.1 | Open `/analytics/overview` | Overview shows Total Events, Event Types, Sources, and Actors from the analytics adapter, plus usage cards for LLM, Embeddings, Vector Store, Cache, and Storage. |
| S-121.2 | Open `/analytics/events` and choose one event type and source represented in the fixture | The event table contains only matching rows and each row shows its type, timestamp, source, and actor when present. |
| S-121.3 | Open `/analytics/llm` and choose `Last 7 days` | Request and cost metrics use events within the selected date range and the page displays its date-range control. |
| S-121.4 | On the LLM Usage page, select one model in the model filter | The Daily Usage Trend chart reflects only the selected model; clearing the filter restores all models for the selected date range. |
| S-121.5 | Open an event row | The event detail drawer shows its exact event type, time, source product/version, actor when present, and event payload. |
| S-121.6 | Open Analytics when no analytics adapter is configured | Studio displays `Analytics Not Configured` with guidance to configure an analytics adapter; it does not present an empty chart as measured zero usage. |
