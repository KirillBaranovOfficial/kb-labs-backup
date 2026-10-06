#!/usr/bin/env node
import { startHost } from "./host.js";
import { hostStartupFailure } from "./startup-error.js";

// process.cwd() = the directory platform/project roots are resolved from.
startHost({ startDir: process.cwd(), moduleUrl: import.meta.url }).catch(
  (error: unknown) => {
    // Keep startup failures machine-readable for kb-create's supervisor; runHost already logged the failure.
    process.stderr.write(`${JSON.stringify(hostStartupFailure(error))}\n`);
    // Setting exitCode lets the logger flush without duplicating a stack trace.
    process.exitCode = 1;
  },
);
