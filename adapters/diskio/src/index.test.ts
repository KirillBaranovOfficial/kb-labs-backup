import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { access, mkdtemp, rm, symlink } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { AdapterConfigError } from "@kb-labs/sdk";
import { createAdapter } from "./index.js";
import { manifest } from "./manifest.js";

describe("disk storage", () => {
  let root: string;

  beforeEach(async () => {
    root = await mkdtemp(join(tmpdir(), "disk-storage-"));
  });

  afterEach(async () => {
    await rm(root, { recursive: true, force: true });
  });

  it("round-trips nested binary data and reports metadata", async () => {
    const store = createAdapter({ baseDir: root });
    const payload = Buffer.from([0, 1, 2, 255]);
    await store.write("a/b/blob.bin", payload);

    expect(await store.read("a/b/blob.bin")).toEqual(payload);
    expect(await store.list("a/")).toEqual(["a/b/blob.bin"]);
    expect((await store.stat("a/b/blob.bin"))?.size).toBe(payload.length);
    expect((await store.stat("a/b/blob.bin"))?.contentType).toBe("application/octet-stream");
  });

  it("roots the platform storage option in the platform data directory", async () => {
    expect(manifest.contexts).toContain("platformData");
    expect(manifest.configSchema?.baseDir).toMatchObject({ pathBase: "platformData", default: "storage" });

    const platformDataRoot = join(root, "platform", ".kb", "data");
    const store = createAdapter({ platformData: { dataRoot: platformDataRoot } });
    await store.write("platform.txt", Buffer.from("platform-owned"));

    expect(await store.read("platform.txt")).toEqual(Buffer.from("platform-owned"));
    await expect(access(join(platformDataRoot, "storage", "platform.txt"))).resolves.toBeUndefined();
    await expect(access(join(root, "storage", "platform.txt"))).rejects.toMatchObject({ code: "ENOENT" });
  });

  it("requires the platform data context when no base directory is configured", () => {
    expect(() => createAdapter()).toThrowError(AdapterConfigError);
  });

  it("treats missing reads and deletes as harmless", async () => {
    const store = createAdapter({ baseDir: root });
    expect(await store.read("missing.txt")).toBeNull();
    await expect(store.delete("missing.txt")).resolves.toBeUndefined();
    expect(await store.exists("missing.txt")).toBe(false);
  });

  // case: S-211.5
  it("surfaces non-missing filesystem errors from exists", async () => {
    const store = createAdapter({ baseDir: root });
    await symlink('loop', join(root, 'loop'));

    await expect(store.exists('loop')).rejects.toMatchObject({ code: 'ELOOP' });
  });

  it("rejects paths that resolve outside the configured root", async () => {
    const store = createAdapter({ baseDir: root });
    await expect(store.read("../outside.txt")).rejects.toThrow("Path traversal detected");
    await expect(store.write("/tmp/outside.txt", Buffer.from("x"))).rejects.toThrow("Path traversal detected");
  });

  it("copies and moves files while creating destination folders", async () => {
    const store = createAdapter({ baseDir: root });
    await store.write("source.txt", Buffer.from("value"));
    await store.copy("source.txt", "copies/copy.txt");
    await store.move("source.txt", "archive/moved.txt");

    expect(await store.read("copies/copy.txt")).toEqual(Buffer.from("value"));
    expect(await store.exists("source.txt")).toBe(false);
    expect(await store.exists("archive/moved.txt")).toBe(true);
  });
});
