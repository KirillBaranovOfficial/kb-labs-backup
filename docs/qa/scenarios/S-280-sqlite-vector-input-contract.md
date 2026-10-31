---
id: S-280
title: SQLite vector storage rejects invalid dimensions and values
persona: plugin-author
area: platform-core
priority: P1
status: planned
release: false
modes: [local]
surfaces: [api]
---

## User value

A developer using the local SQLite vector adapter can trust that stored vectors are finite, non-empty numeric arrays and that searches compare vectors with compatible dimensions.

## Preconditions

- [ ] The SQLite vector-store adapter is available to the platform.

## Steps

| # | Action | Expected |
|---|---|---|
| S-280.1 | Through the vector-store adapter, upsert an empty vector, a vector containing `NaN` or an infinity, and a zero-magnitude vector | Each call rejects with `KB_RUNTIME_INPUT_INVALID`, identifies `field: vector`, and does not store the record. The hint comes from the error catalogue. |
| S-280.2 | Store a non-empty finite vector, then search with an empty vector, a non-finite vector, or a different dimension | Each search rejects with `KB_RUNTIME_INPUT_INVALID` and `field: vector`; no result is returned with a synthetic zero similarity score. |
| S-280.3 | Upsert records whose vectors have different dimensions in the same namespace, then attempt a different dimension in a populated namespace | Each call rejects with `KB_RUNTIME_INPUT_INVALID` before writing any record from that call. |
