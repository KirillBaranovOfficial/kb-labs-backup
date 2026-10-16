export type GeneratedDocSource = { source: string; examples: string[] };
export type GeneratedNodePolicy = { source: string; requiredMajor: number };
export type GeneratedSources = {
  commands: Record<string, GeneratedDocSource>;
  nodePolicy: GeneratedNodePolicy;
};

export function renderGeneratedDocSource(
  command: string,
  generatedSources: Record<string, GeneratedDocSource>,
  match?: string,
): string;
export function renderNodePolicy(generatedSources: GeneratedSources): string;
export function syncDocSources(mdx: string, docPath: string | undefined, generatedSources: GeneratedSources): string;
