import { toErrorEnvelope, type ErrorEnvelope } from "@kb-labs/core-platform";
import { HostExposureRefusedError } from "./errors.js";

export function hostStartupFailure(error: unknown): { ok: false; error: ErrorEnvelope } {
  const envelope = error instanceof HostExposureRefusedError
    ? error.envelope
    : toErrorEnvelope(error);
  return { ok: false, error: envelope };
}
