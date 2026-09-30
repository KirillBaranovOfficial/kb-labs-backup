import { describe, expect, it } from "vitest";
import { rejectsUnauthenticatedRequest } from "../mcp/auth-mode.js";

describe("MCP access mode", () => {
  it("allows tokenless requests in local mode", () => {
    expect(rejectsUnauthenticatedRequest("local", false)).toBe(false);
  });

  it("requires a verified gateway token in secured mode", () => {
    expect(rejectsUnauthenticatedRequest("secured", false)).toBe(true);
    expect(rejectsUnauthenticatedRequest("secured", true)).toBe(false);
  });
});
