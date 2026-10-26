export interface NodeEnginePolicy {
  requiredMajor: number;
}

export declare function parseNodeEnginePolicy(value: unknown): NodeEnginePolicy;
export declare function nodeVersionSupported(requiredMajor: number, actualVersion: string): boolean;
export declare function readNodeEnginePolicy(packageManifest: string): NodeEnginePolicy;
export declare function readNodeEngineDeclaration(packageManifest: string): string | undefined;
export declare function runtimePackageManifest(moduleURL: string | URL): string;
export declare function selectNodeExecutable(rootManifest: string, environment?: NodeJS.ProcessEnv): string;
