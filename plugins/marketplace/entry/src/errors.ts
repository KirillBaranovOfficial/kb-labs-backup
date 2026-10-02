import { handleError } from '@kb-labs/shared-cli-ui';

interface MarketplaceErrorContext {
  ui?: {
    json?: (payload: unknown) => void;
    error?: (message: string, options?: Record<string, unknown>) => void;
  };
}

export function handleMarketplaceError(ctx: MarketplaceErrorContext, error: unknown, isJson?: boolean): void {
  handleError(ctx, error, isJson);
}
