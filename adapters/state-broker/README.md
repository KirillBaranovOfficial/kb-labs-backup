# `@kb-labs/adapters-state-broker`

`ICache` adapter backed by the KB Labs StateBroker daemon shipped with the platform. The default install connects to that local service; it does not require Redis or another external cache service. If the daemon is unavailable, cache operations fail and the platform health check reports the service problem. The adapter does not switch silently to an in-memory cache.
