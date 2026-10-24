import { createContextLogger } from '../../../core/platform/dist/index.js';
import { createServer } from '../../../services/marketplace/api/dist/index.js';
import { MarketplaceService } from '../../../services/marketplace/core/dist/index.js';

const root = process.argv[2];
if (!root) {
  throw new Error('Marketplace sync fixture requires the workspace root');
}

const baseLogger = {
  trace() {},
  debug() {},
  info() {},
  warn() {},
  error() {},
  fatal() {},
  async flush() {},
  child() { return baseLogger; },
};
const logger = createContextLogger(baseLogger, {
  applicationId: 'marketplace-sync-fixture',
  serviceId: 'marketplace',
  instanceId: String(process.pid),
  layer: 'service',
});
const service = new MarketplaceService({
  platformRoot: root,
  projectRoot: root,
  sources: [],
  logger,
});
const server = await createServer({ service, logger });
await server.listen({ host: '127.0.0.1', port: 0 });
const address = server.server.address();
if (address === null || typeof address === 'string') {
  throw new Error('Marketplace sync fixture did not bind a TCP port');
}
process.stdout.write(`${JSON.stringify({ url: `http://127.0.0.1:${address.port}` })}\n`);

process.once('SIGTERM', () => {
  void server.close().then(() => process.exit(0));
});
