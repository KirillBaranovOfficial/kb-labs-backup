import { promises as fs } from 'node:fs';
import path from 'node:path';

interface PlatformLayoutContract {
  platformDataRoot?: {
    platformPath?: string;
    statePath?: string;
  };
}

/** Resolve the platform data projection from the Go-owned layout contract. */
export async function resolvePlatformDataRoot(platformRoot: string, development?: boolean): Promise<string> {
  const installedContractPath = path.join(platformRoot, '.kb/v2/platform-layout.json');
  const developmentContractPath = path.join(platformRoot, 'tools/kb-create/v2/contracts/platform-layout.json');
  const contractPath = development === true
    ? developmentContractPath
    : development === false
      ? installedContractPath
      : await selectPlatformLayoutContract(installedContractPath, developmentContractPath);
  const installed = contractPath === installedContractPath;
  let contract: PlatformLayoutContract;
  try {
    contract = JSON.parse(await fs.readFile(contractPath, 'utf8')) as PlatformLayoutContract;
  } catch (error) {
    throw new Error(`Cannot read platform data-root layout contract at ${contractPath}: ${error instanceof Error ? error.message : String(error)}`);
  }
  const platformPath = contract.platformDataRoot?.platformPath;
  const statePath = contract.platformDataRoot?.statePath;
  if (!platformPath || !statePath || path.isAbsolute(platformPath) || path.isAbsolute(statePath)) {
    throw new Error(`Platform layout contract at ${contractPath} has no valid platformDataRoot paths.`);
  }
  const projection = path.resolve(platformRoot, platformPath);
  if (!installed) return projection;
  try {
    return await fs.realpath(projection);
  } catch (error) {
    throw new Error(`Cannot resolve platform data-root projection ${projection}: ${error instanceof Error ? error.message : String(error)}`);
  }
}

async function selectPlatformLayoutContract(installedContractPath: string, developmentContractPath: string): Promise<string> {
  try {
    await fs.access(installedContractPath);
    return installedContractPath;
  } catch (error) {
    if (!isMissingPath(error)) throw error;
  }
  try {
    await fs.access(developmentContractPath);
    return developmentContractPath;
  } catch (error) {
    if (!isMissingPath(error)) throw error;
    return installedContractPath;
  }
}

function isMissingPath(error: unknown): boolean {
  return error instanceof Error && 'code' in error && error.code === 'ENOENT';
}
