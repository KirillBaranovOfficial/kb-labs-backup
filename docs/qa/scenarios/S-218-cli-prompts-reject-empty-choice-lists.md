---
id: S-218
title: CLI prompts — reject empty choice lists as invalid input
persona: plugin-author
area: cli
priority: P2
status: active
release: false
modes: [local]
surfaces: [cli, agent]
---

## Goal

Help a plugin author identify an invalid select prompt definition instead of
silently receiving an empty result or an unrelated runtime exception.

## Steps

| # | Action | Expected |
|---|---|---|
| S-218.1 | Call the single-select and multi-select prompts with the message `Choose deployment` and an empty choice list | Each call fails with catalogued `KB_RUNTIME_INPUT_INVALID`; the cause names `Choose deployment` and explains that no choices were supplied. Neither prompt returns a value or an empty list, and neither throws a `TypeError`. |
