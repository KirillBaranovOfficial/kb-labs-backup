# state

> Distributed state management daemon for KB Labs platform services.

![license](https://img.shields.io/badge/license-MIT-green)
![platform](https://img.shields.io/badge/kb--labs-%3E%3D0.1.0-orange)
![tags](https://img.shields.io/badge/tags-state%20%7C%20distributed%20%7C%20infrastructure-lightgrey)

---

## Overview

State Daemon is a platform infrastructure service that provides distributed
state management via HTTP (State Broker). The `kb-create` host assigns its
loopback address at startup and passes it to child runtimes.

---

## Requirements

**KB Labs platform** `>= 0.1.0`

**Address:** host-assigned loopback address under `kb-create`; `kb-dev` uses
the port configured for the standalone development service.

---

## Usage

State Daemon is a platform service — it starts automatically with `kb-dev start`.

```bash
kb-dev start         # starts state daemon + all platform services
kb-dev status        # verify state-daemon is running
```

Use `kb-dev status` to check the standalone development service. Host-managed
StateBroker addresses are private runtime values and are not a fixed URL.

---

## Changelog

### 1.4.0

- State Broker HTTP server

---

## License

MIT
