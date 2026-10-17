import { mcpToolError } from './request-handler.js';

// @ts-expect-error MCP callers cannot add undeclared diagnostic details.
mcpToolError('KB_AUTH_REQUIRED', { gatewayUrl: 'https://gateway.test', undeclared: 'missing' });
