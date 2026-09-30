export type McpAuthMode = "local" | "secured";

export function rejectsUnauthenticatedRequest(
  mode: McpAuthMode,
  authenticated: boolean,
): boolean {
  return mode === "secured" && !authenticated;
}
