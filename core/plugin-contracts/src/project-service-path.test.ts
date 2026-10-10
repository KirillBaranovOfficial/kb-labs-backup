import { describe, expect, it } from "vitest";
import { projectServicePath } from "./project-service-path.js";

describe("projectServicePath", () => {
  it("builds a service-namespaced URL without an internal mount prefix", () => {
    expect(projectServicePath("p1", "rest", "/analytics/events?type=created")).toBe(
      "/api/v1/projects/p1/rest/analytics/events?type=created",
    );
  });

  it("encodes path segment identities and supports the service root", () => {
    expect(projectServicePath("project/one", "mcp")).toBe(
      "/api/v1/projects/project%2Fone/mcp",
    );
  });
});
