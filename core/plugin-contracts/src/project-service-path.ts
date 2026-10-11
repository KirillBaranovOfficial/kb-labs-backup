/** Build the public URL for one service in a registered project. */
export function projectServicePath(
  projectId: string,
  serviceId: string,
  path = "",
): string {
  const suffix = path.replace(/^\/+/, "");
  return `/api/v1/projects/${encodeURIComponent(projectId)}/${encodeURIComponent(serviceId)}${suffix ? `/${suffix}` : ""}`;
}
