/**
 * GET /sessions/:sessionId - Get session details
 */

import { defineHandler, PluginError, rethrowForRest, type RestInput, type PluginContextV3 } from '@kb-labs/sdk';
import { SessionManager } from '@kb-labs/agent-core';
import type { GetSessionResponse } from '@kb-labs/agent-contracts';

interface GetSessionRequest {
  sessionId?: string;
}

export default defineHandler({
  async execute(
    ctx: PluginContextV3,
    input: RestInput<GetSessionRequest>
  ): Promise<GetSessionResponse> {
    try {
      const params = input.params as Record<string, string> | undefined;
      const sessionId = params?.sessionId;

      if (!sessionId) {
        throw new Error('Session ID is required');
      }

      const sessionManager = new SessionManager(ctx.cwd);
      const session = await sessionManager.getSessionInfo(sessionId);

      if (!session) {
        throw new PluginError(
          'KB_RUNTIME_RESOURCE_NOT_FOUND',
          `Agent session ${sessionId} was not found.`,
          404,
          { sessionId },
        );
      }

      return { session };
    } catch (err) {
      rethrowForRest(err);
    }
  },
});
