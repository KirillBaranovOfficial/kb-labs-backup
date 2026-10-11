const gatewayPathRoots: ReadonlySet<string> = new Set([
  "auth", "health", "ready", "metrics", "observability",
  "hosts", "clients", "internal", "webhooks", "telemetry", "docs", "docs-all",
  "openapi.json", "openapi-merged.json", "echo",
]);

export function isGatewayPath(pathname: string): boolean {
  if (pathname === "/api" || pathname.startsWith("/api/")
    || pathname === "/platform" || pathname.startsWith("/platform/")) {
    return true;
  }
  const first = pathname.split("/").filter(Boolean)[0];
  return first !== undefined && gatewayPathRoots.has(first);
}
