export function nodeVersionSupported(requiredMajor: number | string, actualVersion: string): boolean;
export function parseNodeEnginePolicy(value: unknown): { requiredMajor: number };
export function readNodeEngineDeclaration(packageManifest: string): string | undefined;
export function readNodeEnginePolicy(packageManifest: string): { requiredMajor: number };
export function runtimePackageManifest(moduleURL: string): string;
export function selectNodeExecutable(rootManifest: string, environment?: NodeJS.ProcessEnv): string;
