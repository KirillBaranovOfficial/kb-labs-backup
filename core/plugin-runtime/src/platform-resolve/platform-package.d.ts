/** The platform package set redirected to the host's installed copy. */
export declare function isPlatformPackage(name: string): boolean;

/** Package name of a bare platform specifier, or undefined when it is not a platform package. */
export declare function platformPackageOf(specifier: string): string | undefined;
