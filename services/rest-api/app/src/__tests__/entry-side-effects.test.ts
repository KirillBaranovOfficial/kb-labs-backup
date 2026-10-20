import { beforeEach, describe, expect, it, vi } from "vitest";

const { runService, requireEnvironmentSecret } = vi.hoisted(() => ({ runService: vi.fn(), requireEnvironmentSecret: vi.fn() }));

vi.mock("@kb-labs/shared-daemon", () => ({ runService, requireEnvironmentSecret }));

describe("rest-api entrypoints", () => {
  beforeEach(() => {
    runService.mockReset();
    requireEnvironmentSecret.mockReset();
  });

  it("importing the library entry does not start the service", async () => {
    const entry = await import("../index");

    expect(runService).not.toHaveBeenCalled();
    expect(typeof entry.bootstrap).toBe("function");
    expect(typeof entry.setup).toBe("function");
  });

  it("bootstrap delegates to runService with the exported setup", async () => {
    const { bootstrap, setup } = await import("../index");
    runService.mockResolvedValue(undefined);

    await bootstrap("/some/root");

    expect(requireEnvironmentSecret).toHaveBeenCalledWith("GATEWAY_INTERNAL_SECRET");
    expect(runService).toHaveBeenCalledWith(
      expect.objectContaining({
        appId: "rest-api",
        serviceId: "rest",
        startDir: "/some/root",
        defaultPort: 5050,
        portEnvVar: "REST_API_PORT",
        setup,
      }),
    );
  });

  // case: S-038.1
  it('fails before starting when GATEWAY_INTERNAL_SECRET is missing', async () => {
    const { bootstrap } = await import('../index');
    requireEnvironmentSecret.mockImplementation(() => { throw new Error('KB_CONFIG_SECRET_MISSING: GATEWAY_INTERNAL_SECRET'); });

    await expect(bootstrap('/some/root')).rejects.toThrow('KB_CONFIG_SECRET_MISSING: GATEWAY_INTERNAL_SECRET');
    expect(runService).not.toHaveBeenCalled();
  });
});
