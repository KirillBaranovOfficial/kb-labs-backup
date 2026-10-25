export declare function nodeVersionSupported(requiredMajor: number | string, actualVersion: string): boolean;
export declare function readNodeEnginePolicy(packageManifest: string): { requiredMajor: number };
export declare function runtimePackageManifest(moduleURL: string): string;
export declare function selectNodeExecutable(
  rootManifest: string,
  environment?: NodeJS.ProcessEnv,
): string;
