import { describe, expect, it, vi } from "vitest";
import { finishHostStop } from "./harness.js";

describe("host test harness shutdown", () => {
  it("propagates a shutdown handler rejection and still cleans up", async () => {
    const shutdownError = new Error("injected shutdown handler rejection");
    const processExited = new Promise<number>(() => {});
    const startShutdown = vi.fn(() => [Promise.reject(shutdownError)]);
    const cleanup = vi.fn(async () => {});

    await expect(
      finishHostStop(startShutdown, processExited, cleanup, 100),
    ).rejects.toBe(shutdownError);
    expect(startShutdown).toHaveBeenCalledOnce();
    expect(cleanup).toHaveBeenCalledOnce();
  });
});
